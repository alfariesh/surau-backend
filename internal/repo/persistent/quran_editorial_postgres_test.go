package persistent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuranEditorialImportValidationSortsAndRejectsDuplicates(t *testing.T) {
	t.Parallel()

	surahPatches := []QuranSurahEditorialPatch{
		{SurahID: 3, Lang: "id"},
		{SurahID: 2, Lang: "en"},
		{SurahID: 2, Lang: "ar"},
	}
	metadata := []QuranSurahMetadataUpdate{{SurahID: 3}, {SurahID: 1}}
	require.NoError(t, sortAndValidateSurahImport(surahPatches, metadata))
	assert.Equal(t, []int{2, 2, 3}, []int{
		surahPatches[0].SurahID,
		surahPatches[1].SurahID,
		surahPatches[2].SurahID,
	})
	assert.Equal(t, []string{"ar", "en", "id"}, []string{
		surahPatches[0].Lang,
		surahPatches[1].Lang,
		surahPatches[2].Lang,
	})
	assert.Equal(t, []int{1, 3}, []int{metadata[0].SurahID, metadata[1].SurahID})

	err := sortAndValidateSurahImport([]QuranSurahEditorialPatch{
		{SurahID: 2, Lang: "id"},
		{SurahID: 2, Lang: "id"},
	}, nil)
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)

	err = sortAndValidateSurahImport(nil, []QuranSurahMetadataUpdate{
		{SurahID: 2},
		{SurahID: 2},
	})
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)
}

func TestQuranAyahEditorialImportValidationSortsAndRejectsDuplicates(t *testing.T) {
	t.Parallel()

	patches := []QuranAyahEditorialPatch{
		{SurahID: 3, AyahNumber: 1, Lang: "id"},
		{SurahID: 2, AyahNumber: 2, Lang: "id"},
		{SurahID: 2, AyahNumber: 1, Lang: "id"},
		{SurahID: 2, AyahNumber: 1, Lang: "ar"},
	}
	require.NoError(t, sortAndValidateAyahImport(patches))
	assert.Equal(t, []string{"2:1:ar", "2:1:id", "2:2:id", "3:1:id"}, []string{
		quranPatchScope(&patches[0]),
		quranPatchScope(&patches[1]),
		quranPatchScope(&patches[2]),
		quranPatchScope(&patches[3]),
	})

	err := sortAndValidateAyahImport([]QuranAyahEditorialPatch{
		{SurahID: 2, AyahNumber: 1, Lang: "id"},
		{SurahID: 2, AyahNumber: 1, Lang: "id"},
	})
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)
}

func quranPatchScope(patch *QuranAyahEditorialPatch) string {
	return fmt.Sprintf("%d:%d:%s", patch.SurahID, patch.AyahNumber, patch.Lang)
}

func TestQuranEditorialPreparationAndComparisonEdges(t *testing.T) {
	t.Parallel()

	reviewedAt := time.Date(2026, time.July, 11, 1, 2, 3, 987654321, time.FixedZone("WIB", 7*60*60))
	ayah, err := prepareAyahEditorialEdit(&entity.QuranAyahEditorialEdit{
		SurahID:       2,
		AyahNumber:    255,
		Lang:          "id",
		ReviewedAt:    &reviewedAt,
		LicenseStatus: entity.LicenseStatusPermitted,
	})
	require.NoError(t, err)
	assert.Equal(t, "2:255", ayah.AyahKey)
	assert.Empty(t, ayah.FAQ)
	assert.JSONEq(t, `{}`, string(ayah.Metadata))
	require.NotNil(t, ayah.ReviewedAt)
	assert.Zero(t, ayah.ReviewedAt.Nanosecond()%int(time.Microsecond))

	_, err = prepareAyahEditorialEdit(&entity.QuranAyahEditorialEdit{Metadata: entity.RawJSON(`{broken`)})
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)
	_, err = prepareSurahEditorialEdit(&entity.QuranSurahEditorialEdit{Metadata: entity.RawJSON(`{broken`)})
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)

	left := ayah
	left.Metadata = entity.RawJSON(`{"a":1,"b":2}`)
	right := left
	right.Status = entity.EditStatusPublished
	right.Metadata = entity.RawJSON(`{"b":2,"a":1}`)
	right.UpdatedAt = time.Now()
	right.PublishedAt = &right.UpdatedAt
	assert.True(t, equalAyahEditorialContent(&left, &right))
	right.FAQ = []entity.QuranAyahEditorialFAQ{{Question: "changed", AnswerHTML: "<p>yes</p>"}}
	assert.False(t, equalAyahEditorialContent(&left, &right))
	assert.False(t, equalJSON(entity.RawJSON(`{broken`), entity.RawJSON(`{}`)))
}

