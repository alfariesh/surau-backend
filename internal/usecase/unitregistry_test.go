package usecase_test

import (
	"bytes"
	"context"
	"crypto/sha256"
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

//nolint:wsl_v5 // ordered mock stages mirror the two-phase reconcile transaction sequence
func TestUnitRegistryInitialEditedBookPersistsRawThenEffectiveLineage(t *testing.T) {
	t.Parallel()

	uc, repo := newUnitRegistryFixture(t)
	ctx := context.Background()
	src := unitTestSource()
	src.Pages[0].RawContentHTML = "النص الخام قبل التحرير"
	src.Pages[0].RawContentText = "النص الخام قبل التحرير"
	src.Pages[0].ContentHTML = "النص المنشور بعد التحرير"
	src.Pages[0].ContentText = "النص المنشور بعد التحرير"
	src.Pages[0].HasPublishedEdit = true
	src.Pages[0].EditActorID = "editor"

	var rawPlan entity.UnitReconcilePlan
	repo.EXPECT().LoadBookSource(ctx, 797).Return(src, nil).Times(2)
	repo.EXPECT().Snapshot(ctx, 797).Return(emptyUnitSnapshot(), nil)
	repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, plan *entity.UnitReconcilePlan) error {
			rawPlan = *plan
			assert.True(t, rawPlan.Intermediate, "raw pass must keep the public unit view stale")
			require.Len(t, rawPlan.Mints, 1)
			assert.Equal(t, "النص الخام قبل التحرير", rawPlan.Mints[0].Text)

			return nil
		},
	)
	repo.EXPECT().Snapshot(ctx, 797).DoAndReturn(func(context.Context, int) (entity.UnitRegistrySnapshot, error) {
		return entity.UnitRegistrySnapshot{
			Active:            append([]entity.CitableUnit(nil), rawPlan.Mints...),
			MaxOrdinalByScope: map[int]int{0: 1},
			ExistingIDs:       map[string]struct{}{rawPlan.Mints[0].ID: {}},
		}, nil
	})
	repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, plan *entity.UnitReconcilePlan) error {
			assert.False(t, plan.Intermediate, "effective pass is the only materialization finalizer")
			require.Len(t, plan.Mints, 1)
			require.Len(t, plan.Edges, 1)
			assert.Equal(t, rawPlan.Mints[0].ID, plan.Edges[0].PredecessorID)
			assert.Equal(t, plan.Mints[0].ID, plan.Edges[0].SuccessorID)

			return nil
		},
	)

	report, err := uc.ReconcileBook(ctx, 797)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Minted)
	assert.Equal(t, 1, report.Superseded)
}

//nolint:wsl_v5 // ordered mock assertions mirror the single atomic historical migration plan
func TestUnitRegistryExistingPilotAddsRawHistoryWithoutReplacingActiveID(t *testing.T) {
	t.Parallel()

	uc, repo := newUnitRegistryFixture(t)
	ctx := context.Background()
	src := unitTestSource()
	src.Pages[0].RawContentHTML = "النص الخام قبل التحرير 😀"
	src.Pages[0].RawContentText = "النص الخام قبل التحرير 😀"
	src.Pages[0].ContentHTML = "النص المنشور بعد التحرير"
	src.Pages[0].ContentText = "النص المنشور بعد التحرير"
	src.Pages[0].HasPublishedEdit = true
	src.Pages[0].EditActorID = "editor"

	effective, _, err := unitregistry.DeriveBook(&src)
	require.NoError(t, err)
	seed := unitregistry.PlanBook(src.BookID, src.LoadedAt, effective, &entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: map[int]int{}, ExistingIDs: map[string]struct{}{},
	})
	require.Len(t, seed.Mints, 1)
	active := seed.Mints[0]
	activeID := active.ID
	active.SourceDocumentHash = nil // exact pre-K-1 pilot shape
	active.SourceCharStart = nil
	active.SourceCharEnd = nil

	snap := entity.UnitRegistrySnapshot{
		Active:            []entity.CitableUnit{active},
		MaxOrdinalByScope: map[int]int{0: active.Ordinal},
		ExistingIDs:       map[string]struct{}{active.ID: {}},
	}
	repo.EXPECT().LoadBookSource(ctx, src.BookID).Return(src, nil)
	repo.EXPECT().Snapshot(ctx, src.BookID).Return(snap, nil)
	repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, plan *entity.UnitReconcilePlan) error {
			require.Empty(t, plan.Mints, "effective pilot unit must retain its UUID")
			require.Empty(t, plan.Retires, "active pilot unit must not be retired")
			require.Len(t, plan.Updates, 1)
			assert.Equal(t, activeID, plan.Updates[0].ID)
			require.Len(t, plan.HistoricalMints, 1)

			historical := plan.HistoricalMints[0]
			assert.NotEqual(t, activeID, historical.ID)
			assert.Equal(t, entity.UnitLifecycleSuperseded, historical.Lifecycle)
			assert.Equal(t, entity.ProvenanceClassSource, historical.ProvenanceClass)
			assert.Equal(t, true, historical.ProvenanceDetail["raw_snapshot"])
			require.NotNil(t, historical.SourceCharStart)
			require.NotNil(t, historical.SourceCharEnd)
			assert.Equal(t, 0, *historical.SourceCharStart)
			assert.Equal(t, len([]rune(src.Pages[0].RawContentText)), *historical.SourceCharEnd)
			wantHash := sha256.Sum256([]byte(src.Pages[0].RawContentText))
			assert.True(t, bytes.Equal(wantHash[:], historical.SourceDocumentHash))

			foundLineage := false
			for _, edge := range plan.Edges {
				if edge.PredecessorID == historical.ID && edge.SuccessorID == activeID {
					foundLineage = true
				}
			}
			assert.True(t, foundLineage, "historical Anchor must resolve to unchanged active pilot ID")
			assert.Equal(t, int64(1), plan.ExpectedActive)

			return nil
		},
	)

	report, err := uc.ReconcileBook(ctx, src.BookID)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Minted)
	assert.Equal(t, 1, report.Superseded)
}

