package importer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveAyahEditorialImport drives the real per-ayah editorial importer against a
// live throwaway Postgres and proves the idempotency/clear semantics (G3) and the
// provenance-only persistence (G11). Gated on SURAU_LIVE_PG so it never runs in a
// normal `go test ./...`.
//
//	SURAU_LIVE_PG=postgres://... go test ./internal/importer/ -run TestLiveAyahEditorialImport -v
func TestLiveAyahEditorialImport(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	t.Parallel()

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close) // registered first → runs LAST, after the row cleanup below

	// Seed the FK targets (surah + ayah) and start from a clean editorial row.
	_, err = pool.Exec(ctx, `INSERT INTO quran_surahs (surah_id, ayah_count) VALUES (2, 286) ON CONFLICT (surah_id) DO NOTHING`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key) VALUES (2, 255, '2:255') ON CONFLICT (surah_id, ayah_number) DO NOTHING`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM quran_ayah_editorial WHERE surah_id = 2 AND ayah_number = 255`)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM quran_ayah_editorial WHERE surah_id = 2 AND ayah_number = 255`); err != nil {
			t.Logf("cleanup: %v", err)
		}
	})

	run := func(t *testing.T, body string) QuranAyahEditorialStats {
		t.Helper()
		path := filepath.Join(t.TempDir(), "editorial.json")
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
		stats, err := RunQuranAyahEditorialImport(ctx, QuranAyahEditorialOptions{PostgresURL: url, Paths: []string{path}})
		require.NoError(t, err)
		return stats
	}
	read := func(t *testing.T) (faqLen int, reviewedBy *string, updatedAt time.Time) {
		t.Helper()
		require.NoError(t, pool.QueryRow(
			ctx,
			`SELECT jsonb_array_length(faq), reviewed_by, updated_at FROM quran_ayah_editorial WHERE surah_id=2 AND ayah_number=255 AND lang='id'`,
		).Scan(&faqLen, &reviewedBy, &updatedAt))
		return faqLen, reviewedBy, updatedAt
	}

	const withFAQ = `[{"surah_id":2,"ayah_number":255,"lang":"id","meta_title":"Ayat Kursi","intisari_html":"<p>i</p>","tafsir_range":"255","reviewed_by":"rev1","faq":[{"question":"q","answer_html":"<p>a</p>"}]}]`

	// 1) Initial import with a FAQ.
	s1 := run(t, withFAQ)
	assert.Equal(t, 1, s1.Upserted)
	faqLen, reviewedBy, u1 := read(t)
	assert.Equal(t, 1, faqLen, "FAQ stored")
	require.NotNil(t, reviewedBy)
	assert.Equal(t, "rev1", *reviewedBy)

	// 2) Re-import the IDENTICAL payload → no-op, updated_at must not move (idempotent).
	s2 := run(t, withFAQ)
	assert.Equal(t, 1, s2.Skipped, "byte-identical re-import is skipped")
	_, _, u2 := read(t)
	assert.True(t, u1.Equal(u2), "no-op re-import must not bump updated_at")

	// 3) Re-import with faq:[] → FAQ cleared (G3), content changed so updated_at bumps.
	cleared := `[{"surah_id":2,"ayah_number":255,"lang":"id","meta_title":"Ayat Kursi","intisari_html":"<p>i</p>","tafsir_range":"255","reviewed_by":"rev1","faq":[]}]`
	run(t, cleared)
	faqLen, _, u3 := read(t)
	assert.Equal(t, 0, faqLen, "explicit faq:[] clears the FAQ")
	assert.True(t, u3.After(u1), "content change bumps updated_at")

	// 4) Provenance-only change (reviewed_by) with identical content → persisted (G11)
	//    but updated_at must NOT move (content checksum unchanged).
	provenance := `[{"surah_id":2,"ayah_number":255,"lang":"id","meta_title":"Ayat Kursi","intisari_html":"<p>i</p>","tafsir_range":"255","reviewed_by":"rev2","faq":[]}]`
	run(t, provenance)
	_, reviewedBy, u4 := read(t)
	require.NotNil(t, reviewedBy)
	assert.Equal(t, "rev2", *reviewedBy, "provenance-only change is persisted")
	assert.True(t, u3.Equal(u4), "provenance-only change must NOT bump updated_at")
}
