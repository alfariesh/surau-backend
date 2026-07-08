package importer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAyahEditorialChecksumContentOnly proves the idempotency contract: the
// checksum covers content-bearing fields ONLY. A publish (needs_review->permitted)
// or a provenance-only change must NOT move the checksum — otherwise re-imports
// would churn updated_at and the sitemap lastmod across thousands of rows.
func TestAyahEditorialChecksumContentOnly(t *testing.T) {
	t.Parallel()

	base := quranAyahEditorialRecord{
		SurahID:         2,
		AyahNumber:      255,
		Lang:            "id",
		MetaTitle:       new("Ayat Kursi"),
		MetaDescription: new("Ayat terbesar"),
		IntisariHTML:    new("<p>intisari</p>"),
		KeutamaanHTML:   new("<ul><li>x</li></ul>"),
		TafsirRange:     new("255"),
		faqProvided:     true,
		faqJSON:         []byte(`[{"question":"q","answer_html":"<p>a</p>"}]`),
		license:         "needs_review",
		LicenseStatus:   new("needs_review"),
		AuthorName:      new("Tim Surau"),
	}
	baseSum := ayahEditorialChecksum(base)

	t.Run("license change does not move checksum", func(t *testing.T) {
		t.Parallel()

		rec := base
		rec.license = "permitted"
		rec.LicenseStatus = new("permitted")
		assert.Equal(t, baseSum, ayahEditorialChecksum(rec))
	})

	t.Run("provenance change does not move checksum", func(t *testing.T) {
		t.Parallel()

		rec := base
		rec.AuthorName = new("Someone Else")
		rec.ReviewedBy = new("reviewer")
		assert.Equal(t, baseSum, ayahEditorialChecksum(rec))
	})

	t.Run("content change moves checksum", func(t *testing.T) {
		t.Parallel()

		rec := base
		rec.MetaTitle = new("Ayat Kursi (revisi)")
		assert.NotEqual(t, baseSum, ayahEditorialChecksum(rec))
	})

	t.Run("faq change moves checksum", func(t *testing.T) {
		t.Parallel()

		rec := base
		rec.faqJSON = []byte(`[{"question":"q2","answer_html":"<p>a2</p>"}]`)
		assert.NotEqual(t, baseSum, ayahEditorialChecksum(rec))
	})
}

// TestDecodeQuranAyahEditorialRecordFaqPresence proves the importer distinguishes an
// absent faq key (keep stored) from an explicit faq:[] (clear), which is what makes a
// FAQ deletable on re-import and keeps the checksum a pure function of the payload.
func TestDecodeQuranAyahEditorialRecordFaqPresence(t *testing.T) {
	t.Parallel()

	absent, err := decodeQuranAyahEditorialRecord(json.RawMessage(`{"surah_id":2,"ayah_number":255,"lang":"id"}`))
	require.NoError(t, err)
	assert.False(t, absent.faqProvided)
	assert.Nil(t, absent.faqJSON)

	empty, err := decodeQuranAyahEditorialRecord(json.RawMessage(`{"surah_id":2,"ayah_number":255,"lang":"id","faq":[]}`))
	require.NoError(t, err)
	assert.True(t, empty.faqProvided)
	assert.Equal(t, []byte("[]"), empty.faqJSON)

	// Absent (keep) and present-empty (clear) must hash differently.
	assert.NotEqual(t, ayahEditorialChecksum(absent), ayahEditorialChecksum(empty))
}

// TestDecodeQuranAyahEditorialRecordRejectsUnknownField guards against a typo'd content
// key silently no-op'ing through the COALESCE upsert (G11).
func TestDecodeQuranAyahEditorialRecordRejectsUnknownField(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"surah_id":2,"ayah_number":255,"lang":"id","intisari":"<p>typo key</p>"}`)
	_, err := decodeQuranAyahEditorialRecord(raw)
	require.Error(t, err)
}

// TestDecodeQuranAyahEditorialRecordRejectsTafsirHTML guards the sacred-text
// boundary: this layer stores SEO enrichment + a tafsir_range pointer, never the
// reproduced tafsir body. A stray tafsir_html key must fail loudly.
func TestDecodeQuranAyahEditorialRecordRejectsTafsirHTML(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"surah_id":2,"ayah_number":255,"lang":"id","tafsir_html":"<p>...</p>"}`)
	_, err := decodeQuranAyahEditorialRecord(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "tafsir_html")
}

// TestDecodeQuranAyahEditorialRecordValidations covers the cheap fail-fast checks
// that run before any DB connection.
func TestDecodeQuranAyahEditorialRecordValidations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "valid minimal", raw: `{"surah_id":2,"ayah_number":255,"lang":"id","tafsir_range":"255"}`, ok: true},
		{name: "valid range", raw: `{"surah_id":113,"ayah_number":1,"lang":"id","tafsir_range":"1-5"}`, ok: true},
		{name: "surah out of range", raw: `{"surah_id":115,"ayah_number":1,"lang":"id"}`},
		{name: "ayah zero", raw: `{"surah_id":2,"ayah_number":0,"lang":"id"}`},
		{name: "bad lang", raw: `{"surah_id":2,"ayah_number":255,"lang":"fr"}`},
		{name: "bad tafsir_range", raw: `{"surah_id":2,"ayah_number":255,"lang":"id","tafsir_range":"a-b"}`},
		{name: "faq missing answer", raw: `{"surah_id":2,"ayah_number":255,"lang":"id","faq":[{"question":"q","answer_html":""}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := decodeQuranAyahEditorialRecord(json.RawMessage(tt.raw))
			if tt.ok {
				require.NoError(t, err)

				return
			}

			require.Error(t, err)
		})
	}
}
