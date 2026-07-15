package persistent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuranAyahEditorialRepositoryWorkflow exercises the repository itself,
// rather than stopping at controller fakes. It covers draft/publish no-ops,
// stale CAS tokens, revision pagination, restore, and public isolation.
//
//nolint:paralleltest // serial stateful acceptance story over one test-owned ayah
func TestLiveQuranAyahEditorialRepositoryWorkflow(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()

	const (
		ayahNo = 990008
		lang   = "id"
	)

	surahID, ayahKey := setupQuranEditorialAyahFixture(t, pg, 114, ayahNo)

	repository := NewEditorialRepo(pg)

	_, err = repository.GetAyahEditorialWorkspace(ctx, ayahKey, lang)
	assert.ErrorIs(t, err, entity.ErrDraftNotFound)
	_, err = repository.PublishAyahEditorialDraft(ctx, "", ayahKey, "ar", nil, entity.EditOriginREST)
	assert.ErrorIs(t, err, entity.ErrDraftNotFound)
	_, err = repository.SaveAyahEditorialDraft(ctx, "", entity.QuranAyahEditorialEdit{
		SurahID:       surahID,
		AyahNumber:    ayahNo + 100,
		Lang:          lang,
		LicenseStatus: entity.LicenseStatusPermitted,
	}, nil, entity.EditOriginREST)
	assert.ErrorIs(t, err, entity.ErrQuranAyahNotFound)

	reviewedAt := time.Date(2026, time.July, 11, 9, 8, 7, 654321987, time.FixedZone("WIB", 7*60*60))
	restrictedTitle := "needs review draft"
	first, err := repository.SaveAyahEditorialDraft(ctx, "", entity.QuranAyahEditorialEdit{
		SurahID:         surahID,
		AyahNumber:      ayahNo,
		Lang:            lang,
		MetaTitle:       &restrictedTitle,
		MetaDescription: new("description"),
		Intisari:        new("<p>summary</p>"),
		Keutamaan:       new("<p>virtue</p>"),
		FAQ: []entity.QuranAyahEditorialFAQ{
			{Question: "Question?", AnswerHTML: "<p>Answer.</p>"},
		},
		TafsirRange:   new("1-2"),
		AuthorName:    new("Editorial team"),
		ReviewedBy:    new("Reviewer"),
		ReviewedAt:    &reviewedAt,
		LicenseStatus: entity.LicenseStatusNeedsReview,
		Metadata:      entity.RawJSON(`{"fixture":"q1_repository"}`),
	}, nil, "")
	require.NoError(t, err)
	require.NotNil(t, first.Draft)
	assert.Equal(t, entity.EditStatusDraft, first.Draft.Status)
	assert.Equal(t, ayahKey, first.Draft.AyahKey)
	require.NotNil(t, first.Draft.ReviewedAt)
	assert.Zero(t, first.Draft.ReviewedAt.Nanosecond()%int(time.Microsecond))

	stale := first.Draft.UpdatedAt.Add(-time.Second)
	_, err = repository.PublishAyahEditorialDraft(ctx, "", ayahKey, lang, &stale, entity.EditOriginREST)
	assert.ErrorIs(t, err, entity.ErrPreconditionFailed)
	_, err = repository.PublishAyahEditorialDraft(
		ctx, "", ayahKey, lang, &first.Draft.UpdatedAt, entity.EditOriginREST,
	)
	assert.ErrorIs(t, err, entity.ErrLicenseNotPermitted)

	invalid := *first.Draft
	invalid.Metadata = entity.RawJSON(`{broken`)
	_, err = repository.SaveAyahEditorialDraft(
		ctx, "", invalid, &first.Draft.UpdatedAt, entity.EditOriginREST,
	)
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)

	permittedTitle := "permitted draft"
	permitted := *first.Draft
	permitted.MetaTitle = &permittedTitle
	permitted.LicenseStatus = entity.LicenseStatusPermitted
	second, err := repository.SaveAyahEditorialDraft(
		ctx, "", permitted, &first.Draft.UpdatedAt, "",
	)
	require.NoError(t, err)
	require.NotNil(t, second.Draft)
	assert.True(t, second.Draft.UpdatedAt.After(first.Draft.UpdatedAt))

	_, err = repository.SaveAyahEditorialDraft(
		ctx, "", *second.Draft, &first.Draft.UpdatedAt, entity.EditOriginREST,
	)
	assert.ErrorIs(t, err, entity.ErrPreconditionFailed)

	noOp, err := repository.SaveAyahEditorialDraft(
		ctx, "", *second.Draft, &second.Draft.UpdatedAt, entity.EditOriginREST,
	)
	require.NoError(t, err)
	require.NotNil(t, noOp.Draft)
	assert.True(t, noOp.Draft.UpdatedAt.Equal(second.Draft.UpdatedAt))

	_, err = repository.PublishAyahEditorialDraft(
		ctx, "", ayahKey, lang, &first.Draft.UpdatedAt, entity.EditOriginREST,
	)
	assert.ErrorIs(t, err, entity.ErrPreconditionFailed)
	published, err := repository.PublishAyahEditorialDraft(
		ctx, "", ayahKey, lang, &second.Draft.UpdatedAt, entity.EditOriginREST,
	)
	require.NoError(t, err)
	require.NotNil(t, published.Draft)
	require.NotNil(t, published.Published)
	assert.Equal(t, permittedTitle, *published.Published.MetaTitle)
	assert.True(t, published.Published.UpdatedAt.After(second.Draft.UpdatedAt))

	publishNoOp, err := repository.PublishAyahEditorialDraft(
		ctx, "", ayahKey, lang, &published.Draft.UpdatedAt, entity.EditOriginREST,
	)
	require.NoError(t, err)
	require.NotNil(t, publishNoOp.Published)
	assert.True(t, publishNoOp.Published.UpdatedAt.Equal(published.Published.UpdatedAt))

	ayahNumber := ayahNo
	filter := repo.QuranEditorialRevisionFilter{
		AssetType:  entity.QuranEditorialAssetAyah,
		SurahID:    surahID,
		AyahNumber: &ayahNumber,
		Lang:       lang,
		Limit:      1,
		Offset:     1,
	}
	page, total, err := repository.ListQuranEditorialRevisions(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, page, 1)
	require.NotNil(t, page[0].AyahKey)
	assert.Equal(t, ayahKey, *page[0].AyahKey)
	assert.Equal(t, entity.EditOriginREST, page[0].Origin, "empty origin defaults to REST")

	filter.Limit = 50
	filter.Offset = 0
	history, total, err := repository.ListQuranEditorialRevisions(ctx, filter)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, history, 3)
	assert.Equal(t, entity.EditStatusPublished, history[0].Status)

	_, err = repository.RestoreAyahEditorialRevision(
		ctx, "", ayahKey, lang, uuid.NewString(), &published.Draft.UpdatedAt,
	)
	assert.ErrorIs(t, err, entity.ErrDraftNotFound)
	_, err = repository.RestoreAyahEditorialRevision(
		ctx, "", ayahKey, lang, history[2].ID, &second.Draft.UpdatedAt,
	)
	assert.ErrorIs(t, err, entity.ErrPreconditionFailed)

	restored, err := repository.RestoreAyahEditorialRevision(
		ctx, "", ayahKey, lang, history[2].ID, &published.Draft.UpdatedAt,
	)
	require.NoError(t, err)
	require.NotNil(t, restored.Draft)
	require.NotNil(t, restored.Published)
	assert.Equal(t, restrictedTitle, *restored.Draft.MetaTitle)
	assert.Equal(t, permittedTitle, *restored.Published.MetaTitle,
		"restore must not mutate the public snapshot")

	restoredNoOp, err := repository.RestoreAyahEditorialRevision(
		ctx, "", ayahKey, lang, history[2].ID, &restored.Draft.UpdatedAt,
	)
	require.NoError(t, err)
	require.NotNil(t, restoredNoOp.Draft)
	assert.True(t, restoredNoOp.Draft.UpdatedAt.Equal(restored.Draft.UpdatedAt))

	_, total, err = repository.ListQuranEditorialRevisions(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, 4, total, "an exact restore no-op must not append another revision")

	_, err = repository.PublishAyahEditorialDraft(
		ctx, "", ayahKey, lang, &restored.Draft.UpdatedAt, entity.EditOriginREST,
	)
	assert.ErrorIs(t, err, entity.ErrLicenseNotPermitted)

	var publicTitle string
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT meta_title FROM quran_ayah_editorial_public
WHERE surah_id = $1 AND ayah_number = $2 AND lang = $3`, surahID, ayahNo, lang).Scan(&publicTitle))
	assert.Equal(t, permittedTitle, publicTitle)

	corruptRevisionID := insertCorruptQuranEditorialRevision(t, pg, surahID, ayahNo, lang)
	_, err = repository.RestoreAyahEditorialRevision(
		ctx, "", ayahKey, lang, corruptRevisionID, &restored.Draft.UpdatedAt,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode revision snapshot")
}

// TestLiveQuranEditorialImporterRepositoryMatrix proves importer atomicity at
// the repository boundary, including duplicate input, missing parents, license
// rollback, explicit publish, metadata gating, and repeat-run no-ops.
//
//nolint:paralleltest // serial stateful importer matrix over isolated scopes
func TestLiveQuranEditorialImporterRepositoryMatrix(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	repository := NewEditorialRepo(pg)

	duplicateSurah := []QuranSurahEditorialPatch{
		{SurahID: 1, Lang: "id"},
		{SurahID: 1, Lang: "id"},
	}
	_, _, err = repository.ImportSurahEditorialBatch(ctx, duplicateSurah, nil, false)
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)
	_, _, err = repository.ImportSurahEditorialBatch(ctx, nil, []QuranSurahMetadataUpdate{
		{SurahID: 1},
		{SurahID: 1},
	}, true)
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)
	_, _, err = repository.ImportAyahEditorialBatch(ctx, []QuranAyahEditorialPatch{
		{SurahID: 1, AyahNumber: 1, Lang: "id"},
		{SurahID: 1, AyahNumber: 1, Lang: "id"},
	}, false)
	assert.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)

	_, _, err = repository.ImportSurahEditorialBatch(ctx, []QuranSurahEditorialPatch{
		{SurahID: 999, Lang: "id"},
	}, nil, false)
	assert.ErrorIs(t, err, entity.ErrQuranSurahNotFound)
	_, _, err = repository.ImportAyahEditorialBatch(ctx, []QuranAyahEditorialPatch{
		{SurahID: 114, AyahNumber: 990099, Lang: "id"},
	}, false)
	assert.ErrorIs(t, err, entity.ErrQuranAyahNotFound)
	_, _, err = repository.ImportSurahEditorialBatch(ctx, nil, []QuranSurahMetadataUpdate{
		{SurahID: 999, Slug: new("must-not-write")},
	}, true)
	assert.ErrorIs(t, err, entity.ErrQuranSurahNotFound)

	surahID, lang, original := claimQuranSurahEditorialImportScope(t, pg)
	cleanupQuranSurahEditorialImportScope(t, pg, surahID, lang, original)

	draftTitle := "imported draft"
	newSlug := fmt.Sprintf("q1-repository-live-%d", surahID)
	newRuku := 999
	patch := QuranSurahEditorialPatch{
		SurahID:       surahID,
		Lang:          lang,
		MetaTitle:     &draftTitle,
		LicenseStatus: entity.LicenseStatusNeedsReview,
	}
	metadata := []QuranSurahMetadataUpdate{{
		SurahID:   surahID,
		Slug:      &newSlug,
		RukuCount: &newRuku,
	}}
	changed, published, err := repository.ImportSurahEditorialBatch(ctx, []QuranSurahEditorialPatch{patch}, metadata, false)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)
	assert.Zero(t, published)
	assertQuranSurahMetadataUnchanged(t, pg, surahID, original)

	beforeRejectedPublish, err := repository.GetSurahEditorialWorkspace(ctx, surahID, lang)
	require.NoError(t, err)
	require.NotNil(t, beforeRejectedPublish.Draft)

	changed, published, err = repository.ImportSurahEditorialBatch(
		ctx, []QuranSurahEditorialPatch{patch}, metadata, true,
	)
	assert.ErrorIs(t, err, entity.ErrLicenseNotPermitted)
	assert.Zero(t, changed)
	assert.Zero(t, published)

	afterRejectedPublish, err := repository.GetSurahEditorialWorkspace(ctx, surahID, lang)
	require.NoError(t, err)
	require.NotNil(t, afterRejectedPublish.Draft)
	assert.True(t, afterRejectedPublish.Draft.UpdatedAt.Equal(beforeRejectedPublish.Draft.UpdatedAt))
	assertQuranSurahMetadataUnchanged(t, pg, surahID, original)

	patch.LicenseStatus = entity.LicenseStatusPermitted
	patch.LicenseOverride = true
	changed, published, err = repository.ImportSurahEditorialBatch(
		ctx, []QuranSurahEditorialPatch{patch}, metadata, true,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)
	assert.Equal(t, 1, published)

	workspace, err := repository.GetSurahEditorialWorkspace(ctx, surahID, lang)
	require.NoError(t, err)
	require.NotNil(t, workspace.Published)
	assert.Equal(t, entity.LicenseStatusPermitted, workspace.Published.LicenseStatus)
	assertQuranSurahMetadata(t, pg, surahID, &newSlug, &newRuku)

	changed, published, err = repository.ImportSurahEditorialBatch(
		ctx, []QuranSurahEditorialPatch{patch}, metadata, true,
	)
	require.NoError(t, err)
	assert.Zero(t, changed)
	assert.Zero(t, published)

	const ayahNo = 990009

	ayahSurahID, ayahKey := setupQuranEditorialAyahFixture(t, pg, 114, ayahNo)
	ayahPatch := QuranAyahEditorialPatch{
		SurahID:       ayahSurahID,
		AyahNumber:    ayahNo,
		Lang:          "en",
		IntisariHTML:  new("<p>import draft</p>"),
		FAQProvided:   true,
		FAQ:           []entity.QuranAyahEditorialFAQ{},
		LicenseStatus: entity.LicenseStatusNeedsReview,
	}
	changed, published, err = repository.ImportAyahEditorialBatch(ctx, []QuranAyahEditorialPatch{ayahPatch}, false)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)
	assert.Zero(t, published)

	ayahBefore, err := repository.GetAyahEditorialWorkspace(ctx, ayahKey, "en")
	require.NoError(t, err)
	require.NotNil(t, ayahBefore.Draft)

	ayahPatch.IntisariHTML = new("<p>must roll back</p>")
	changed, published, err = repository.ImportAyahEditorialBatch(ctx, []QuranAyahEditorialPatch{ayahPatch}, true)
	assert.ErrorIs(t, err, entity.ErrLicenseNotPermitted)
	assert.Zero(t, changed)
	assert.Zero(t, published)

	ayahAfterRollback, err := repository.GetAyahEditorialWorkspace(ctx, ayahKey, "en")
	require.NoError(t, err)
	require.NotNil(t, ayahAfterRollback.Draft)
	assert.Equal(t, *ayahBefore.Draft.Intisari, *ayahAfterRollback.Draft.Intisari)
	assert.True(t, ayahBefore.Draft.UpdatedAt.Equal(ayahAfterRollback.Draft.UpdatedAt))

	ayahPatch.LicenseStatus = entity.LicenseStatusPermitted
	ayahPatch.LicenseOverride = true
	changed, published, err = repository.ImportAyahEditorialBatch(ctx, []QuranAyahEditorialPatch{ayahPatch}, true)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)
	assert.Equal(t, 1, published)
	changed, published, err = repository.ImportAyahEditorialBatch(ctx, []QuranAyahEditorialPatch{ayahPatch}, true)
	require.NoError(t, err)
	assert.Zero(t, changed)
	assert.Zero(t, published)
}

// TestLiveQuranEditorialRepositoryClosedPoolErrors covers the adapter's
// database-unavailable contract for every public Q-1 repository method.
//
//nolint:paralleltest // serial because it deliberately closes its private pool
func TestLiveQuranEditorialRepositoryClosedPoolErrors(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	pg.Close()

	ctx := context.Background()
	repository := NewEditorialRepo(pg)
	assertRepositoryError := func(err error) { t.Helper(); require.Error(t, err) }

	_, err = repository.GetSurahEditorialWorkspace(ctx, 1, "id")
	assertRepositoryError(err)
	_, err = repository.SaveSurahEditorialDraft(ctx, "", entity.QuranSurahEditorialEdit{SurahID: 1}, nil, "")
	assertRepositoryError(err)
	_, err = repository.PublishSurahEditorialDraft(ctx, "", 1, "id", nil, "")
	assertRepositoryError(err)
	_, err = repository.RestoreSurahEditorialRevision(ctx, "", 1, "id", uuid.NewString(), nil)
	assertRepositoryError(err)
	_, err = repository.GetAyahEditorialWorkspace(ctx, "1:1", "id")
	assertRepositoryError(err)
	_, err = repository.SaveAyahEditorialDraft(ctx, "", entity.QuranAyahEditorialEdit{
		SurahID: 1, AyahNumber: 1,
	}, nil, "")
	assertRepositoryError(err)
	_, err = repository.PublishAyahEditorialDraft(ctx, "", "1:1", "id", nil, "")
	assertRepositoryError(err)
	_, err = repository.RestoreAyahEditorialRevision(ctx, "", "1:1", "id", uuid.NewString(), nil)
	assertRepositoryError(err)
	_, _, err = repository.ListQuranEditorialRevisions(ctx, repo.QuranEditorialRevisionFilter{
		AssetType: entity.QuranEditorialAssetSurah, SurahID: 1, Lang: "id", Limit: 1,
	})
	assertRepositoryError(err)
	_, _, err = repository.ImportSurahEditorialBatch(ctx, nil, nil, false)
	assertRepositoryError(err)
	_, _, err = repository.ImportAyahEditorialBatch(ctx, nil, false)
	assertRepositoryError(err)
}

type quranSurahMetadataSnapshot struct {
	slug               *string
	chronologicalOrder *int
	rukuCount          *int
	updatedAt          time.Time
	insertedParent     bool
}

func setupQuranEditorialAyahFixture(
	t *testing.T,
	pg *postgres.Postgres,
	preferredSurahID,
	ayahNo int,
) (int, string) {
	t.Helper()

	ctx := context.Background()
	surahID, inserted, err := claimQuranSurahLiveFixture(
		ctx, pg, preferredSurahID, "q1-repository-fixture",
		"Q-1 repository fixture", 0, "q1_repository_parent_fixture",
	)
	require.NoError(t, err)

	ayahKey := fmt.Sprintf("%d:%d", surahID, ayahNo)

	_, err = pg.Pool.Exec(ctx, `DELETE FROM quran_ayahs WHERE ayah_key = $1`, ayahKey)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, metadata)
VALUES ($1, $2, $3, '{"q1_repository_fixture":true}'::jsonb)`, surahID, ayahNo, ayahKey)
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		if _, cleanupErr := pg.Pool.Exec(cleanupCtx,
			`DELETE FROM quran_ayahs WHERE ayah_key = $1`, ayahKey); cleanupErr != nil {
			t.Logf("cleanup Q-1 repository ayah fixture: %v", cleanupErr)
		}

		if inserted {
			if cleanupErr := cleanupInsertedQuranSurah(
				cleanupCtx, pg, surahID, "q1_repository_parent_fixture",
			); cleanupErr != nil {
				t.Logf("cleanup Q-1 repository surah fixture: %v", cleanupErr)
			}
		}
	})

	return surahID, ayahKey
}

