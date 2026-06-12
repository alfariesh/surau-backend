package response

import "github.com/evrone/go-clean-template/internal/entity"

// QuranReaderAyah is a compact ayah payload for Quran reader list screens.
type QuranReaderAyah struct {
	SurahID         int                             `json:"surah_id" example:"73"`
	AyahNumber      int                             `json:"ayah_number" example:"4"`
	AyahKey         string                          `json:"ayah_key" example:"73:4"`
	TextQPCHafs     *string                         `json:"text_qpc_hafs,omitempty"`
	JuzNumber       *int                            `json:"juz_number,omitempty" example:"29"`
	PageNumber      *int                            `json:"page_number,omitempty" example:"574"`
	Translation     *QuranReaderAyahTranslation     `json:"translation,omitempty"`
	Transliteration *QuranReaderAyahTransliteration `json:"transliteration,omitempty"`
	Audio           []QuranReaderAyahAudioTrack     `json:"audio,omitempty"`
} // @name v1.QuranReaderAyah

// QuranReaderAyahTranslation contains only reader-visible translation text.
type QuranReaderAyahTranslation struct {
	Text string `json:"text"`
} // @name v1.QuranReaderAyahTranslation

// QuranReaderAyahTransliteration contains reader-visible transliteration text.
type QuranReaderAyahTransliteration struct {
	Text string `json:"text"`
} // @name v1.QuranReaderAyahTransliteration

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

// QuranSurahAudioManifest is the compact surah audio payload for FE players.
type QuranSurahAudioManifest struct {
	SurahID         int                       `json:"surah_id" example:"1"`
	Recitation      QuranSurahAudioRecitation `json:"recitation"`
	Mode            string                    `json:"mode" example:"ayah"`
	Tracks          []QuranSurahAudioTrack    `json:"tracks"`
	MissingAyahKeys []string                  `json:"missing_ayah_keys"`
} // @name v1.QuranSurahAudioManifest

// QuranSurahAudioRecitation summarizes the selected recitation for one manifest.
type QuranSurahAudioRecitation struct {
	ID               string  `json:"id" example:"qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953"`
	DisplayName      string  `json:"display_name" example:"Mishari Rashid Al-Afasy"`
	ReciterName      *string `json:"reciter_name,omitempty" example:"Mishari Rashid Al-Afasy"`
	Style            *string `json:"style,omitempty" example:"murattal"`
	Mode             string  `json:"mode" example:"ayah"`
	IsDefault        bool    `json:"is_default" example:"true"`
	TrackCount       int     `json:"track_count" example:"6236"`
	PublicTrackCount int     `json:"public_track_count" example:"6236"`
	SegmentCount     int     `json:"segment_count" example:"77796"`
} // @name v1.QuranSurahAudioRecitation

// QuranSurahAudioTrack is a playable audio item in a surah manifest.
type QuranSurahAudioTrack struct {
	RecitationID    string                        `json:"recitation_id" example:"qul-recitation"`
	TrackType       string                        `json:"track_type" example:"ayah"`
	TrackKey        string                        `json:"track_key" example:"73:4"`
	SurahID         int                           `json:"surah_id" example:"73"`
	AyahNumber      *int                          `json:"ayah_number,omitempty" example:"4"`
	URL             string                        `json:"url"`
	DurationMS      *int                          `json:"duration_ms,omitempty" example:"3000"`
	DurationSeconds *int                          `json:"duration_seconds,omitempty" example:"3"`
	MIMEType        *string                       `json:"mime_type,omitempty" example:"audio/mpeg"`
	Segments        []QuranReaderAyahAudioSegment `json:"segments,omitempty"`
} // @name v1.QuranSurahAudioTrack

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
	if ayah.Transliteration != nil {
		item.Transliteration = &QuranReaderAyahTransliteration{Text: ayah.Transliteration.Text}
	}

	return item
}

