package persistent

import (
	"regexp"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

// TestQuranSurahSelectSQLColumnCountMatchesScan guards the SELECT/scan coupling:
// quranSurahSelectSQL must emit exactly the number of top-level columns that
// scanQuranSurah reads, identical across includeInfo / includeEditorialHTML so the
// NULL-fallback and real branches stay aligned. Bump expectedColumns ONLY together
// with the scan target list in scanQuranSurah.
//
// NOTE: this counts top-level SELECT columns only; it catches added/removed
// columns (the common mistake) but NOT a same-typed reorder — keep the SELECT and
// scan order in sync by hand.
func TestQuranSurahSelectSQLColumnCountMatchesScan(t *testing.T) {
	t.Parallel()

	const expectedColumns = 40

	for _, includeInfo := range []bool{false, true} {
		for _, includeEditorialHTML := range []bool{false, true} {
			query := quranSurahSelectSQL("", includeInfo, includeEditorialHTML)
			got := topLevelSelectColumnCount(t, query)
			assert.Equalf(t, expectedColumns, got,
				"includeInfo=%v includeEditorialHTML=%v: SELECT column count must equal scanQuranSurah targets",
				includeInfo, includeEditorialHTML)
		}
	}
}

// TestQuranAyahSelectSQLColumnCountMatchesScan guards the ayah SELECT/scan
// coupling. quranAyahSelectSQL ALWAYS emits the 9 editorial columns plus
// content_updated_at (real values when included, NULL-typed placeholders when
// not), so the top-level column count is CONSTANT across every flag combination
// and must equal what scanQuranAyahInternal reads with withEditorial=true (the
// path every quranAyahSelectSQL row is scanned through). Bump expectedColumns ONLY
// together with that scan target list.
func TestQuranAyahSelectSQLColumnCountMatchesScan(t *testing.T) {
	t.Parallel()

	const expectedColumns = 36

	for _, includeTranslation := range []bool{false, true} {
		for _, includeTransliteration := range []bool{false, true} {
			for _, includeEditorial := range []bool{false, true} {
				for _, includeEditorialHTML := range []bool{false, true} {
					query := quranAyahSelectSQL("", includeTranslation, includeTransliteration, includeEditorial, includeEditorialHTML)
					got := topLevelSelectColumnCount(t, query)
					assert.Equalf(t, expectedColumns, got,
						"includeTranslation=%v includeTransliteration=%v includeEditorial=%v includeEditorialHTML=%v: SELECT column count must equal scanQuranAyahInternal targets",
						includeTranslation, includeTransliteration, includeEditorial, includeEditorialHTML)
				}
			}
		}
	}
}

// TestQuranAyahSelectSQLEditorialColumnOrder complements the count guard: several
// editorial columns are the same type (text), so a same-typed reorder would slip
// past a count check yet silently mis-map onto the scan targets. Assert the alias
// order in the SELECT matches the order scanQuranAyahInternal appends them, in
// BOTH the NULL-placeholder (list/nav) and real-column (detail) variants.
func TestQuranAyahSelectSQLEditorialColumnOrder(t *testing.T) {
	t.Parallel()

	want := []string{
		"ed_lang",
		"ed_meta_title",
		"ed_meta_description",
		"ed_tafsir_range",
		"ed_license_status",
		"ed_updated_at",
		"ed_intisari_html",
		"ed_keutamaan_html",
		"ed_faq",
		"content_updated_at",
	}

	for _, includeEditorial := range []bool{false, true} {
		query := quranAyahSelectSQL("", true, true, includeEditorial, includeEditorial)
		assert.Equalf(t, want, editorialColumnAliases(query),
			"includeEditorial=%v: editorial alias order must match scanQuranAyahInternal append order", includeEditorial)
	}
}

var editorialAliasRe = regexp.MustCompile(`AS (ed_[a-z_]+|content_updated_at)`)

// editorialColumnAliases returns the editorial + content_updated_at column aliases
// in the order they appear in the query.
func editorialColumnAliases(query string) []string {
	matches := editorialAliasRe.FindAllStringSubmatch(query, -1)

	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}

	return out
}

