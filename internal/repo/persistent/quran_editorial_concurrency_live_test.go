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
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuranEditorialConcurrencyAndRestore is Q-1's SQL-level concurrency
// proof. It covers both the first draft over a grandfather-style published-only
// row and a later edit over an existing draft: exactly one of two writers with
// the same ETag succeeds, the loser gets ErrPreconditionFailed, and only the
// winner appends a revision. It then restores and republishes that revision.
//
//nolint:paralleltest // one serial stateful acceptance story over fixed live fixtures
func TestLiveQuranEditorialConcurrencyAndRestore(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()

	var (
		surahID        int
		lang           string
		insertedParent bool
	)

	// Claim only a scope that is demonstrably empty. Never pre-delete a fixed
	// surah/lang pair: this suite is allowed to run against a populated corpus.
	err = pg.Pool.QueryRow(ctx, `
SELECT surah.surah_id, candidate.lang
FROM quran_surahs surah
CROSS JOIN (VALUES ('en'), ('id'), ('ar')) AS candidate(lang)
WHERE EXISTS (
    SELECT 1 FROM quran_surah_slug_registry registry
    WHERE registry.surah_id = surah.surah_id AND registry.slug = surah.slug
)
  AND NOT EXISTS (
    SELECT 1 FROM quran_surah_editorial editorial
    WHERE editorial.surah_id = surah.surah_id AND editorial.lang = candidate.lang
)
  AND NOT EXISTS (
    SELECT 1 FROM quran_editorial_revisions revision
    WHERE revision.resource_type = 'surah'
      AND revision.surah_id = surah.surah_id
      AND revision.ayah_number IS NULL
      AND revision.lang = candidate.lang
)
ORDER BY surah.surah_id DESC, candidate.lang
LIMIT 1`).Scan(&surahID, &lang)
	if errors.Is(err, pgx.ErrNoRows) {
		// A newly created parent is even more isolated when the corpus has a gap.
		err = pg.Pool.QueryRow(ctx, `
SELECT candidate.surah_id
FROM generate_series(1, 114) AS candidate(surah_id)
WHERE NOT EXISTS (
    SELECT 1 FROM quran_surahs surah WHERE surah.surah_id = candidate.surah_id
)
ORDER BY candidate.surah_id DESC
LIMIT 1`).Scan(&surahID)
		if errors.Is(err, pgx.ErrNoRows) {
			t.Skip("no empty surah editorial scope is available for the live fixture")
		}

		require.NoError(t, err)

		lang = "en"
		insertedParent, err = insertQuranSurahLiveFixture(
			ctx, pg, surahID, fmt.Sprintf("q1-concurrency-fixture-%d", surahID),
			"Q-1 concurrency fixture", 0, "q1_concurrency_fixture",
		)
		require.NoError(t, err)
	} else {
		require.NoError(t, err)
	}

	repository := NewEditorialRepo(pg)
	cleanup := func() {
		cleanupCtx := context.Background()

		tx, cleanupErr := pg.Pool.Begin(cleanupCtx)
		if cleanupErr == nil {
			cleanupErr = markQuranEditorialWriter(cleanupCtx, tx)
		}

		if cleanupErr == nil {
			_, cleanupErr = tx.Exec(cleanupCtx, `
DELETE FROM quran_editorial_revisions
WHERE resource_type = 'surah' AND surah_id = $1 AND ayah_number IS NULL AND lang = $2`, surahID, lang)
		}

		if cleanupErr == nil {
			_, cleanupErr = tx.Exec(cleanupCtx, `
DELETE FROM quran_surah_editorial WHERE surah_id = $1 AND lang = $2`, surahID, lang)
		}

		if cleanupErr == nil {
			cleanupErr = tx.Commit(cleanupCtx)
		} else if tx != nil {
			_ = tx.Rollback(cleanupCtx)
		}

		if cleanupErr != nil {
			t.Logf("cleanup Q-1 concurrency workflow: %v", cleanupErr)
		}
	}

	t.Cleanup(func() {
		cleanup()

		if insertedParent {
			if cleanupErr := cleanupInsertedQuranSurah(
				context.Background(), pg, surahID, "q1_concurrency_fixture",
			); cleanupErr != nil {
				t.Logf("cleanup Q-1 concurrency surah: %v", cleanupErr)
			}
		}
	})

	initialTitle := "published baseline"
	initial, err := repository.SaveSurahEditorialDraft(ctx, "", entity.QuranSurahEditorialEdit{
		SurahID:       surahID,
		Lang:          lang,
		MetaTitle:     &initialTitle,
		LicenseStatus: entity.LicenseStatusPermitted,
	}, nil, entity.EditOriginREST)
	require.NoError(t, err)
	require.NotNil(t, initial.Draft)
	published, err := repository.PublishSurahEditorialDraft(
		ctx, "", surahID, lang, &initial.Draft.UpdatedAt, entity.EditOriginREST,
	)
	require.NoError(t, err)
	require.NotNil(t, published.Published)

	// Migration-grandfathered resources have only a published row. This direct
	// delete is test fixture setup under the same transaction-local marker.
	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, markQuranEditorialWriter(ctx, tx))
	_, err = tx.Exec(ctx, `
DELETE FROM quran_surah_editorial
WHERE surah_id = $1 AND lang = $2 AND status = 'draft'`, surahID, lang)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	publishedOnly, err := repository.GetSurahEditorialWorkspace(ctx, surahID, lang)
	require.NoError(t, err)
	require.Nil(t, publishedOnly.Draft)
	require.NotNil(t, publishedOnly.Published)

	firstWinner := raceSurahEditorialSaves(
		t, repository, surahID, lang, publishedOnly.Published.UpdatedAt, "first-a", "first-b",
	)
	require.NotNil(t, firstWinner.Draft)
	firstWinnerTitle := *firstWinner.Draft.MetaTitle

	revisions, total, err := repository.ListQuranEditorialRevisions(ctx, quranRevisionFilter(surahID, lang))
	require.NoError(t, err)
	require.Equal(t, 3, total, "baseline save+publish and exactly one first-draft winner")
	require.Len(t, revisions, 3)
	firstRaceRevisionID := revisions[0].ID

	secondWinner := raceSurahEditorialSaves(
		t, repository, surahID, lang, firstWinner.Draft.UpdatedAt, "second-a", "second-b",
	)
	require.NotNil(t, secondWinner.Draft)

	_, total, err = repository.ListQuranEditorialRevisions(ctx, quranRevisionFilter(surahID, lang))
	require.NoError(t, err)
	require.Equal(t, 4, total, "existing-draft race appends only its winner")

	// Saving the exact winner is a successful no-op with no token/revision churn.
	noOp, err := repository.SaveSurahEditorialDraft(
		ctx, "", *secondWinner.Draft, &secondWinner.Draft.UpdatedAt, entity.EditOriginREST,
	)
	require.NoError(t, err)
	require.NotNil(t, noOp.Draft)
	assert.True(t, noOp.Draft.UpdatedAt.Equal(secondWinner.Draft.UpdatedAt))

	_, total, err = repository.ListQuranEditorialRevisions(ctx, quranRevisionFilter(surahID, lang))
	require.NoError(t, err)
	require.Equal(t, 4, total)

	restored, err := repository.RestoreSurahEditorialRevision(
		ctx, "", surahID, lang, firstRaceRevisionID, &secondWinner.Draft.UpdatedAt,
	)
	require.NoError(t, err)
	require.NotNil(t, restored.Draft)
	require.NotNil(t, restored.Draft.MetaTitle)
	assert.Equal(t, firstWinnerTitle, *restored.Draft.MetaTitle)

	republished, err := repository.PublishSurahEditorialDraft(
		ctx, "", surahID, lang, &restored.Draft.UpdatedAt, entity.EditOriginREST,
	)
	require.NoError(t, err)
	require.NotNil(t, republished.Published)
	assert.Equal(t, firstWinnerTitle, *republished.Published.MetaTitle)

	revisions, total, err = repository.ListQuranEditorialRevisions(ctx, quranRevisionFilter(surahID, lang))
	require.NoError(t, err)
	require.Equal(t, 6, total)
	require.Len(t, revisions, 6)
	assert.Equal(t, entity.EditStatusPublished, revisions[0].Status)
	assert.Equal(t, entity.EditOriginREST, revisions[0].Origin)
	assert.Equal(t, entity.EditOriginRestore, revisions[1].Origin)

	var publicTitle string
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT meta_title
FROM quran_surah_editorial_public
WHERE surah_id = $1 AND lang = $2`, surahID, lang).Scan(&publicTitle))
	assert.Equal(t, firstWinnerTitle, publicTitle)
}

func raceSurahEditorialSaves(
	t *testing.T,
	repository *EditorialRepo,
	surahID int,
	lang string,
	expected time.Time,
	leftTitle,
	rightTitle string,
) entity.QuranSurahEditorialWorkspace {
	t.Helper()

	type outcome struct {
		workspace entity.QuranSurahEditorialWorkspace
		err       error
	}

	start := make(chan struct{})
	outcomes := make(chan outcome, 2)

	for _, title := range []string{leftTitle, rightTitle} {
		go func() {
			<-start

			workspace, saveErr := repository.SaveSurahEditorialDraft(
				context.Background(), "", entity.QuranSurahEditorialEdit{
					SurahID:       surahID,
					Lang:          lang,
					MetaTitle:     &title,
					LicenseStatus: entity.LicenseStatusPermitted,
				}, &expected, entity.EditOriginREST,
			)
			outcomes <- outcome{workspace: workspace, err: saveErr}
		}()
	}

	close(start)

	var winner entity.QuranSurahEditorialWorkspace

	successes, stale := 0, 0

	for range 2 {
		result := <-outcomes
		switch {
		case result.err == nil:
			successes++
			winner = result.workspace
		case errors.Is(result.err, entity.ErrPreconditionFailed):
			stale++
		default:
			t.Fatalf("unexpected concurrent save error: %v", result.err)
		}
	}

	require.Equal(t, 1, successes)
	require.Equal(t, 1, stale)

	return winner
}

func quranRevisionFilter(surahID int, lang string) repo.QuranEditorialRevisionFilter {
	return repo.QuranEditorialRevisionFilter{
		AssetType: entity.QuranEditorialAssetSurah,
		SurahID:   surahID,
		Lang:      lang,
		Limit:     50,
	}
}
