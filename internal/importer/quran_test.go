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
	assert.Equal(t, 1, stats.Recitations)
	assert.Equal(t, 1, stats.AudioTracks)
	assert.Equal(t, 1, stats.AudioSegments)
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

func writeQuranFixture(t *testing.T, dir string, name string, content string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func writeQuranZipFixture(t *testing.T, dir string, name string, filesAndContent ...string) string {
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
