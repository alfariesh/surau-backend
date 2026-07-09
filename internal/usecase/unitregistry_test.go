package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newUnitRegistryFixture(t *testing.T) (*unitregistry.UseCase, *MockCitableUnitRepo) {
	t.Helper()

	ctrl := gomock.NewController(t)
	repo := NewMockCitableUnitRepo(ctrl)

	return unitregistry.New(repo), repo
}

func unitTestSource() entity.BookUnitSource {
	return entity.BookUnitSource{
		BookID:     797,
		ReleaseKey: "1.0",
		Pages: []entity.BookUnitSourcePage{
			{PageID: 1, ContentHTML: "فقرة واحدة للاختبار العام"},
		},
		LoadedAt: time.Date(2026, 7, 9, 2, 0, 0, 0, time.UTC),
	}
}

func emptyUnitSnapshot() entity.UnitRegistrySnapshot {
	return entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: map[int]int{},
		ExistingIDs:       map[string]struct{}{},
	}
}

func TestUnitRegistryReconcileRetriesOnConflict(t *testing.T) {
	t.Parallel()

	uc, repo := newUnitRegistryFixture(t)
	ctx := context.Background()

	repo.EXPECT().LoadBookSource(ctx, 797).Return(unitTestSource(), nil).Times(3)
	repo.EXPECT().Snapshot(ctx, 797).Return(emptyUnitSnapshot(), nil).Times(3)
	gomock.InOrder(
		repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).Return(entity.ErrUnitReconcileConflict),
		repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).Return(entity.ErrUnitReconcileConflict),
		repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, plan *entity.UnitReconcilePlan) error {
				assert.Equal(t, 3, plan.Report.Attempts)
				assert.Equal(t, 1, plan.Report.Minted)

				return nil
			},
		),
	)

	report, err := uc.ReconcileBook(ctx, 797)
	require.NoError(t, err)
	assert.Equal(t, 3, report.Attempts)
}

func TestUnitRegistryReconcileGivesUpAfterRetries(t *testing.T) {
	t.Parallel()

	uc, repo := newUnitRegistryFixture(t)
	ctx := context.Background()

	repo.EXPECT().LoadBookSource(ctx, 797).Return(unitTestSource(), nil).Times(3)
	repo.EXPECT().Snapshot(ctx, 797).Return(emptyUnitSnapshot(), nil).Times(3)
	repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).Return(entity.ErrUnitReconcileConflict).Times(3)

	_, err := uc.ReconcileBook(ctx, 797)
	require.ErrorIs(t, err, entity.ErrUnitReconcileConflict)
}

func TestUnitRegistryPublishHookSkipsUnderivedBook(t *testing.T) {
	t.Parallel()

	uc, repo := newUnitRegistryFixture(t)
	ctx := context.Background()

	repo.EXPECT().BookDerivedAt(ctx, 42).Return(nil, nil)

	_, skipped, err := uc.ReconcileBookIfDerived(ctx, 42)
	require.NoError(t, err)
	assert.True(t, skipped, "book without initial backfill must be a no-op")
}

func TestUnitRegistryPublishHookReconcilesDerivedBook(t *testing.T) {
	t.Parallel()

	uc, repo := newUnitRegistryFixture(t)
	ctx := context.Background()
	derivedAt := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)

	repo.EXPECT().BookDerivedAt(ctx, 797).Return(&derivedAt, nil)
	repo.EXPECT().LoadBookSource(ctx, 797).Return(unitTestSource(), nil)
	repo.EXPECT().Snapshot(ctx, 797).Return(emptyUnitSnapshot(), nil)
	repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).Return(nil)

	report, skipped, err := uc.ReconcileBookIfDerived(ctx, 797)
	require.NoError(t, err)
	assert.False(t, skipped)
	assert.Equal(t, 1, report.Minted)
}

func TestUnitRegistryAuditPassDetectsHashMismatch(t *testing.T) {
	t.Parallel()

	uc, repo := newUnitRegistryFixture(t)
	ctx := context.Background()

	goodText := "نص سليم لم يمسه أحد"
	tampered := "نص عُدل خارج الخدمة"

	units := []entity.CitableUnit{
		{
			ID: "u-ok", Kind: entity.UnitKindParagraph, Text: goodText,
			ContentHash:          unitregistry.ContentHash(entity.UnitKindParagraph, "", goodText),
			TextNormalized:       "irrelevant-for-old-version",
			NormalizationVersion: 999, // future profile: normalization check skipped
		},
		{
			ID: "u-tampered", Kind: entity.UnitKindParagraph, Text: tampered,
			ContentHash:          unitregistry.ContentHash(entity.UnitKindParagraph, "", goodText),
			NormalizationVersion: 1,
		},
	}

	repo.EXPECT().AuditCounts(ctx).Return(entity.CitableAuditReport{UnitsByLifecycle: map[string]int64{}}, nil)
	repo.EXPECT().ListActiveUnitsForHashCheck(ctx).Return(units, nil)

	report, err := uc.AuditPass(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), report.Violations.HashMismatch)
	assert.False(t, report.RanAt.IsZero())
}