func TestQuranEditorialWorkspaceAndWriteErrorEdges(t *testing.T) {
	t.Parallel()

	current := time.Date(2026, time.July, 11, 1, 2, 3, 0, time.UTC)
	surahWorkspace := entity.QuranSurahEditorialWorkspace{
		Published: &entity.QuranSurahEditorialEdit{UpdatedAt: current},
	}
	ayahWorkspace := entity.QuranAyahEditorialWorkspace{
		Published: &entity.QuranAyahEditorialEdit{UpdatedAt: current},
	}

	assert.NoError(t, ensureSurahEditorialExpected(surahWorkspace, nil))
	assert.NoError(t, ensureSurahEditorialExpected(surahWorkspace, &current))
	assert.ErrorIs(t, ensureSurahEditorialExpected(entity.QuranSurahEditorialWorkspace{}, &current),
		entity.ErrPreconditionFailed)
	assert.NoError(t, ensureAyahEditorialExpected(ayahWorkspace, &current))
	assert.ErrorIs(t, ensureAyahEditorialExpected(entity.QuranAyahEditorialWorkspace{}, &current),
		entity.ErrPreconditionFailed)

	baseSurah, existed := baseSurahEditorialEdit(surahWorkspace)
	assert.True(t, existed)
	assert.Equal(t, current, baseSurah.UpdatedAt)

	_, existed = baseSurahEditorialEdit(entity.QuranSurahEditorialWorkspace{})
	assert.False(t, existed)
	baseAyah, existed := baseAyahEditorialEdit(ayahWorkspace)
	assert.True(t, existed)
	assert.Equal(t, current, baseAyah.UpdatedAt)

	_, existed = baseAyahEditorialEdit(entity.QuranAyahEditorialWorkspace{})
	assert.False(t, existed)

	status := ""
	applyImportLicense(&status, false, "", false)
	assert.Equal(t, entity.LicenseStatusNeedsReview, status)
	applyImportLicense(&status, true, entity.LicenseStatusPermitted, false)
	assert.Equal(t, entity.LicenseStatusNeedsReview, status)
	applyImportLicense(&status, true, entity.LicenseStatusPermitted, true)
	assert.Equal(t, entity.LicenseStatusPermitted, status)

	licenseErr := &pgconn.PgError{Code: "P0001", Message: "license_not_permitted"}
	assert.ErrorIs(t, mapQuranEditorialWriteError(licenseErr), entity.ErrLicenseNotPermitted)
	assert.ErrorIs(t, mapQuranEditorialWriteError(errDatabaseUnavailable), errDatabaseUnavailable)
}

func TestQuranEditorialRepositoryRejectsInvalidAyahKeysBeforeDatabase(t *testing.T) {
	t.Parallel()

	repository := &EditorialRepo{}
	_, err := repository.GetAyahEditorialWorkspace(t.Context(), "invalid", "id")
	assert.ErrorIs(t, err, entity.ErrInvalidAyahKey)
	_, err = repository.PublishAyahEditorialDraft(t.Context(), "", "invalid", "id", nil, "")
	assert.ErrorIs(t, err, entity.ErrInvalidAyahKey)
	_, err = repository.RestoreAyahEditorialRevision(t.Context(), "", "invalid", "id", "revision", nil)
	assert.ErrorIs(t, err, entity.ErrInvalidAyahKey)
}

