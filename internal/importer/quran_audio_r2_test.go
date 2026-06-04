package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadQuranAudioR2Manifest(t *testing.T) {
	t.Parallel()

	path := writeQuranFixture(t, t.TempDir(), "manifest.jsonl", `{"r2_key":"quran/audio/recitation/ayah/001001.mp3","track_key":"1:1","track_type":"ayah","recitation_id":"recitation","audio_url":"https://source/001001.mp3"}

{"r2_key":"quran/audio/recitation/surah/001.mp3","track_key":"1","track_type":"surah","recitation_id":"recitation"}`)

	entries, stats, err := loadQuranAudioR2Manifest(path, "https://example.com/base/")

	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, 2, stats.Tracks)
	assert.Equal(t, 2, stats.PublicURLs)
	assert.Equal(t, 1, entries[0].SurahID)
	assert.Equal(t, 1, entries[0].AyahNumber)
	assert.Equal(t, "https://source/001001.mp3", entries[0].AudioURL)
	assert.Equal(t, "https://example.com/base/quran/audio/recitation/ayah/001001.mp3", entries[0].PublicURL)
	assert.Equal(t, "https://example.com/base/quran/audio/recitation/surah/001.mp3", entries[1].PublicURL)
}

func TestLoadQuranAudioR2ManifestRequiresJoinFields(t *testing.T) {
	t.Parallel()

	path := writeQuranFixture(t, t.TempDir(), "manifest.jsonl", `{"r2_key":"quran/audio/recitation/ayah/001001.mp3","track_key":"1:1","track_type":"ayah"}`)

	_, _, err := loadQuranAudioR2Manifest(path, "https://example.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing recitation_id")
}

func TestQuranAudioPublicURLWithoutBase(t *testing.T) {
	t.Parallel()

	assert.Empty(t, quranAudioPublicURL("", "quran/audio/a.mp3"))
}

func TestRunQuranAudioR2SyncDryRunCountsRecitations(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	manifestPath := writeQuranFixture(t, dir, "manifest.jsonl", `{"r2_key":"quran/audio/rec-1/ayah/001001.mp3","track_key":"1:1","track_type":"ayah","recitation_id":"rec-1"}
{"r2_key":"quran/audio/rec-2/ayah/001001.mp3","track_key":"1:1","track_type":"ayah","recitation_id":"rec-2"}`)
	metadataPath := writeQuranFixture(t, dir, "metadata.json", `[{
		"id":"rec-1",
		"display_name":"Clean Reciter",
		"reciter_name":"Clean Reciter",
		"style":"murattal",
		"mode":"ayah",
		"sort_order":10,
		"is_visible":true
	}]`)

	stats, err := RunQuranAudioR2Sync(t.Context(), QuranAudioR2SyncOptions{
		ManifestPath:           manifestPath,
		RecitationMetadataPath: metadataPath,
		PublicBaseURL:          "https://cdn.example",
		DryRun:                 true,
	})

	require.NoError(t, err)
	assert.True(t, stats.DryRun)
	assert.Equal(t, 2, stats.Recitations)
	assert.Equal(t, 2, stats.Tracks)
	assert.Equal(t, 2, stats.PublicURLs)
}

func TestLoadQuranAudioR2RecitationMetadataSupportsKeyedObject(t *testing.T) {
	t.Parallel()

	path := writeQuranFixture(t, t.TempDir(), "metadata.json", `{
		"rec-1": {"display_name":"Reciter One", "mode":"ayah", "is_visible":false}
	}`)

	items, err := loadQuranAudioR2RecitationMetadata(path)

	require.NoError(t, err)
	require.Contains(t, items, "rec-1")
	assert.Equal(t, "rec-1", items["rec-1"].ID)
	assert.Equal(t, "Reciter One", items["rec-1"].DisplayName)
	require.NotNil(t, items["rec-1"].IsVisible)
	assert.False(t, *items["rec-1"].IsVisible)
}

func TestApplyQuranAudioR2PublicURLPolicy(t *testing.T) {
	t.Parallel()

	disabled := false
	entries := []quranAudioR2ManifestEntry{
		{RecitationID: "rec-disabled", PublicURL: "https://cdn.example/a.mp3"},
		{RecitationID: "rec-enabled", PublicURL: "https://cdn.example/b.mp3"},
	}

	count := applyQuranAudioR2PublicURLPolicy(entries, map[string]quranAudioR2RecitationMetadata{
		"rec-disabled": {UsePublicURL: &disabled},
	})

	assert.Equal(t, 1, count)
	assert.Empty(t, entries[0].PublicURL)
	assert.True(t, entries[0].ClearPublicURL)
	assert.Equal(t, "https://cdn.example/b.mp3", entries[1].PublicURL)
	assert.False(t, entries[1].ClearPublicURL)
}
