package importer

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunQuranAssetImportDryRun(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := QuranAssetOptions{
		SurahNamesPath:              writeQuranFixture(t, dir, "surahs.json", `[{"surah_id":73,"name_arabic":"المزمل","name_latin":"Al-Muzzammil","ayah_count":2}]`),
		SurahInfoPaths:              []string{writeQuranZipFixture(t, dir, "surah-info-id.json.zip", "surah-info-id.json", `{"73":{"surah_number":73,"surah_name":"Al-Muzzammil","text":"<p>Info</p>","short_text":"Info pendek"}}`)},
		ScriptQPCHafsPath:           writeQuranFixture(t, dir, "qpc.json", `[{"verse_key":"73:1","text":"يَـٰٓأَيُّهَا ٱلْمُزَّمِّلُ","page_number":574},{"verse_key":"73:2","text":"قُمِ ٱلَّيْلَ إِلَّا قَلِيلًا"}]`),
		ScriptImlaeiSimplePath:      writeQuranFixture(t, dir, "simple.json", `{"73:1":"يا ايها المزمل","73:2":"قم الليل الا قليلا"}`),
		TranslationSimplePath:       writeQuranFixture(t, dir, "translation.json", `{"73:1":{"t":"Wahai orang yang berselimut!"},"73:2":{"t":"Bangunlah pada malam hari, kecuali sebagian kecil."}}`),
		TranslationFootnoteTagsPath: writeQuranFixture(t, dir, "footnotes.json", `[{"verse_key":"73:1","text":"Wahai orang yang berselimut!","footnotes":[{"n":1,"t":"Catatan"}]}]`),
		TransliterationPaths: []QuranTransliterationPath{
			{Lang: "id", Path: writeQuranFixture(t, dir, "latin.json", `{"73:1":"Yā ayyuhal-muzzammil(u).","73:2":"Qumil-laila illā qalīlā(n)."}`)},
		},
		RecitationPath: writeQuranZipFixture(
			t,
			dir,
			"surah-recitation-test.zip",
			"segments.json",
			`{"73:1":{"segments":[[1,0,2500]],"timestamp_from":0,"timestamp_to":2500}}`,
			"surah.json",
			`{"73":{"surah_number":73,"audio_url":"https://cdn.example/073.mp3","duration":120}}`,
		),
		DryRun: true,
	}

	stats, err := RunQuranAssetImport(context.Background(), opts)

	require.NoError(t, err)
	assert.True(t, stats.DryRun)
	assert.Equal(t, 1, stats.Surahs)
	assert.Equal(t, 1, stats.SurahInfos)
	assert.Equal(t, 2, stats.Ayahs)
	assert.Equal(t, 2, stats.Translations)
	assert.Equal(t, 2, stats.Transliterations)
	assert.Equal(t, 1, stats.Recitations)
	assert.Equal(t, 1, stats.AudioTracks)
	assert.Equal(t, 1, stats.AudioSegments)
}

func TestParseTransliterationMapSkipsInvalidAyahKeys(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	assets := quranAssetSet{
		surahs:                 make(map[int]*quranSurahImport),
		ayahs:                  make(map[string]*quranAyahImport),
		transliterationSources: make(map[string]*quranTransliterationSourceImport),
		transliterations:       make(map[string]*quranTransliterationImport),
		checksums:              make(map[string]string),
	}

	err := parseTransliteration(QuranTransliterationPath{
		Lang: "en",
		Path: writeQuranFixture(t, dir, "en-transliteration.json", `{"73:1":"yaayyuha al-muzammilu","bad-key":"skip me"}`),
	}, &assets)

	require.NoError(t, err)
	require.Len(t, assets.transliterations, 1)
	assert.Contains(t, assets.transliterations, defaultEnglishTransliterationSourceID+":73:1")
	assert.Contains(t, assets.transliterationSources, defaultEnglishTransliterationSourceID)
	assert.Contains(t, assets.ayahs, "73:1")
	assert.NotContains(t, assets.ayahs, "bad-key")
}

func TestParseKemenagTranslationWithStructuredFootnotes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	opts := QuranAssetOptions{
		SurahNamesPath:         writeQuranFixture(t, dir, "surahs.json", `[{"surah_id":1,"name_arabic":"الفاتحة","ayah_count":1}]`),
		ScriptQPCHafsPath:      writeQuranFixture(t, dir, "qpc.json", `[{"verse_key":"1:1","text":"بِسْمِ اللّٰهِ الرَّحْمٰنِ الرَّحِيْمِ"}]`),
		ScriptImlaeiSimplePath: writeQuranFixture(t, dir, "simple.json", `{"1:1":"بسم الله الرحمن الرحيم"}`),
		TranslationFootnoteTagsPath: writeQuranFixture(t, dir, "kemenag-translation.json", `[{
			"verse_key":"1:1",
			"text":"Dengan nama Allah1)",
			"footnotes":[{"number":1,"marker":"1)","text":"Catatan resmi Kemenag"}],
			"metadata":{"source":"kemenag-quran-ayah","kemenag_id":1}
		}]`),
		DryRun: true,
	}

	assets, err := parseQuranAssets(opts)

	require.NoError(t, err)
	require.Contains(t, assets.translations, "1:1")
	assert.JSONEq(t, `[{"number":1,"marker":"1)","text":"Catatan resmi Kemenag"}]`, string(assets.translations["1:1"].Footnotes))
	assert.Contains(t, string(assets.translations["1:1"].Metadata), "kemenag-quran-ayah")
}

func TestQuranAssetImportLangOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	base := QuranAssetOptions{
		SurahNamesPath:         writeQuranFixture(t, dir, "surahs.json", `[{"surah_id":73,"name_arabic":"المزمل","ayah_count":1}]`),
		SurahInfoPaths:         []string{writeQuranFixture(t, dir, "surah-info.json", `{"73":{"surah_number":73,"text":"<p>Info</p>"}}`)},
		ScriptQPCHafsPath:      writeQuranFixture(t, dir, "qpc.json", `[{"verse_key":"73:1","text":"يَـٰٓأَيُّهَا ٱلْمُزَّمِّلُ"}]`),
		ScriptImlaeiSimplePath: writeQuranFixture(t, dir, "simple.json", `{"73:1":"يا ايها المزمل"}`),
		TranslationSimplePath:  writeQuranFixture(t, dir, "translation.json", `{"73:1":"O wrapped one!"}`),
		TranslationLang:        "en-US",
		TranslationSourceID:    "qul-test-en-simple",
		SurahInfoLang:          "en",
		DryRun:                 true,
	}

	stats, err := RunQuranAssetImport(context.Background(), base)
	require.NoError(t, err)
	assert.Equal(t, 1, stats.SurahInfos)
	assert.Equal(t, 1, stats.Translations)

	assets, err := parseQuranAssets(base)
	require.NoError(t, err)
	require.Contains(t, assets.surahInfos, "73:en")

	base.TranslationLang = "fr"
	_, err = RunQuranAssetImport(context.Background(), base)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported language")
}

func TestQuranAssetOptionsTranslationSourceDefaults(t *testing.T) {
	t.Parallel()

	defaultID := QuranAssetOptions{}.withDefaults()
	assert.Equal(t, defaultQuranTranslationSourceID, defaultID.TranslationSourceID)
	assert.Equal(t, defaultQuranTranslationSourceName, defaultID.TranslationSourceName)
	assert.Equal(t, defaultQuranTranslationSourceURL, defaultID.TranslationSourceURL)
	assert.Equal(t, defaultQuranTranslationResourceID, defaultID.TranslationResourceID)
	assert.Equal(t, defaultQuranTranslationFormat, defaultID.TranslationFormat)
	assert.Equal(t, defaultQuranTranslationFootnoteFormat, defaultID.TranslationFootnoteFormat)

	customEN := QuranAssetOptions{
		TranslationLang:       "en",
		TranslationSourceID:   "qul-haleem-en-simple",
		TranslationSourceName: "M. A. S. Abdel Haleem",
	}.withDefaults()
	assert.Empty(t, customEN.TranslationSourceURL)
	assert.Empty(t, customEN.TranslationResourceID)
	assert.Equal(t, legacyQULTranslationFormat, customEN.TranslationFormat)
	assert.Equal(t, legacyQULTranslationFootnoteFormat, customEN.TranslationFootnoteFormat)

	legacyID := QuranAssetOptions{
		TranslationSourceID: legacyQULTranslationSourceID,
	}.withDefaults()
	assert.Equal(t, legacyQULTranslationSourceName, legacyID.TranslationSourceName)
	assert.Equal(t, legacyQULTranslationSourceURL, legacyID.TranslationSourceURL)
	assert.Equal(t, legacyQULTranslationResourceID, legacyID.TranslationResourceID)

	customExplicit := QuranAssetOptions{
		TranslationLang:           "en",
		TranslationSourceID:       "qul-haleem-en-simple",
		TranslationSourceURL:      "https://qul.tarteel.ai/resources/translation/131",
		TranslationResourceID:     "131",
		TranslationFormat:         "custom-simple.json",
		TranslationFootnoteFormat: "custom-footnotes.json",
	}.withDefaults()
	assert.Equal(t, "https://qul.tarteel.ai/resources/translation/131", customExplicit.TranslationSourceURL)
	assert.Equal(t, "131", customExplicit.TranslationResourceID)
	assert.Equal(t, "custom-simple.json", customExplicit.TranslationFormat)
	assert.Equal(t, "custom-footnotes.json", customExplicit.TranslationFootnoteFormat)
}

