package entity

import "time"

// QuranSurah describes one Quran surah plus imported QUL metadata.
type QuranSurah struct {
	SurahID         int             `json:"surah_id" example:"73"`
	NameArabic      *string         `json:"name_arabic,omitempty" example:"المزمل"`
	NameLatin       *string         `json:"name_latin,omitempty" example:"Al-Muzzammil"`
	NameTranslation *string         `json:"name_translation,omitempty" example:"Orang yang Berselimut"`
	RevelationType  *string         `json:"revelation_type,omitempty" example:"makkiyah"`
	AyahCount       int             `json:"ayah_count" example:"20"`
	Info            *QuranSurahInfo `json:"info,omitempty"`
	Metadata        RawJSON         `json:"metadata,omitempty" swaggertype:"object"`
	UpdatedAt       time.Time       `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranSurah

// QuranSurahInfo stores language-specific imported background information for one surah.
type QuranSurahInfo struct {
	Lang          string     `json:"lang" example:"id"`
	SurahName     *string    `json:"surah_name,omitempty" example:"Al-Fatihah"`
	TextHTML      string     `json:"text_html"`
	ShortText     *string    `json:"short_text,omitempty"`
	SourceName    string     `json:"source_name" example:"QUL Surah information"`
	SourceURL     *string    `json:"source_url,omitempty"`
	QULResourceID *string    `json:"qul_resource_id,omitempty"`
	Format        string     `json:"format" example:"json"`
	LicenseStatus string     `json:"license_status" example:"needs_review"`
	Checksum      *string    `json:"checksum,omitempty"`
	Metadata      RawJSON    `json:"metadata,omitempty" swaggertype:"object"`
	ImportedAt    *time.Time `json:"imported_at,omitempty" example:"2026-01-01T00:00:00Z"`
	UpdatedAt     time.Time  `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranSurahInfo

// QuranTranslation is one ayah translation from an imported QUL source.
type QuranTranslation struct {
	SourceID  string    `json:"source_id" example:"qul-kfgqpc-id-simple"`
	Lang      string    `json:"lang" example:"id"`
	Text      string    `json:"text"`
	Footnotes RawJSON   `json:"footnotes,omitempty" swaggertype:"object"`
	Chunks    RawJSON   `json:"chunks,omitempty" swaggertype:"object"`
	Metadata  RawJSON   `json:"metadata,omitempty" swaggertype:"object"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranTranslation

// QuranAudioSegment is an ayah-level timestamp range for one audio track.
type QuranAudioSegment struct {
	SegmentIndex    int     `json:"segment_index" example:"1"`
	AyahKey         string  `json:"ayah_key" example:"73:4"`
	TimestampFromMS int     `json:"timestamp_from_ms" example:"1200"`
	TimestampToMS   int     `json:"timestamp_to_ms" example:"4200"`
	DurationMS      *int    `json:"duration_ms,omitempty" example:"3000"`
	Metadata        RawJSON `json:"metadata,omitempty" swaggertype:"object"`
} // @name entity.QuranAudioSegment

// QuranRecitation describes one imported reciter/resource and its audio coverage.
type QuranRecitation struct {
	ID               string     `json:"id" example:"qul-ayah-recitation-mishari-rashid-al-afasy-murattal-hafs-953"`
	Name             string     `json:"name" example:"QUL ayah recitation mishari rashid al afasy murattal hafs 953"`
	ReciterName      *string    `json:"reciter_name,omitempty" example:"Mishari Rashid Al-Afasy"`
	Style            *string    `json:"style,omitempty" example:"murattal"`
	Mode             string     `json:"mode" example:"ayah"`
	SourceURL        *string    `json:"source_url,omitempty"`
	QULResourceID    *string    `json:"qul_resource_id,omitempty" example:"953"`
	Format           string     `json:"format" example:"json"`
	LicenseStatus    string     `json:"license_status" example:"needs_review"`
	Checksum         *string    `json:"checksum,omitempty"`
	TrackCount       int        `json:"track_count" example:"6236"`
	PublicTrackCount int        `json:"public_track_count" example:"0"`
	HasPublicAudio   bool       `json:"has_public_audio" example:"false"`
	IsDefault        bool       `json:"is_default" example:"false"`
	Metadata         RawJSON    `json:"metadata,omitempty" swaggertype:"object"`
	ImportedAt       *time.Time `json:"imported_at,omitempty" example:"2026-01-01T00:00:00Z"`
	UpdatedAt        time.Time  `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranRecitation

// QuranAudioTrack stores recitation track metadata. R2 fields stay nullable until ingestion.
type QuranAudioTrack struct {
	RecitationID    string              `json:"recitation_id" example:"qul-recitation"`
	TrackType       string              `json:"track_type" example:"ayah"`
	TrackKey        string              `json:"track_key" example:"73:4"`
	SurahID         int                 `json:"surah_id" example:"73"`
	AyahNumber      *int                `json:"ayah_number,omitempty" example:"4"`
	AudioURL        *string             `json:"audio_url,omitempty"`
	R2Key           *string             `json:"r2_key,omitempty"`
	PublicURL       *string             `json:"public_url,omitempty"`
	DurationMS      *int                `json:"duration_ms,omitempty"`
	DurationSeconds *int                `json:"duration_seconds,omitempty"`
	MIMEType        *string             `json:"mime_type,omitempty" example:"audio/mpeg"`
	Segments        []QuranAudioSegment `json:"segments,omitempty"`
	Metadata        RawJSON             `json:"metadata,omitempty" swaggertype:"object"`
	UpdatedAt       time.Time           `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranAudioTrack

// QuranAyah is one canonical ayah row with optional translation and audio metadata.
type QuranAyah struct {
	SurahID          int               `json:"surah_id" example:"73"`
	AyahNumber       int               `json:"ayah_number" example:"4"`
	AyahKey          string            `json:"ayah_key" example:"73:4"`
	TextQPCHafs      *string           `json:"text_qpc_hafs,omitempty"`
	TextImlaeiSimple *string           `json:"text_imlaei_simple,omitempty"`
	SearchText       *string           `json:"search_text,omitempty"`
	ScriptType       *string           `json:"script_type,omitempty"`
	FontFamily       *string           `json:"font_family,omitempty"`
	PageNumber       *int              `json:"page_number,omitempty"`
	JuzNumber        *int              `json:"juz_number,omitempty"`
	HizbNumber       *int              `json:"hizb_number,omitempty"`
	Translation      *QuranTranslation `json:"translation,omitempty"`
	Audio            []QuranAudioTrack `json:"audio,omitempty"`
	Metadata         RawJSON           `json:"metadata,omitempty" swaggertype:"object"`
	UpdatedAt        time.Time         `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranAyah

// QuranSearchResult is one ranked Quran search hit.
type QuranSearchResult struct {
	Ayah  QuranAyah `json:"ayah"`
	Score float64   `json:"score" example:"0.82"`
} // @name entity.QuranSearchResult

// BookQuranReference links a kitab location to a Quran surah or ayah range.
type BookQuranReference struct {
	ID                 string      `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	BookID             int         `json:"book_id" example:"797"`
	PageID             int         `json:"page_id" example:"12"`
	HeadingID          *int        `json:"heading_id,omitempty" example:"10"`
	KnowledgeMentionID *string     `json:"knowledge_mention_id,omitempty"`
	SourceText         string      `json:"source_text"`
	NormalizedText     string      `json:"normalized_text"`
	ReferenceKind      string      `json:"reference_kind" example:"surah_ayah"`
	SurahID            *int        `json:"surah_id,omitempty" example:"73"`
	FromAyahNumber     *int        `json:"from_ayah_number,omitempty" example:"4"`
	ToAyahNumber       *int        `json:"to_ayah_number,omitempty" example:"4"`
	FromAyahKey        *string     `json:"from_ayah_key,omitempty" example:"73:4"`
	ToAyahKey          *string     `json:"to_ayah_key,omitempty" example:"73:4"`
	MatchStrategy      string      `json:"match_strategy" example:"explicit_surah_ayah"`
	Confidence         *float64    `json:"confidence,omitempty" example:"1"`
	ReviewStatus       string      `json:"review_status" example:"approved"`
	Ayahs              []QuranAyah `json:"ayahs,omitempty"`
	Metadata           RawJSON     `json:"metadata,omitempty" swaggertype:"object"`
	CreatedAt          time.Time   `json:"created_at" example:"2026-01-01T00:00:00Z"`
	UpdatedAt          time.Time   `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookQuranReference
