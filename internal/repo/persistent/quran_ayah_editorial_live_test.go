package persistent

import (
	"context"
	"os"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	repocontract "github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveAyahEditorialReadPath exercises the real repo read path against a live
// throwaway Postgres seeded by the verification script. Gated on SURAU_LIVE_PG so
// it never runs in normal `go test ./...`.
//
//	SURAU_LIVE_PG=postgres://... go test ./internal/repo/persistent/ -run TestLiveAyahEditorialReadPath -v
func TestLiveAyahEditorialReadPath(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	defer pg.Close()
	repo := NewQuranRepo(pg)
	ctx := context.Background()

	t.Run("GetAyah 2:255 returns full permitted editorial", func(t *testing.T) {
		ayah, err := repo.GetAyah(ctx, "2:255", "id", "", false, "")
		require.NoError(t, err)
		require.NotNil(t, ayah.Editorial, "permitted editorial must be attached")
		assert.Equal(t, "permitted", ayah.Editorial.LicenseStatus)
		require.NotNil(t, ayah.Editorial.MetaTitle)
		assert.NotEmpty(t, *ayah.Editorial.MetaTitle)
		require.NotNil(t, ayah.Editorial.Intisari, "detail read must include heavy intisari")
		require.NotNil(t, ayah.Editorial.Keutamaan, "detail read must include heavy keutamaan")
		assert.Len(t, ayah.Editorial.FAQ, 2, "both FAQ entries survive sanitization")
		require.NotNil(t, ayah.Editorial.TafsirRange)
		assert.Equal(t, "255", *ayah.Editorial.TafsirRange)
		assert.NotNil(t, ayah.ContentUpdatedAt, "content_updated_at must be populated")
	})

	t.Run("ListSurahAyahs 113 hides needs_review editorial", func(t *testing.T) {
		ayahs, err := repo.ListSurahAyahs(ctx, 113, 0, 0, "id", "", false, false, true, "")
		require.NoError(t, err)
		require.NotEmpty(t, ayahs)
		for _, a := range ayahs {
			assert.Nilf(t, a.Editorial, "ayah %s is needs_review and must NOT leak editorial", a.AyahKey)
		}
	})

	t.Run("ListSurahAyahs 2 returns light editorial only (no heavy HTML)", func(t *testing.T) {
		ayahs, err := repo.ListSurahAyahs(ctx, 2, 255, 255, "id", "", false, false, true, "")
		require.NoError(t, err)
		require.Len(t, ayahs, 1)
		ed := ayahs[0].Editorial
		require.NotNil(t, ed, "permitted editorial visible in list")
		require.NotNil(t, ed.MetaTitle)
		assert.NotEmpty(t, *ed.MetaTitle, "light tier carries meta")
		assert.Nil(t, ed.Intisari, "list tier must NOT carry heavy intisari")
		assert.Nil(t, ed.Keutamaan, "list tier must NOT carry heavy keutamaan")
		assert.Empty(t, ed.FAQ, "list tier must NOT carry FAQ")
	})

	t.Run("ListSurahAyahs includeEditorial=false skips editorial (reader_minimal path)", func(t *testing.T) {
		ayahs, err := repo.ListSurahAyahs(ctx, 2, 255, 255, "id", "", false, false, false, "")
		require.NoError(t, err)
		require.Len(t, ayahs, 1)
		assert.Nil(t, ayahs[0].Editorial, "includeEditorial=false must not attach editorial even for permitted")
	})

	t.Run("SearchAyahs returns hits and total via single-pass window", func(t *testing.T) {
		results, total, err := repo.SearchAyahs(ctx, repocontract.QuranSearchFilter{
			Query: "muzammil", Lang: "id", Limit: 50, Offset: 0,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, total, 1, "windowed total must count the match")
		assert.NotEmpty(t, results, "search finds the seeded ayah")
	})

	t.Run("attachBookReferenceAyahs batches multiple ranges in one query", func(t *testing.T) {
		s2, f2, t2 := 2, 255, 255
		s113, f113, t113 := 113, 1, 3
		refs := []entity.BookQuranReference{
			{SurahID: &s2, FromAyahNumber: &f2, ToAyahNumber: &t2},
			{SurahID: &s113, FromAyahNumber: &f113, ToAyahNumber: &t113},
		}
		// Real P3 path: one unnest query through quranAyahSelectSQL, bucketed per ref.
		require.NoError(t, repo.attachBookReferenceAyahs(ctx, refs, "id", ""))

		require.Len(t, refs[0].Ayahs, 1, "2:255 range yields one ayah")
		assert.Equal(t, "2:255", refs[0].Ayahs[0].AyahKey)
		require.NotNil(t, refs[0].Ayahs[0].Editorial, "permitted editorial attached (light)")

		require.Len(t, refs[1].Ayahs, 3, "113:1-3 range yields three ayahs")
		assert.Equal(t, "113:1", refs[1].Ayahs[0].AyahKey)
		assert.Equal(t, "113:3", refs[1].Ayahs[2].AyahKey)
	})
}