// topLevelSelectColumnCount counts comma-separated columns in the SELECT...FROM
// clause at parenthesis depth 0, so commas inside GREATEST/COALESCE/array_agg are
// not miscounted.
func topLevelSelectColumnCount(t *testing.T, query string) int {
	t.Helper()

	upper := strings.ToUpper(query)
	selectIdx := strings.Index(upper, "SELECT ")
	fromIdx := strings.Index(upper, "\nFROM ")
	if selectIdx < 0 || fromIdx < 0 || fromIdx <= selectIdx {
		t.Fatalf("could not locate SELECT...FROM in query")
	}

	clause := query[selectIdx+len("SELECT ") : fromIdx]
	depth := 0
	columns := 1
	for _, r := range clause {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				columns++
			}
		}
	}

	return columns
}

// TestQuranRecitationSelectColumnCountMatchesScan guards the recitation SELECT/scan
// coupling: ListRecitations and getVisibleRecitation share quranRecitationSelectColumns,
// which must emit exactly the number of top-level columns scanQuranRecitation reads.
// Bump expectedColumns ONLY together with the scan target list in scanQuranRecitation.
func TestQuranRecitationSelectColumnCountMatchesScan(t *testing.T) {
	t.Parallel()

	const expectedColumns = 22

	got := topLevelSelectColumnCount(t, "SELECT "+quranRecitationSelectColumns+"\nFROM quran_recitations")
	assert.Equal(t, expectedColumns, got,
		"recitation SELECT column count must equal scanQuranRecitation targets")
}

func TestMarkDefaultRecitationPrefersFullPublicAyah(t *testing.T) {
	t.Parallel()

	recitations := []entity.QuranRecitation{
		{ID: "surah-public", Name: "A", Mode: "surah", TrackCount: 114, PublicTrackCount: 114, PlayableTrackCount: 114, CoveragePercent: 1, HasPublicAudio: true, HasPlayableAudio: true},
		{ID: "ayah-partial", Name: "A", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 2, PlayableTrackCount: 2, CoveragePercent: 2.0 / 6236.0, HasPublicAudio: false, HasPlayableAudio: false},
		{ID: "ayah-public", Name: "B", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 6236, PlayableTrackCount: 6236, CoveragePercent: 1, HasPublicAudio: true, HasPlayableAudio: true},
	}

	markDefaultRecitation(recitations)

	// Two recitations are fully covered (surah-public, ayah-public); the ayah mode wins
	// the coverage tie, and the barely-covered ayah-partial never beats them.
	assert.False(t, recitations[0].IsDefault)
	assert.False(t, recitations[1].IsDefault)
	assert.True(t, recitations[2].IsDefault)
}

func TestMarkDefaultRecitationPrefersPinnedPriority(t *testing.T) {
	t.Parallel()

	priority := 0
	recitations := []entity.QuranRecitation{
		{ID: "abdul-basit", DisplayName: "Abdul Basit", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 6236, PlayableTrackCount: 6236, HasPublicAudio: true, HasPlayableAudio: true},
		{ID: "mishari", DisplayName: "Mishari Rashid Al-Afasy", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 6236, PlayableTrackCount: 6236, HasPublicAudio: true, HasPlayableAudio: true, DefaultPriority: &priority},
	}

	markDefaultRecitation(recitations)

	assert.False(t, recitations[0].IsDefault)
	assert.True(t, recitations[1].IsDefault)
}

func TestMarkDefaultRecitationAcceptsSourceAudioFallback(t *testing.T) {
	t.Parallel()

	recitations := []entity.QuranRecitation{
		{
			ID:                 "ayah-source-audio",
			Name:               "A",
			Mode:               "ayah",
			TrackCount:         6236,
			PublicTrackCount:   0,
			PlayableTrackCount: 6236,
			HasPublicAudio:     false,
			HasPlayableAudio:   true,
		},
	}

	markDefaultRecitation(recitations)

	assert.True(t, recitations[0].IsDefault)
}