// QuranSurahAudioManifestFromEntity maps a domain manifest to the compact API payload.
func QuranSurahAudioManifestFromEntity(manifest *entity.QuranSurahAudioManifest) QuranSurahAudioManifest {
	if manifest == nil {
		return QuranSurahAudioManifest{}
	}

	return QuranSurahAudioManifest{
		SurahID: manifest.SurahID,
		Recitation: QuranSurahAudioRecitation{
			ID:               manifest.Recitation.ID,
			DisplayName:      manifest.Recitation.DisplayName,
			ReciterName:      manifest.Recitation.ReciterName,
			Style:            manifest.Recitation.Style,
			Mode:             manifest.Recitation.Mode,
			IsDefault:        manifest.Recitation.IsDefault,
			TrackCount:       manifest.Recitation.TrackCount,
			PublicTrackCount: manifest.Recitation.PublicTrackCount,
			SegmentCount:     manifest.Recitation.SegmentCount,
		},
		Mode:            manifest.Mode,
		Tracks:          surahAudioTracks(manifest.Tracks),
		MissingAyahKeys: manifest.MissingAyahKeys,
	}
}

func readerAudioTracks(tracks []entity.QuranAudioTrack) []QuranReaderAyahAudioTrack {
	items := make([]QuranReaderAyahAudioTrack, 0, len(tracks))
	for i := range tracks {
		track := &tracks[i]
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

func surahAudioTracks(tracks []entity.QuranAudioTrack) []QuranSurahAudioTrack {
	items := make([]QuranSurahAudioTrack, 0, len(tracks))
	for i := range tracks {
		track := &tracks[i]

		url := readerAudioURL(track)
		if url == "" {
			continue
		}

		items = append(items, QuranSurahAudioTrack{
			RecitationID:    track.RecitationID,
			TrackType:       track.TrackType,
			TrackKey:        track.TrackKey,
			SurahID:         track.SurahID,
			AyahNumber:      track.AyahNumber,
			URL:             url,
			DurationMS:      track.DurationMS,
			DurationSeconds: track.DurationSeconds,
			MIMEType:        track.MIMEType,
			Segments:        readerAudioSegments(track.Segments),
		})
	}

	return items
}

func readerAudioURL(track *entity.QuranAudioTrack) string {
	if track == nil {
		return ""
	}

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
	Items []entity.QuranSearchResult `json:"items"`
	Total int                        `json:"total" example:"42"`
} // @name v1.QuranSearchList

// BookQuranReferenceList -.
type BookQuranReferenceList struct {
	Items []entity.BookQuranReference `json:"items"`
	Total int                         `json:"total" example:"42"`
} // @name v1.BookQuranReferenceList

// QuranSurahList -.
type QuranSurahList struct {
	Items []entity.QuranSurah `json:"items"`
	Total int                 `json:"total" example:"114"`
} // @name v1.QuranSurahList

// QuranRecitationList -.
type QuranRecitationList struct {
	Items []entity.QuranRecitation `json:"items"`
	Total int                      `json:"total" example:"3"`
} // @name v1.QuranRecitationList

// QuranTranslationSourceList -.
type QuranTranslationSourceList struct {
	Items []entity.QuranTranslationSource `json:"items"`
	Total int                             `json:"total" example:"2"`
} // @name v1.QuranTranslationSourceList

// QuranNavigationSegmentList holds juz or hizb divisions.
type QuranNavigationSegmentList struct {
	Items []entity.QuranNavigationSegment `json:"items"`
	Total int                             `json:"total" example:"30"`
} // @name v1.QuranNavigationSegmentList

// QuranAyahList -.
type QuranAyahList struct {
	Items []entity.QuranAyah `json:"items"`
	Total int                `json:"total" example:"7"`
} // @name v1.QuranAyahList

// QuranReaderAyahList -.
type QuranReaderAyahList struct {
	Items []QuranReaderAyah `json:"items"`
	Total int               `json:"total" example:"7"`
} // @name v1.QuranReaderAyahList
