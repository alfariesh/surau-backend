package persistent

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveQuranSitemapCoverageInvariant proves Q-4 against the real corpus:
// the repository set equals every published+permitted id/en page in both
// directions, lastmod equals the effective database timestamp exactly, and
// the operator report remains internally complete.
//
//	SURAU_LIVE_PG=postgres://... go test -p 1 ./internal/repo/persistent -run TestLiveQuranSitemapCoverageInvariant -v
//
//nolint:paralleltest // serial live-corpus invariant and timing gate
func TestLiveQuranSitemapCoverageInvariant(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	repository := NewQuranRepo(pg)
	items, err := repository.ListQuranSitemap(ctx)
	require.NoError(t, err)
	require.LessOrEqual(t, len(items), 50000, "one sitemap must stay below the protocol URL limit")

	expected := loadExpectedQuranSitemap(ctx, t, pg)

	actual := make(map[string]time.Time, len(items))
	for _, item := range items {
		key := liveQuranSitemapKey(&item)
		_, duplicate := actual[key]
		require.Falsef(t, duplicate, "duplicate sitemap page %s", key)
		actual[key] = item.Lastmod
	}

	assert.Equal(t, len(expected), len(actual), "100%% of eligible pages, and only eligible pages, must be listed")

	for key, wantLastmod := range expected {
		gotLastmod, ok := actual[key]
		assert.Truef(t, ok, "eligible page missing from sitemap: %s", key)

		if ok {
			assert.WithinDuration(t, wantLastmod, gotLastmod, 0,
				"lastmod must equal effective updated_at exactly (stronger than five minutes): %s", key)
		}
	}

	for key := range actual {
		_, ok := expected[key]
		assert.Truef(t, ok, "ineligible page leaked into sitemap: %s", key)
	}

	feed, feedTotal, err := repository.ListQuranFeed(ctx, repo.QuranFeedFilter{Limit: 200})
	require.NoError(t, err)
	require.Equal(t, len(items), feedTotal)
	require.Len(t, feed, min(len(items), 200))

	for index := 1; index < len(feed); index++ {
		assert.False(t, feed[index].Lastmod.After(feed[index-1].Lastmod), "feed must be lastmod-descending")
	}

	future := time.Now().Add(24 * time.Hour)
	emptyFeed, emptyTotal, err := repository.ListQuranFeed(ctx, repo.QuranFeedFilter{
		Since: &future,
		Limit: 1,
	})
	require.NoError(t, err)
	require.Empty(t, emptyFeed)
	require.Zero(t, emptyTotal)

	if len(items) > 0 {
		resolution, resolveErr := repository.ResolveQuranSurahSlug(ctx, items[0].Slug)
		require.NoError(t, resolveErr)
		assert.Equal(t, items[0].SurahID, resolution.SurahID)
		assert.False(t, resolution.IsAlias)
	}

	_, err = repository.ResolveQuranSurahSlug(ctx, "q4-live-slug-not-registered")
	require.ErrorIs(t, err, entity.ErrQuranSlugNotFound)

	coverage, err := repository.ListQuranEditorialCoverage(ctx)
	require.NoError(t, err)
	require.Len(t, coverage, quranEditorialCoverageRowCount)

	for _, row := range coverage {
		states := row.Indexable + row.PublishedBlockedLicense + row.WorkflowIncomplete + row.MissingEditorial + row.MissingSlug
		assert.Equalf(t, row.TotalTargets, states, "coverage states must be exhaustive for %s/%s", row.Lang, row.PageType)
		assert.Zero(t, row.MissingSlug, "publish guard must keep missing_slug at zero")

		if row.Lang == "ar" {
			assert.Zero(t, row.SitemapItems)
		} else {
			assert.Equal(t, row.Indexable, row.SitemapItems)
		}
	}

	for range 5 {
		_, err = repository.ListQuranSitemap(ctx)
		require.NoError(t, err)
	}

	durations := make([]time.Duration, 20)
	for index := range durations {
		started := time.Now()
		_, err = repository.ListQuranSitemap(ctx)
		require.NoError(t, err)

		durations[index] = time.Since(started)
	}

	slices.Sort(durations)
	p95 := durations[(len(durations)*95+99)/100-1]
	assert.Less(t, p95, 200*time.Millisecond, "Quran sitemap repository p95 must meet the public-read target")
	t.Logf("Q-4 sitemap pages=%d p95=%s", len(items), p95)
}

func loadExpectedQuranSitemap(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
) map[string]time.Time {
	t.Helper()

	rows, err := pg.Pool.Query(ctx, `
SELECT page_type, surah_id, ayah_number, lang, lastmod
FROM (
    SELECT 'surah'::TEXT AS page_type,
           editorial.surah_id,
           NULL::INTEGER AS ayah_number,
           editorial.lang,
           GREATEST(surah.updated_at, editorial.updated_at) AS lastmod
    FROM quran_surah_editorial editorial
    JOIN quran_surahs surah ON surah.surah_id = editorial.surah_id
    JOIN quran_surah_slug_registry registry
      ON registry.slug = surah.slug AND registry.surah_id = surah.surah_id
    WHERE editorial.status = 'published'
      AND editorial.license_status = 'permitted'
      AND editorial.lang IN ('id', 'en')
      AND EXISTS (SELECT 1 FROM public_quran_script_sources WHERE id = 'qpc-hafs')

    UNION ALL

    SELECT 'ayah', editorial.surah_id, editorial.ayah_number, editorial.lang,
           GREATEST(ayah.updated_at, editorial.updated_at)
    FROM quran_ayah_editorial editorial
    JOIN quran_ayahs ayah
      ON ayah.surah_id = editorial.surah_id
     AND ayah.ayah_number = editorial.ayah_number
    JOIN quran_surahs surah ON surah.surah_id = editorial.surah_id
    JOIN quran_surah_slug_registry registry
      ON registry.slug = surah.slug AND registry.surah_id = surah.surah_id
    WHERE editorial.status = 'published'
      AND editorial.license_status = 'permitted'
      AND editorial.lang IN ('id', 'en')
      AND EXISTS (SELECT 1 FROM public_quran_script_sources WHERE id = 'qpc-hafs')
	) expected`)
	require.NoError(t, err)

	defer rows.Close()

	result := make(map[string]time.Time)

	for rows.Next() {
		var (
			pageType   string
			surahID    int
			ayahNumber *int
			lang       string
			lastmod    time.Time
		)
		require.NoError(t, rows.Scan(&pageType, &surahID, &ayahNumber, &lang, &lastmod))
		key := fmt.Sprintf("%s:%d:%d:%s", pageType, surahID, pointerIntValue(ayahNumber), lang)
		result[key] = lastmod
	}

	require.NoError(t, rows.Err())

	return result
}

func liveQuranSitemapKey(item *entity.QuranSitemapItem) string {
	return fmt.Sprintf("%s:%d:%d:%s", item.PageType, item.SurahID, pointerIntValue(item.AyahNumber), item.Lang)
}

func pointerIntValue(value *int) int {
	if value == nil {
		return 0
	}

	return *value
}
