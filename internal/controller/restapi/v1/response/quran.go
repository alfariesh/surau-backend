package response

import "github.com/evrone/go-clean-template/internal/entity"

// QuranReaderAyah is a compact ayah payload for Quran reader list screens.
type QuranReaderAyah struct {
	SurahID     int                         `json:"surah_id" example:"73"`
	AyahNumber  int                         `json:"ayah_number" example:"4"`
	AyahKey     string                      `json:"ayah_key" example:"73:4"`
	TextQPCHafs *string                     `json:"text_qpc_hafs,omitempty"`
	JuzNumber   *int                        `json:"juz_number,omitempty" example:"29"`
	PageNumber  *int                        `json:"page_number,omitempty" example:"574"`
	Translation *QuranReaderAyahTranslation `json:"translation,omitempty"`
	Audio       []QuranReaderAyahAudioTrack `json:"audio,omitempty"`
} // @name v1.QuranReaderAyah

// QuranReaderAyahTranslation contains only reader-visible translation text.
type QuranReaderAyahTranslation struct {
	Text string `json:"text"`
} // @name v1.QuranReaderAyahTranslation

// QuranReaderAyahAudioTrack contains only playback data needed by the reader.
type QuranReaderAyahAudioTrack struct {
	RecitationID string                        `json:"recitation_id" example:"qul-recitation"`
	TrackType    string                        `json:"track_type" example:"ayah"`
	TrackKey     string                        `json:"track_key" example:"73:4"`
	URL          string                        `json:"url"`
	Segments     []QuranReaderAyahAudioSegment `json:"segments,omitempty"`
} // @name v1.QuranReaderAyahAudioTrack

// QuranReaderAyahAudioSegment is compact ayah timestamp metadata.
type QuranReaderAyahAudioSegment struct {
	SegmentIndex    int    `json:"segment_index" example:"1"`
	AyahKey         string `json:"ayah_key" example:"73:4"`
	TimestampFromMS int    `json:"timestamp_from_ms" example:"1200"`
	TimestampToMS   int    `json:"timestamp_to_ms" example:"4200"`
	DurationMS      *int   `json:"duration_ms,omitempty" example:"3000"`
} // @name v1.QuranReaderAyahAudioSegment

// QuranReaderAyahs maps domain ayahs to the compact reader payload.
func QuranReaderAyahs(ayahs []entity.QuranAyah) []QuranReaderAyah {
	items := make([]QuranReaderAyah, 0, len(ayahs))
	for _, ayah := range ayahs {
		items = append(items, QuranReaderAyahFromEntity(ayah))
	}

	return items
}

// QuranReaderAyahFromEntity maps one domain ayah to the compact reader payload.
func QuranReaderAyahFromEntity(ayah entity.QuranAyah) QuranReaderAyah {
	item := QuranReaderAyah{
		SurahID:     ayah.SurahID,
		AyahNumber:  ayah.AyahNumber,
		AyahKey:     ayah.AyahKey,
		TextQPCHafs: ayah.TextQPCHafs,
		JuzNumber:   ayah.JuzNumber,
		PageNumber:  ayah.PageNumber,
		Audio:       readerAudioTracks(ayah.Audio),
	}
	if ayah.Translation != nil {
		item.Translation = &QuranReaderAyahTranslation{Text: ayah.Translation.Text}
	}

	return item
}

func readerAudioTracks(tracks []entity.QuranAudioTrack) []QuranReaderAyahAudioTrack {
	items := make([]QuranReaderAyahAudioTrack, 0, len(tracks))
	for _, track := range tracks {
		url := readerAudioURL(track)
		if url == "" {
			continue
		}

		items = append(items, QuranReaderAyahAudioTrack{
			RecitationID: track.RecitationID,
			TrackType:    track.TrackType,
			TrackKey:     track.TrackKey,
			URL:          url,
			Segments:     readerAudioSegments(track.Segments),
		})
	}

	return items
}

func readerAudioURL(track entity.QuranAudioTrack) string {
	if track.PublicURL != nil && *track.PublicURL != "" {
		return *track.PublicURL
	}
	if track.AudioURL != nil && *track.AudioURL != "" {
		return *track.AudioURL
	}

	return ""
}

func readerAudioSegments(segments []entity.QuranAudioSegment) []QuranReaderAyahAudioSegment {
	items := make([]QuranReaderAyahAudioSegment, 0, len(segments))
	for _, segment := range segments {
		items = append(items, QuranReaderAyahAudioSegment{
			SegmentIndex:    segment.SegmentIndex,
			AyahKey:         segment.AyahKey,
			TimestampFromMS: segment.TimestampFromMS,
			TimestampToMS:   segment.TimestampToMS,
			DurationMS:      segment.DurationMS,
		})
	}

	return items
}

// QuranSearchList -.
type QuranSearchList struct {
	Results []entity.QuranSearchResult `json:"results"`
	Total   int                        `json:"total" example:"42"`
} // @name v1.QuranSearchList

// BookQuranReferenceList -.
type BookQuranReferenceList struct {
	References []entity.BookQuranReference `json:"references"`
	Total      int                         `json:"total" example:"42"`
} // @name v1.BookQuranReferenceList