func insertCorruptQuranEditorialRevision(
	t *testing.T,
	pg *postgres.Postgres,
	surahID,
	ayahNo int,
	lang string,
) string {
	t.Helper()

	ctx := context.Background()

	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)

	defer rollbackTx(ctx, tx)

	require.NoError(t, markQuranEditorialWriter(ctx, tx))

	revisionID := uuid.NewString()
	_, err = tx.Exec(ctx, `
INSERT INTO quran_editorial_revisions (
    id, resource_type, surah_id, ayah_number, lang, status, version, origin, snapshot
) VALUES ($1, 'ayah', $2, $3, $4, 'draft', 100, 'rest', '{"surah_id":"not-an-integer"}'::jsonb)`,
		revisionID, surahID, ayahNo, lang)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	return revisionID
}

func claimQuranSurahEditorialImportScope(
	t *testing.T,
	pg *postgres.Postgres,
) (surahID int, lang string, result quranSurahMetadataSnapshot) {
	t.Helper()

	ctx := context.Background()

	err := pg.Pool.QueryRow(ctx, `
SELECT candidate.surah_id
FROM generate_series(1, 114) AS candidate(surah_id)
WHERE NOT EXISTS (
    SELECT 1 FROM quran_surahs surah WHERE surah.surah_id = candidate.surah_id
)
ORDER BY candidate.surah_id DESC
LIMIT 1`).Scan(&surahID)
	if errors.Is(err, pgx.ErrNoRows) {
		t.Skip("no empty surah editorial scope is available for importer repository test")
	}

	require.NoError(t, err)

	lang = "en"
	err = pg.Pool.QueryRow(ctx, `
INSERT INTO quran_surahs (surah_id, name_latin, ayah_count, metadata)
VALUES ($1, 'Q-1 importer repository fixture', 0, '{"q1_import_repository_fixture":true}'::jsonb)
RETURNING slug, chronological_order, ruku_count, updated_at`, surahID).Scan(
		&result.slug,
		&result.chronologicalOrder,
		&result.rukuCount,
		&result.updatedAt,
	)
	require.NoError(t, err)

	result.insertedParent = true

	return surahID, lang, result
}

