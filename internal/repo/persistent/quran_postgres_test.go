package persistent

import (
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestMarkDefaultRecitationPrefersFullPublicAyah(t *testing.T) {
	t.Parallel()

	recitations := []entity.QuranRecitation{
		{ID: "surah-public", Name: "A", Mode: "surah", TrackCount: 114, PublicTrackCount: 114, PlayableTrackCount: 114, HasPublicAudio: true, HasPlayableAudio: true},
		{ID: "ayah-partial", Name: "A", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 2, PlayableTrackCount: 2, HasPublicAudio: false, HasPlayableAudio: false},
		{ID: "ayah-public", Name: "B", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 6236, PlayableTrackCount: 6236, HasPublicAudio: true, HasPlayableAudio: true},
	}

	markDefaultRecitation(recitations)

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

func TestMarkDefaultRecitationLeavesNoDefaultWithoutFullPlayableAudio(t *testing.T) {
	t.Parallel()

	recitations := []entity.QuranRecitation{
		{ID: "ayah-partial", Name: "A", Mode: "ayah", TrackCount: 6236, PublicTrackCount: 2, PlayableTrackCount: 2, HasPublicAudio: false, HasPlayableAudio: false},
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

	assert.True(t, quranAudioTrackLess(ayahTrack, surahTrack))
	assert.False(t, quranAudioTrackLess(surahTrack, ayahTrack))
}

func TestMissingManifestAyahKeys(t *testing.T) {
	t.Parallel()

	publicURL := "https://cdn.example/1.mp3"
	ayahNumber := 1
	assert.Equal(t,
		[]string{"1:2"},
		missingManifestAyahKeys([]string{"1:1", "1:2"}, "ayah", []entity.QuranAudioTrack{{
			TrackType:  "ayah",
			TrackKey:   "1:1",
			AyahNumber: &ayahNumber,
			PublicURL:  &publicURL,
		}}),
	)

	assert.Equal(t,
		[]string{"1:2"},
		missingManifestAyahKeys([]string{"1:1", "1:2"}, "surah", []entity.QuranAudioTrack{{
			TrackType: "surah",
			TrackKey:  "1",
			PublicURL: &publicURL,
			Segments:  []entity.QuranAudioSegment{{AyahKey: "1:1"}},
		}}),
	)
}

func TestQuranNavigationColumnAllowlist(t *testing.T) {
	t.Parallel()

	column, err := quranNavigationColumn("juz")
	assert.NoError(t, err)
	assert.Equal(t, "juz_number", column)

	column, err = quranNavigationColumn("hizb")
	assert.NoError(t, err)
	assert.Equal(t, "hizb_number", column)

	_, err = quranNavigationColumn("page")
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