func TestMergeSurahEditorialImportPatchPreservesAbsentFields(t *testing.T) {
	t.Parallel()

	reviewedAt := time.Date(2026, time.July, 11, 1, 2, 3, 0, time.UTC)
	base := &entity.QuranSurahEditorialEdit{
		SurahID:         2,
		Lang:            "id",
		Status:          entity.EditStatusDraft,
		MetaTitle:       new("lama"),
		MetaDescription: new("tetap"),
		AuthorName:      new("Tim Surau"),
		ReviewedAt:      &reviewedAt,
		LicenseStatus:   entity.LicenseStatusPermitted,
		Metadata:        entity.RawJSON(`{"source":"existing"}`),
	}

	merged := mergeSurahEditorialImportPatch(entity.QuranSurahEditorialWorkspace{Draft: base},
		&QuranSurahEditorialPatch{
			SurahID:         2,
			Lang:            "id",
			MetaTitle:       new("baru"),
			LicenseStatus:   entity.LicenseStatusNeedsReview,
			LicenseOverride: false,
		})

	assert.Equal(t, "baru", *merged.MetaTitle)
	assert.Equal(t, "tetap", *merged.MetaDescription)
	assert.Equal(t, "Tim Surau", *merged.AuthorName)
	assert.True(t, merged.ReviewedAt.Equal(reviewedAt))
	assert.Equal(t, entity.LicenseStatusPermitted, merged.LicenseStatus)
	assert.JSONEq(t, `{"source":"existing"}`, string(merged.Metadata))
}

func TestMergeAyahEditorialImportPatchPresenceBits(t *testing.T) {
	t.Parallel()

	base := &entity.QuranAyahEditorialEdit{
		SurahID:       2,
		AyahNumber:    255,
		AyahKey:       "2:255",
		Lang:          "id",
		Status:        entity.EditStatusPublished,
		Intisari:      new("tetap"),
		FAQ:           []entity.QuranAyahEditorialFAQ{{Question: "q", AnswerHTML: "<p>a</p>"}},
		LicenseStatus: entity.LicenseStatusPermitted,
		Metadata:      entity.RawJSON(`{"source":"existing"}`),
	}
	workspace := entity.QuranAyahEditorialWorkspace{Published: base}

	preserved := mergeAyahEditorialImportPatch(workspace, &QuranAyahEditorialPatch{
		SurahID:       2,
		AyahNumber:    255,
		Lang:          "id",
		LicenseStatus: entity.LicenseStatusNeedsReview,
	})
	require.Len(t, preserved.FAQ, 1)
	assert.Equal(t, "q", preserved.FAQ[0].Question)
	assert.Equal(t, "tetap", *preserved.Intisari)
	assert.Equal(t, entity.LicenseStatusPermitted, preserved.LicenseStatus)

	cleared := mergeAyahEditorialImportPatch(workspace, &QuranAyahEditorialPatch{
		SurahID:         2,
		AyahNumber:      255,
		Lang:            "id",
		FAQProvided:     true,
		FAQ:             []entity.QuranAyahEditorialFAQ{},
		LicenseStatus:   entity.LicenseStatusRestricted,
		LicenseOverride: true,
	})
	assert.Empty(t, cleared.FAQ)
	assert.Equal(t, entity.LicenseStatusRestricted, cleared.LicenseStatus)
	assert.JSONEq(t, `{"source":"existing"}`, string(cleared.Metadata))
}

func TestQuranEditorialNoOpComparisonIgnoresDerivedFields(t *testing.T) {
	t.Parallel()

	oldTime := time.Date(2025, time.January, 1, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)
	left := &entity.QuranSurahEditorialEdit{
		SurahID:       1,
		Lang:          "id",
		Status:        entity.EditStatusDraft,
		MetaTitle:     new("sama"),
		LicenseStatus: entity.LicenseStatusPermitted,
		Checksum:      nil,
		Metadata:      entity.RawJSON(`{"a":1,"b":2}`),
		CreatedAt:     oldTime,
		UpdatedAt:     oldTime,
	}
	right := *left
	right.Status = entity.EditStatusPublished
	right.Checksum = new("derived-checksum")
	right.Metadata = entity.RawJSON(`{"b":2,"a":1}`)
	right.CreatedAt = newTime
	right.UpdatedAt = newTime
	right.PublishedAt = &newTime
	right.UpdatedBy = new("00000000-0000-0000-0000-000000000001")

	assert.True(t, equalSurahEditorialContent(left, &right),
		"a derived checksum or workflow timestamp must not turn a payload no-op into a revision")
	right.MetaTitle = new("berubah")
	assert.False(t, equalSurahEditorialContent(left, &right))
}