func TestQuranAssetOptionsRequireSourceIDForNonIDTranslations(t *testing.T) {
	t.Parallel()

	opts := QuranAssetOptions{
		SurahNamesPath:         "surahs.json",
		ScriptQPCHafsPath:      "qpc.json",
		ScriptImlaeiSimplePath: "simple.json",
		TranslationSimplePath:  "translation.json",
		TranslationLang:        "en",
		DryRun:                 true,
	}

	err := opts.validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "translation source id is required")
}

func TestFillAyahNavigationFromCanonicalBoundaries(t *testing.T) {
	t.Parallel()

	existingJuz := 99
	assets := quranAssetSet{
		ayahs: map[string]*quranAyahImport{
			"1:1":   {SurahID: 1, AyahNumber: 1},
			"2:75":  {SurahID: 2, AyahNumber: 75},
			"2:142": {SurahID: 2, AyahNumber: 142, JuzNumber: &existingJuz},
			"3:92":  {SurahID: 3, AyahNumber: 92},
			"3:93":  {SurahID: 3, AyahNumber: 93},
			"5:82":  {SurahID: 5, AyahNumber: 82},
			"55:1":  {SurahID: 55, AyahNumber: 1},
		},
	}

	fillAyahNavigation(&assets)

	assertNavigation(t, assets.ayahs["1:1"], 1, 1)
	assertNavigation(t, assets.ayahs["2:75"], 1, 2)
	assertNavigation(t, assets.ayahs["2:142"], 99, 3)
	assertNavigation(t, assets.ayahs["3:92"], 3, 6)
	assertNavigation(t, assets.ayahs["3:93"], 4, 7)
	assertNavigation(t, assets.ayahs["5:82"], 7, 13)
	assertNavigation(t, assets.ayahs["55:1"], 27, 54)
}

func TestResolveQuranMention(t *testing.T) {
	t.Parallel()

	aliases := map[string]surahLookup{}
	addSurahAlias(aliases, surahLookup{SurahID: 73, AyahCount: 2}, "المزمل")
	addSurahAlias(aliases, surahLookup{SurahID: 112, AyahCount: 4}, "الإخلاص")
	ayahs := []ayahLookup{
		{SurahID: 97, AyahNumber: 1, AyahKey: "97:1", Text: "إِنَّا أَنْزَلْنَاهُ فِي لَيْلَةِ الْقَدْرِ"},
	}

	resolution, ok := resolveQuranMention("سورة المزمل: 1-2", aliases, ayahs)
	require.True(t, ok)
	require.NotNil(t, resolution.SurahID)
	require.NotNil(t, resolution.FromAyahNumber)
	require.NotNil(t, resolution.ToAyahNumber)
	assert.Equal(t, 73, *resolution.SurahID)
	assert.Equal(t, 1, *resolution.FromAyahNumber)
	assert.Equal(t, 2, *resolution.ToAyahNumber)
	assert.Equal(t, "approved", resolution.ReviewStatus)

	resolution, ok = resolveQuranMention("سورة الإخلاص", aliases, ayahs)
	require.True(t, ok)
	require.NotNil(t, resolution.SurahID)
	assert.Equal(t, 112, *resolution.SurahID)
	assert.Equal(t, "surah", resolution.ReferenceKind)

	resolution, ok = resolveQuranMention("انا انزلناه في ليلة القدر", aliases, ayahs)
	require.True(t, ok)
	assert.Equal(t, "quote", resolution.ReferenceKind)
	assert.Equal(t, "needs_review", resolution.ReviewStatus)

	_, ok = resolveQuranMention("سورة المزمل: 1-99", aliases, ayahs)
	assert.False(t, ok)
}

func TestResolveQuranMentionAmbiguousQuote(t *testing.T) {
	t.Parallel()

	ayahs := []ayahLookup{
		{SurahID: 1, AyahNumber: 1, AyahKey: "1:1", Text: "الحمد لله"},
		{SurahID: 2, AyahNumber: 1, AyahKey: "2:1", Text: "الحمد لله"},
	}

	_, ok := resolveQuranMention("الحمد لله", nil, ayahs)

	assert.False(t, ok)
}

func assertNavigation(t *testing.T, ayah *quranAyahImport, juzNumber, hizbNumber int) {
	t.Helper()

	require.NotNil(t, ayah.JuzNumber)
	require.NotNil(t, ayah.HizbNumber)
	assert.Equal(t, juzNumber, *ayah.JuzNumber)
	assert.Equal(t, hizbNumber, *ayah.HizbNumber)
}

func writeQuranFixture(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func writeQuranZipFixture(t *testing.T, dir, name string, filesAndContent ...string) string {
	t.Helper()
	require.Zero(t, len(filesAndContent)%2)

	path := filepath.Join(dir, name)
	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	for i := 0; i < len(filesAndContent); i += 2 {
		writer, err := zipWriter.Create(filesAndContent[i])
		require.NoError(t, err)
		_, err = writer.Write([]byte(filesAndContent[i+1]))
		require.NoError(t, err)
	}
	require.NoError(t, zipWriter.Close())

	return path
}
