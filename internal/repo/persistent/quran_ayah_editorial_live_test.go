package persistent

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	repocontract "github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveAyahEditorialReadPath exercises the real repo read path against a
// live Postgres using a SELF-SEEDED fixture (F1-E): throwaway ayahs on surah
// 113 far above its real ayah_count, so nothing collides with a real corpus
// or with the other live tests (the importer test owns 113:280; the coverage
// test owns 113:901-903 but pre-cleans and runs serially). Gated on
// SURAU_LIVE_PG so it never runs in normal `go test ./...`.
//
//	SURAU_LIVE_PG=postgres://... go test ./internal/repo/persistent/ -run TestLiveAyahEditorialReadPath -v
//
//nolint:paralleltest // serial live-DB read-path checks over shared seeded rows (gated on SURAU_LIVE_PG)
func TestLiveAyahEditorialReadPath(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)

	// Registered first so it runs LAST, after the row cleanup below.
	t.Cleanup(pg.Close)

	repo := NewQuranRepo(pg)
	ctx := context.Background()
	withFixtureWriter := func(fn func(pgx.Tx) error) error {
		tx, txErr := pg.Pool.Begin(ctx)
		if txErr != nil {
			return txErr
		}
		defer rollbackTx(ctx, tx)

		if _, txErr = tx.Exec(ctx,
			`SET LOCAL surau.quran_editorial_writer = 'quran-editorial-service'`); txErr != nil {
			return txErr
		}

		if txErr := fn(tx); txErr != nil {
			return txErr
		}

		return tx.Commit(ctx)
	}

	const (
		surahID     = 113
		permittedNo = 905 // full editorial, license permitted
		sourceID    = "f1e-editorial-readpath-source"
		// Unique token so SearchAyahs can only ever match the seeded row.
		searchNeedle = "f1ereadpathfixture905"
	)

	permittedKey := fmt.Sprintf("%d:%d", surahID, permittedNo)
	reviewNumbers := []int{901, 902, 903} // editorial rows stuck in needs_review

	seededNumbers := append([]int{}, reviewNumbers...)
	seededNumbers = append(seededNumbers, permittedNo)

	cleanup := func() {
		// Source delete cascades to its translations; ayah delete cascades to
		// editorial rows. Explicit editorial deletes keep this idempotent even
		// if a previous run died halfway; fixture DML carries the same local
		// workflow marker production writes require.
		if _, err := pg.Pool.Exec(ctx, `DELETE FROM quran_translation_sources WHERE id = $1`, sourceID); err != nil {
			t.Logf("cleanup source: %v", err)
		}

		if err := withFixtureWriter(func(tx pgx.Tx) error {
			for _, n := range seededNumbers {
				if _, txErr := tx.Exec(ctx,
					`DELETE FROM quran_ayah_editorial WHERE surah_id = $1 AND ayah_number = $2`, surahID, n); txErr != nil {
					return txErr
				}
			}

			return nil
		}); err != nil {
			t.Logf("cleanup editorial: %v", err)
		}

		for _, n := range seededNumbers {
			if _, err := pg.Pool.Exec(ctx,
				`DELETE FROM quran_ayahs WHERE surah_id = $1 AND ayah_number = $2`, surahID, n); err != nil {
				t.Logf("cleanup ayah %d: %v", n, err)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	// FK targets: the surah row (real corpus wins on conflict) and the ayahs.
	_, err = pg.Pool.Exec(ctx,
		`INSERT INTO quran_surahs (surah_id, ayah_count) VALUES ($1, 5) ON CONFLICT (surah_id) DO NOTHING`, surahID)
	require.NoError(t, err)

	for _, n := range seededNumbers {
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, metadata)
VALUES ($1, $2, $3, '{}'::jsonb)
ON CONFLICT (surah_id, ayah_number) DO NOTHING`, surahID, n, fmt.Sprintf("%d:%d", surahID, n))
		require.NoError(t, err)
	}

	// One fully-populated permitted editorial (the public read path must expose
	// it) and three needs_review rows (it must hide them).
	err = withFixtureWriter(func(tx pgx.Tx) error {
		if _, txErr := tx.Exec(ctx, `
INSERT INTO quran_ayah_editorial (
    surah_id, ayah_number, ayah_key, lang, meta_title, meta_description,
    intisari_html, keutamaan_html, faq, tafsir_range, license_status,
    status, published_at
) VALUES (
    $1, $2, $3, 'id', 'Fixture meta title 905', 'Fixture meta description',
    '<p>Intisari fixture.</p>', '<p>Keutamaan fixture.</p>',
    '[{"question":"Q1?","answer_html":"<p>A1.</p>"},{"question":"Q2?","answer_html":"<p>A2.</p>"}]'::jsonb,
    $4, 'permitted', 'published', now()
)`, surahID, permittedNo, permittedKey, fmt.Sprintf("%d", permittedNo)); txErr != nil {
			return txErr
		}

		for _, n := range reviewNumbers {
			if _, txErr := tx.Exec(ctx, `
INSERT INTO quran_ayah_editorial (surah_id, ayah_number, ayah_key, lang, meta_title, license_status)
VALUES ($1, $2, $3, 'id', 'Draft fixture', 'needs_review')`,
				surahID, n, fmt.Sprintf("%d:%d", surahID, n)); txErr != nil {
				return txErr
			}
		}

		return nil
	})
	require.NoError(t, err)

	// A throwaway translation source + one row carrying the unique token, so
	// the search sub-test matches exactly the seeded ayah.
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_translation_sources (id, lang, name, format, license_status, coverage_count)
VALUES ($1, 'id', 'F1-E Read Path Fixture', 'json', 'permitted', 0)`, sourceID)
	require.NoError(t, err)

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayah_translations (source_id, surah_id, ayah_number, ayah_key, lang, text)
VALUES ($1, $2, $3, $4, 'id', 'Terjemahan fixture dengan token `+searchNeedle+` di dalamnya.')`,
		sourceID, surahID, permittedNo, permittedKey)
	require.NoError(t, err)

	findByKey := func(ayahs []entity.QuranAyah, key string) *entity.QuranAyah {
		for i := range ayahs {
			if ayahs[i].AyahKey == key {
				return &ayahs[i]
			}
		}

		return nil
	}

	t.Run("GetAyah returns full permitted editorial", func(t *testing.T) {
		ayah, err := repo.GetAyah(ctx, permittedKey, "id", "", false, "")
		require.NoError(t, err)
		require.NotNil(t, ayah.Editorial, "permitted editorial must be attached")
		assert.Equal(t, "permitted", ayah.Editorial.LicenseStatus)
		require.NotNil(t, ayah.Editorial.MetaTitle)
		assert.NotEmpty(t, *ayah.Editorial.MetaTitle)
		require.NotNil(t, ayah.Editorial.Intisari, "detail read must include heavy intisari")
		require.NotNil(t, ayah.Editorial.Keutamaan, "detail read must include heavy keutamaan")
		assert.Len(t, ayah.Editorial.FAQ, 2, "both FAQ entries survive sanitization")
		require.NotNil(t, ayah.Editorial.TafsirRange)
		assert.Equal(t, fmt.Sprintf("%d", permittedNo), *ayah.Editorial.TafsirRange)
		assert.NotNil(t, ayah.ContentUpdatedAt, "content_updated_at must be populated")
	})

	t.Run("ListSurahAyahs hides needs_review editorial", func(t *testing.T) {
		// Whole-surah read: the from/to range check rejects numbers above the
		// surah's real ayah_count, so select the seeded rows by key instead.
		ayahs, err := repo.ListSurahAyahs(ctx, surahID, 0, 0, "id", "", false, false, true, "")
		require.NoError(t, err)

		for _, n := range reviewNumbers {
			ayah := findByKey(ayahs, fmt.Sprintf("%d:%d", surahID, n))
			require.NotNilf(t, ayah, "seeded ayah %d:%d must be listed", surahID, n)
			assert.Nilf(t, ayah.Editorial, "ayah %s is needs_review and must NOT leak editorial", ayah.AyahKey)
		}
	})

	t.Run("ListSurahAyahs returns light editorial only (no heavy HTML)", func(t *testing.T) {
		ayahs, err := repo.ListSurahAyahs(ctx, surahID, 0, 0, "id", "", false, false, true, "")
		require.NoError(t, err)

		ayah := findByKey(ayahs, permittedKey)
		require.NotNil(t, ayah, "seeded permitted ayah must be listed")

		ed := ayah.Editorial
		require.NotNil(t, ed, "permitted editorial visible in list")
		require.NotNil(t, ed.MetaTitle)
		assert.NotEmpty(t, *ed.MetaTitle, "light tier carries meta")
		assert.Nil(t, ed.Intisari, "list tier must NOT carry heavy intisari")
		assert.Nil(t, ed.Keutamaan, "list tier must NOT carry heavy keutamaan")
		assert.Empty(t, ed.FAQ, "list tier must NOT carry FAQ")
	})

	t.Run("ListSurahAyahs includeEditorial=false skips editorial (reader_minimal path)", func(t *testing.T) {
		ayahs, err := repo.ListSurahAyahs(ctx, surahID, 0, 0, "id", "", false, false, false, "")
		require.NoError(t, err)

		ayah := findByKey(ayahs, permittedKey)
		require.NotNil(t, ayah, "seeded permitted ayah must be listed")
		assert.Nil(t, ayah.Editorial, "includeEditorial=false must not attach editorial even for permitted")
	})

	t.Run("SearchAyahs returns hits and total via single-pass window", func(t *testing.T) {
		results, total, err := repo.SearchAyahs(ctx, repocontract.QuranSearchFilter{
			Query: searchNeedle, Lang: "id", Limit: 50, Offset: 0,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, total, 1, "windowed total must count the match")
		require.NotEmpty(t, results, "search finds the seeded ayah")
		assert.Equal(t, permittedKey, results[0].Ayah.AyahKey, "the unique token matches only the fixture")
	})

	t.Run("attachBookReferenceAyahs batches multiple ranges in one query", func(t *testing.T) {
		sOne, fOne, tOne := surahID, permittedNo, permittedNo
		sTri, fTri, tTri := surahID, reviewNumbers[0], reviewNumbers[len(reviewNumbers)-1]
		refs := []entity.BookQuranReference{
			{SurahID: &sOne, FromAyahNumber: &fOne, ToAyahNumber: &tOne},
			{SurahID: &sTri, FromAyahNumber: &fTri, ToAyahNumber: &tTri},
		}
		// Real P3 path: one unnest query through quranAyahSelectSQL, bucketed per ref.
		require.NoError(t, repo.attachBookReferenceAyahs(ctx, refs, "id", ""))

		require.Len(t, refs[0].Ayahs, 1, "single-ayah range yields one ayah")
		assert.Equal(t, permittedKey, refs[0].Ayahs[0].AyahKey)
		require.NotNil(t, refs[0].Ayahs[0].Editorial, "permitted editorial attached (light)")

		require.Len(t, refs[1].Ayahs, 3, "901-903 range yields three ayahs")
		assert.Equal(t, fmt.Sprintf("%d:%d", surahID, reviewNumbers[0]), refs[1].Ayahs[0].AyahKey)
		assert.Equal(t, fmt.Sprintf("%d:%d", surahID, reviewNumbers[len(reviewNumbers)-1]), refs[1].Ayahs[2].AyahKey)
	})
}