func TestMarkDefaultRecitationPicksMostCompleteWhenAllPartial(t *testing.T) {
	t.Parallel()

	// No recitation is fully playable, but one has audio and the other has none. The
	// old rule (require every track playable) left NO default here, which wrongly
	// excluded a mostly-complete recitation. Now the recitation with playable coverage
	// wins over the empty one.
	recitations := []entity.QuranRecitation{
		{ID: "ayah-partial", Name: "A", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 6000, PlayableTrackCount: 6000, CoveragePercent: 6000.0 / 6236.0, HasPublicAudio: false, HasPlayableAudio: false},
		{ID: "surah-empty", Name: "B", Mode: "surah", TrackCount: 0, PublicTrackCount: 0, PlayableTrackCount: 0, CoveragePercent: 0, HasPublicAudio: false, HasPlayableAudio: false},
	}

	markDefaultRecitation(recitations)

	assert.True(t, recitations[0].IsDefault)
	assert.False(t, recitations[1].IsDefault)
}

func TestMarkDefaultRecitationLeavesNoDefaultWithoutPlayableAudio(t *testing.T) {
	t.Parallel()

	// Nothing has a playable track → no default at all.
	recitations := []entity.QuranRecitation{
		{ID: "ayah-empty", Name: "A", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 0, PlayableTrackCount: 0, HasPublicAudio: false, HasPlayableAudio: false},
		{ID: "surah-empty", Name: "B", Mode: "surah", TrackCount: 0, PublicTrackCount: 0, PlayableTrackCount: 0, HasPublicAudio: false, HasPlayableAudio: false},
	}

	markDefaultRecitation(recitations)

	assert.False(t, recitations[0].IsDefault)
	assert.False(t, recitations[1].IsDefault)
}

func TestQuranAudioTrackLessPrefersAyahTrack(t *testing.T) {
	t.Parallel()

	ayahTrack := entity.QuranAudioTrack{RecitationID: "rec", TrackType: "ayah", TrackKey: "73:1"}
	surahTrack := entity.QuranAudioTrack{RecitationID: "rec", TrackType: "surah", TrackKey: "73"}

	assert.True(t, quranAudioTrackLess(&ayahTrack, &surahTrack))
	assert.False(t, quranAudioTrackLess(&surahTrack, &ayahTrack))
}

func TestQuranAudioTrackLessNaturalAyahOrder(t *testing.T) {
	t.Parallel()

	n2, n10 := 2, 10
	track2 := entity.QuranAudioTrack{RecitationID: "rec", TrackType: "ayah", SurahID: 1, AyahNumber: &n2, TrackKey: "1:2"}
	track10 := entity.QuranAudioTrack{RecitationID: "rec", TrackType: "ayah", SurahID: 1, AyahNumber: &n10, TrackKey: "1:10"}

	// Regression for lexicographic track_key sort: "1:2" must precede "1:10".
	assert.True(t, quranAudioTrackLess(&track2, &track10))
	assert.False(t, quranAudioTrackLess(&track10, &track2))
}

