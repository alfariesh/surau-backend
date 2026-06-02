package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReaderAvailabilityDecisions(t *testing.T) {
	t.Parallel()

	t.Run("arabic source hides translation tab", func(t *testing.T) {
		t.Parallel()

		got := NewReaderAvailability("ar", "ar", false, false, false, nil, false, false, nil, nil, nil)

		assert.Equal(t, AvailabilityActionShowArabic, got.Title.Action)
		assert.Equal(t, AvailabilityActionHideTranslation, got.Translation.Action)
		assert.False(t, got.Translation.Missing)
	})

	t.Run("exact requested assets show requested", func(t *testing.T) {
		t.Parallel()

		summaryLang := "id"
		got := NewReaderAvailability("id", "id", false, true, false, &summaryLang, true, true, []string{"id"}, []string{"id"}, []string{"id"})

		assert.Equal(t, AvailabilityActionShowRequested, got.Title.Action)
		assert.Equal(t, AvailabilityActionShowRequested, got.Translation.Action)
		assert.Equal(t, AvailabilityActionShowRequested, got.Summary.Action)
		assert.Equal(t, AvailabilityActionShowRequested, got.Audio.Action)
	})

	t.Run("missing requested with alternatives offers language switch", func(t *testing.T) {
		t.Parallel()

		got := NewReaderAvailability("en", "ar", true, false, true, nil, false, false, []string{"id"}, []string{"id"}, []string{"id"})

		assert.Equal(t, AvailabilityActionOfferLang, got.Title.Action)
		assert.Equal(t, AvailabilityActionOfferLang, got.Translation.Action)
		assert.Equal(t, AvailabilityActionOfferLang, got.Summary.Action)
		assert.Equal(t, AvailabilityActionOfferLang, got.Audio.Action)
		assert.True(t, got.Translation.Missing)
	})

	t.Run("missing requested without alternatives hides unavailable surfaces", func(t *testing.T) {
		t.Parallel()

		got := NewReaderAvailability("en", "ar", true, false, true, nil, false, false, nil, nil, nil)

		assert.Equal(t, AvailabilityActionShowArabic, got.Title.Action)
		assert.Equal(t, AvailabilityActionHideTranslation, got.Translation.Action)
		assert.Equal(t, AvailabilityActionHideTranslation, got.Summary.Action)
		assert.Equal(t, AvailabilityActionHideAudio, got.Audio.Action)
	})
}