func cleanupQuranSurahEditorialImportScope(
	t *testing.T,
	pg *postgres.Postgres,
	surahID int,
	lang string,
	original quranSurahMetadataSnapshot,
) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()

		tx, err := pg.Pool.Begin(ctx)
		if err == nil {
			err = markQuranEditorialWriter(ctx, tx)
		}

		if err == nil {
			_, err = tx.Exec(ctx, `
DELETE FROM quran_editorial_revisions
WHERE resource_type = 'surah' AND surah_id = $1 AND ayah_number IS NULL AND lang = $2`,
				surahID, lang)
		}

		if err == nil {
			_, err = tx.Exec(ctx, `
DELETE FROM quran_surah_editorial WHERE surah_id = $1 AND lang = $2`, surahID, lang)
		}

		if err == nil {
			err = tx.Commit(ctx)
		} else if tx != nil {
			_ = tx.Rollback(ctx)
		}

		if err != nil {
			t.Logf("cleanup Q-1 surah importer repository fixture: %v", err)

			return
		}

		if original.insertedParent {
			if deleteErr := cleanupInsertedQuranSurah(
				ctx, pg, surahID, "q1_import_repository_fixture",
			); deleteErr != nil {
				t.Logf("cleanup Q-1 importer repository parent: %v", deleteErr)
			}
		}
	})
}

func assertQuranSurahMetadataUnchanged(
	t *testing.T,
	pg *postgres.Postgres,
	surahID int,
	expected quranSurahMetadataSnapshot,
) {
	t.Helper()
	assertQuranSurahMetadata(t, pg, surahID, expected.slug, expected.rukuCount)
}

func assertQuranSurahMetadata(
	t *testing.T,
	pg *postgres.Postgres,
	surahID int,
	expectedSlug *string,
	expectedRuku *int,
) {
	t.Helper()

	var actualSlug *string

	var actualRuku *int
	require.NoError(t, pg.Pool.QueryRow(context.Background(), `
SELECT slug, ruku_count FROM quran_surahs WHERE surah_id = $1`, surahID).Scan(&actualSlug, &actualRuku))
	assert.Equal(t, expectedSlug, actualSlug)
	assert.Equal(t, expectedRuku, actualRuku)
}