func TestManifestAudioCoverage(t *testing.T) {
	t.Parallel()

	publicURL := "https://cdn.example/1.mp3"
	ayahNumber := 1

	// Ayah mode: an ayah with no playable track is genuinely missing.
	missing, segmentMissing, hasFull := manifestAudioCoverage(
		[]string{"1:1", "1:2"}, "ayah", []entity.QuranAudioTrack{{
			TrackType:  "ayah",
			TrackKey:   "1:1",
			AyahNumber: &ayahNumber,
			PublicURL:  &publicURL,
		}},
	)
	assert.Equal(t, []string{"1:2"}, missing)
	assert.Empty(t, segmentMissing)
	assert.False(t, hasFull)

	// Surah mode with a playable full-surah track + one segment: the full-surah audio
	// covers every ayah (missing is empty), only 1:2 lacks a per-ayah seek offset.
	missing, segmentMissing, hasFull = manifestAudioCoverage(
		[]string{"1:1", "1:2"}, "surah", []entity.QuranAudioTrack{{
			TrackType: "surah",
			TrackKey:  "1",
			PublicURL: &publicURL,
			Segments:  []entity.QuranAudioSegment{{AyahKey: "1:1"}},
		}},
	)
	assert.Empty(t, missing)
	assert.Equal(t, []string{"1:2"}, segmentMissing)
	assert.True(t, hasFull)

	// Surah mode, playable full-surah track, no segments at all: audio still plays for
	// the whole surah, so nothing is "missing" — every ayah is only segment-missing.
	missing, segmentMissing, hasFull = manifestAudioCoverage(
		[]string{"1:1", "1:2"}, "surah", []entity.QuranAudioTrack{{
			TrackType: "surah",
			TrackKey:  "1",
			PublicURL: &publicURL,
		}},
	)
	assert.Empty(t, missing)
	assert.Equal(t, []string{"1:1", "1:2"}, segmentMissing)
	assert.True(t, hasFull)

	// Surah mode with no playable track: the whole surah has no audio.
	missing, segmentMissing, hasFull = manifestAudioCoverage(
		[]string{"1:1", "1:2"}, "surah", []entity.QuranAudioTrack{{
			TrackType: "surah",
			TrackKey:  "1",
		}},
	)
	assert.Equal(t, []string{"1:1", "1:2"}, missing)
	assert.Empty(t, segmentMissing)
	assert.False(t, hasFull)
}

func TestQuranNavigationColumnAllowlist(t *testing.T) {
	t.Parallel()

	column, err := quranNavigationColumn("juz")
	assert.NoError(t, err)
	assert.Equal(t, "juz_number", column)

	column, err = quranNavigationColumn("hizb")
	assert.NoError(t, err)
	assert.Equal(t, "hizb_number", column)

	column, err = quranNavigationColumn("page")
	assert.NoError(t, err)
	assert.Equal(t, "page_number", column)

	_, err = quranNavigationColumn("page_number; DROP TABLE quran_ayahs")
	assert.ErrorIs(t, err, entity.ErrInvalidQuranRange)
}

func TestApplyQuranAyahMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		ayah        entity.QuranAyah
		lang        string
		wantMissing bool
		wantAction  string
	}{
		{
			name:       "arabic hides translation tab",
			ayah:       entity.QuranAyah{AvailableTranslationLangs: []string{"id"}},
			lang:       "ar",
			wantAction: entity.AvailabilityActionHideTranslation,
		},
		{
			name: "exact requested translation",
			ayah: entity.QuranAyah{
				Translation:               &entity.QuranTranslation{Lang: "id", Text: "Terjemah"},
				AvailableTranslationLangs: []string{"id"},
			},
			lang:       "id",
			wantAction: entity.AvailabilityActionShowRequested,
		},
		{
			name: "missing requested with alternative",
			ayah: entity.QuranAyah{
				AvailableTranslationLangs: []string{"id"},
			},
			lang:        "en",
			wantMissing: true,
			wantAction:  entity.AvailabilityActionOfferLang,
		},
		{
			name:        "missing requested without alternative",
			ayah:        entity.QuranAyah{},
			lang:        "en",
			wantMissing: true,
			wantAction:  entity.AvailabilityActionHideTranslation,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			applyQuranAyahMetadata(&tt.ayah, tt.lang, true, false)

			assert.Equal(t, tt.lang, tt.ayah.RequestedLang)
			assert.Equal(t, tt.wantMissing, tt.ayah.TranslationMissing)
			assert.Equal(t, tt.wantAction, tt.ayah.Availability.Translation.Action)
		})
	}
}
