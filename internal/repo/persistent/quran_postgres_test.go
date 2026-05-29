package persistent

import (
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestMarkDefaultRecitationPrefersFullPublicAyah(t *testing.T) {
	t.Parallel()

	recitations := []entity.QuranRecitation{
		{ID: "surah-public", Name: "A", Mode: "surah", TrackCount: 114, PublicTrackCount: 114, HasPublicAudio: true},
		{ID: "ayah-partial", Name: "A", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 2, HasPublicAudio: false},
		{ID: "ayah-public", Name: "B", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 6236, HasPublicAudio: true},
	}

	markDefaultRecitation(recitations)

	assert.False(t, recitations[0].IsDefault)
	assert.False(t, recitations[1].IsDefault)
	assert.True(t, recitations[2].IsDefault)
}

func TestMarkDefaultRecitationLeavesNoDefaultWithoutFullPublicAudio(t *testing.T) {
	t.Parallel()

	recitations := []entity.QuranRecitation{
		{ID: "ayah-partial", Name: "A", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 2, HasPublicAudio: false},
		{ID: "surah-empty", Name: "B", Mode: "surah", TrackCount: 0, PublicTrackCount: 0, HasPublicAudio: false},
	}

	markDefaultRecitation(recitations)

	assert.False(t, recitations[0].IsDefault)
	assert.False(t, recitations[1].IsDefault)
}

func TestQuranAudioTrackLessPrefersAyahTrack(t *testing.T) {
	t.Parallel()

	ayahTrack := entity.QuranAudioTrack{RecitationID: "rec", TrackType: "ayah", TrackKey: "73:1"}
	surahTrack := entity.QuranAudioTrack{RecitationID: "rec", TrackType: "surah", TrackKey: "73"}

	assert.True(t, quranAudioTrackLess(ayahTrack, surahTrack))
	assert.False(t, quranAudioTrackLess(surahTrack, ayahTrack))
}
