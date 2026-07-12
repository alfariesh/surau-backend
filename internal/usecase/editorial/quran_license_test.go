package editorial

import (
	"context"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeQuranSourceLicenseRepo struct {
	items             []entity.QuranSourceLicense
	total             int
	license           entity.QuranSourceLicense
	err               error
	sourceKind        string
	status            string
	limit             uint64
	offset            uint64
	actorID           string
	update            entity.QuranSourceLicenseUpdate
	expectedUpdatedAt *time.Time
}

func (f *fakeQuranSourceLicenseRepo) ListQuranSourceLicenses(
	_ context.Context,
	sourceKind, status string,
	limit, offset uint64,
) ([]entity.QuranSourceLicense, int, error) {
	f.sourceKind, f.status, f.limit, f.offset = sourceKind, status, limit, offset

	return f.items, f.total, f.err
}

func (f *fakeQuranSourceLicenseRepo) GetQuranSourceLicense(
	_ context.Context,
	sourceKind, _ string,
) (entity.QuranSourceLicense, error) {
	f.sourceKind = sourceKind

	return f.license, f.err
}

//nolint:gocritic // value parameter mirrors the production repository interface
func (f *fakeQuranSourceLicenseRepo) UpdateQuranSourceLicense(
	_ context.Context,
	actorID string,
	update entity.QuranSourceLicenseUpdate,
	expectedUpdatedAt *time.Time,
) (entity.QuranSourceLicense, error) {
	f.actorID = actorID
	f.update = update
	f.expectedUpdatedAt = expectedUpdatedAt

	return f.license, f.err
}

func TestQuranSourceLicensesDefaultsAndClamps(t *testing.T) {
	t.Parallel()

	fake := &fakeQuranSourceLicenseRepo{total: 3}
	uc := &UseCase{quranLicense: fake}

	got, err := uc.QuranSourceLicenses(t.Context(), " ", " ", 1000, 999999)
	require.NoError(t, err)
	assert.NotNil(t, got.Items)
	assert.Equal(t, 3, got.Total)
	assert.Empty(t, fake.sourceKind)
	assert.Equal(t, entity.LicenseAuditStatusUnresolved, fake.status)
	assert.Equal(t, uint64(200), fake.limit)
	assert.Equal(t, uint64(10000), fake.offset)
}

func TestUpdateQuranSourceLicenseNormalizesDecisionAndAttribution(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.July, 12, 1, 2, 3, 0, time.UTC)
	evidence := "  https://example.org/quran-license  "
	translator := "  Penerjemah  "
	responsibleName := "  Penerbit  "
	responsibleRole := "  publisher  "
	fake := &fakeQuranSourceLicenseRepo{license: entity.QuranSourceLicense{
		SourceKind: entity.QuranSourceKindTranslation,
		SourceID:   "source-id",
		UpdatedAt:  updatedAt,
	}}
	uc := &UseCase{quranLicense: fake}

	_, err := uc.UpdateQuranSourceLicense(
		t.Context(), " actor-id ", " translation ", " source-id ", " permitted ",
		"  Permission received. ", &evidence, &translator, &responsibleName,
		&responsibleRole, &updatedAt,
	)
	require.NoError(t, err)
	assert.Equal(t, "actor-id", fake.actorID)
	assert.Equal(t, entity.QuranSourceLicenseUpdate{
		SourceKind:      entity.QuranSourceKindTranslation,
		SourceID:        "source-id",
		LicenseStatus:   entity.LicenseStatusPermitted,
		Reason:          "Permission received.",
		EvidenceURL:     new("https://example.org/quran-license"),
		Translator:      new("Penerjemah"),
		ResponsibleName: new("Penerbit"),
		ResponsibleRole: new("publisher"),
	}, fake.update)
	assert.Equal(t, &updatedAt, fake.expectedUpdatedAt)
}

func TestQuranSourceLicenseValidatesInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		sourceKind string
		sourceID   string
		status     string
		reason     string
		evidence   *string
		want       error
	}{
		{name: "source kind", sourceKind: "recitation", sourceID: "x", status: "permitted", reason: "ok", want: entity.ErrQuranSourceNotFound},
		{name: "source id", sourceKind: "translation", sourceID: " ", status: "permitted", reason: "ok", want: entity.ErrQuranSourceNotFound},
		{name: "status", sourceKind: "translation", sourceID: "x", status: "copyrighted", reason: "ok", want: entity.ErrInvalidLicenseStatus},
		{name: "reason", sourceKind: "translation", sourceID: "x", status: "permitted", reason: " ", want: entity.ErrInvalidLicenseReason},
		{name: "evidence", sourceKind: "translation", sourceID: "x", status: "permitted", reason: "ok", evidence: new("file:///tmp/license"), want: entity.ErrInvalidLicenseEvidenceURL},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			uc := &UseCase{quranLicense: &fakeQuranSourceLicenseRepo{}}
			_, err := uc.UpdateQuranSourceLicense(
				t.Context(), "actor", test.sourceKind, test.sourceID, test.status,
				test.reason, test.evidence, nil, nil, nil, nil,
			)
			assert.ErrorIs(t, err, test.want)
		})
	}
}
