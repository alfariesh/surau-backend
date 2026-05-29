package importer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadQuranAudioR2Manifest(t *testing.T) {
	path := writeQuranFixture(t, t.TempDir(), "manifest.jsonl", `{"r2_key":"quran/audio/recitation/ayah/001001.mp3","track_key":"1:1","track_type":"ayah","recitation_id":"recitation"}

{"r2_key":"quran/audio/recitation/surah/001.mp3","track_key":"1","track_type":"surah","recitation_id":"recitation"}`)

	entries, stats, err := loadQuranAudioR2Manifest(path, "https://example.com/base/")

	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, 2, stats.Tracks)
	assert.Equal(t, 2, stats.PublicURLs)
	assert.Equal(t, "https://example.com/base/quran/audio/recitation/ayah/001001.mp3", entries[0].PublicURL)
	assert.Equal(t, "https://example.com/base/quran/audio/recitation/surah/001.mp3", entries[1].PublicURL)
}

func TestLoadQuranAudioR2ManifestRequiresJoinFields(t *testing.T) {
	path := writeQuranFixture(t, t.TempDir(), "manifest.jsonl", `{"r2_key":"quran/audio/recitation/ayah/001001.mp3","track_key":"1:1","track_type":"ayah"}`)

	_, _, err := loadQuranAudioR2Manifest(path, "https://example.com")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing recitation_id")
}

func TestQuranAudioPublicURLWithoutBase(t *testing.T) {
	assert.Empty(t, quranAudioPublicURL("", "quran/audio/a.mp3"))
}