//nolint:wsl_v5 // ordered mock assertions inspect the full historical decision set
func TestUnitRegistryDeletedRawParagraphDoesNotRedirectToUnrelatedText(t *testing.T) {
	t.Parallel()

	uc, repo := newUnitRegistryFixture(t)
	ctx := context.Background()
	src := unitTestSource()
	src.Pages[0].RawContentHTML = "فقرة محذوفة بالكامل\nفقرة ثابتة"
	src.Pages[0].RawContentText = src.Pages[0].RawContentHTML
	src.Pages[0].ContentHTML = "فقرة ثابتة"
	src.Pages[0].ContentText = src.Pages[0].ContentHTML
	src.Pages[0].HasPublishedEdit = true
	src.Pages[0].EditActorID = "editor"

	effective, _, err := unitregistry.DeriveBook(&src)
	require.NoError(t, err)
	seed := unitregistry.PlanBook(src.BookID, src.LoadedAt, effective, &entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: map[int]int{}, ExistingIDs: map[string]struct{}{},
	})
	require.Len(t, seed.Mints, 1)
	active := seed.Mints[0]
	active.SourceDocumentHash = nil
	active.SourceCharStart = nil
	active.SourceCharEnd = nil
	snap := entity.UnitRegistrySnapshot{
		Active:            []entity.CitableUnit{active},
		MaxOrdinalByScope: map[int]int{0: active.Ordinal},
		ExistingIDs:       map[string]struct{}{active.ID: {}},
	}

	repo.EXPECT().LoadBookSource(ctx, src.BookID).Return(src, nil)
	repo.EXPECT().Snapshot(ctx, src.BookID).Return(snap, nil)
	repo.EXPECT().ApplyReconcile(ctx, gomock.Any()).DoAndReturn(
		func(_ context.Context, plan *entity.UnitReconcilePlan) error {
			// The unchanged raw paragraph is already represented by the active
			// effective unit. Only the deleted raw paragraph needs a separate
			// historical record.
			require.Len(t, plan.HistoricalMints, 1)
			var deleted *entity.CitableUnit
			for i := range plan.HistoricalMints {
				if plan.HistoricalMints[i].Text == "فقرة محذوفة بالكامل" {
					deleted = &plan.HistoricalMints[i]
				}
			}
			require.NotNil(t, deleted)
			assert.Equal(t, entity.UnitLifecycleTombstoned, deleted.Lifecycle)
			for _, edge := range plan.Edges {
				assert.NotEqual(t, deleted.ID, edge.PredecessorID,
					"deleted raw paragraph must not redirect to an unrelated survivor")
			}

			return nil
		},
	)

	report, err := uc.ReconcileBook(ctx, src.BookID)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Tombstoned)
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