// TestLiveQuranEditorialNoOpPreservesTokenAndRevision proves the complete
// adapter no-op contract against PostgreSQL, including its revision side
// effect. It is serial because it owns one throwaway ayah.
//
//nolint:paralleltest // gated live-DB test over a fixed throwaway ayah
func TestLiveQuranEditorialNoOpPreservesTokenAndRevision(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()

	const (
		surahID = 114
		ayahNo  = 990007
		ayahKey = "114:990007"
	)

	var insertedSurahID int

	err = pg.Pool.QueryRow(ctx, `
INSERT INTO quran_surahs (surah_id, name_latin, ayah_count)
VALUES ($1, 'Q-1 no-op fixture', 0)
ON CONFLICT (surah_id) DO NOTHING
RETURNING surah_id`, surahID).Scan(&insertedSurahID)
	if !errors.Is(err, pgx.ErrNoRows) {
		require.NoError(t, err)
	}

	_, err = pg.Pool.Exec(ctx, `DELETE FROM quran_ayahs WHERE ayah_key = $1`, ayahKey)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, metadata)
VALUES ($1, $2, $3, '{"q1_noop_fixture":true}'::jsonb)`, surahID, ayahNo, ayahKey)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, cleanupErr := pg.Pool.Exec(cleanupCtx,
			`DELETE FROM quran_ayahs WHERE ayah_key = $1`, ayahKey); cleanupErr != nil {
			t.Logf("cleanup Q-1 no-op ayah fixture: %v", cleanupErr)
		}

		if insertedSurahID != 0 {
			if _, cleanupErr := pg.Pool.Exec(cleanupCtx,
				`DELETE FROM quran_surahs WHERE surah_id = $1`, insertedSurahID); cleanupErr != nil {
				t.Logf("cleanup Q-1 no-op surah fixture: %v", cleanupErr)
			}
		}
	})

	repository := NewEditorialRepo(pg)
	patch := QuranAyahEditorialPatch{
		SurahID:       surahID,
		AyahNumber:    ayahNo,
		Lang:          "en",
		MetaTitle:     new("Q-1 no-op fixture"),
		LicenseStatus: entity.LicenseStatusNeedsReview,
	}

	changed, published, err := repository.ImportAyahEditorialBatch(ctx, []QuranAyahEditorialPatch{patch}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)
	assert.Zero(t, published)

	first, err := repository.GetAyahEditorialWorkspace(ctx, ayahKey, "en")
	require.NoError(t, err)
	require.NotNil(t, first.Draft)

	var firstRevisionCount int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM quran_editorial_revisions
WHERE resource_type = 'ayah' AND surah_id = $1 AND ayah_number = $2 AND lang = 'en'`,
		surahID, ayahNo).Scan(&firstRevisionCount))
	require.Equal(t, 1, firstRevisionCount)

	changed, published, err = repository.ImportAyahEditorialBatch(ctx, []QuranAyahEditorialPatch{patch}, false)
	require.NoError(t, err)
	assert.Zero(t, changed)
	assert.Zero(t, published)

	second, err := repository.GetAyahEditorialWorkspace(ctx, ayahKey, "en")
	require.NoError(t, err)
	require.NotNil(t, second.Draft)
	assert.True(t, first.Draft.UpdatedAt.Equal(second.Draft.UpdatedAt))

	var secondRevisionCount int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM quran_editorial_revisions
WHERE resource_type = 'ayah' AND surah_id = $1 AND ayah_number = $2 AND lang = 'en'`,
		surahID, ayahNo).Scan(&secondRevisionCount))
	assert.Equal(t, firstRevisionCount, secondRevisionCount)
}
