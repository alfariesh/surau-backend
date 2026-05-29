package entity

import (
	"encoding/json"
	"time"
)

// LocalizationMeta describes which language was requested and which language is displayed.
type LocalizationMeta struct {
	RequestedLang  string               `json:"requested_lang"  example:"en"`
	DisplayLang    string               `json:"display_lang"    example:"ar"`
	IsFallback     bool                 `json:"is_fallback"     example:"true"`
	AvailableLangs []string             `json:"available_langs" example:"id"`
	FieldLangs     map[string]string    `json:"field_langs"     swaggertype:"object"`
	Availability   AvailabilityDecision `json:"availability"`
} // @name entity.LocalizationMeta

// LanguageCoverage summarizes available per-language reader assets for one book.
type LanguageCoverage struct {
	Lang               string `json:"lang"                example:"id"`
	TranslatedSections int    `json:"translated_sections" example:"120"`
	SummarizedSections int    `json:"summarized_sections" example:"80"`
	AudioSections      int    `json:"audio_sections"      example:"40"`
} // @name entity.LanguageCoverage

// Category groups books by subject.
type Category struct {
	ID                    int              `json:"id"            example:"10"`
	Name                  string           `json:"name"          example:"علوم الحديث"`
	DisplayOrder          *int             `json:"display_order" example:"10"`
	TranslationStatus     *string          `json:"translation_status,omitempty" example:"generated"`
	TranslationReviewedBy *string          `json:"translation_reviewed_by,omitempty" example:"Editor A"`
	TranslationReviewedAt *time.Time       `json:"translation_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	Localization          LocalizationMeta `json:"localization"`
	IsDeleted             bool             `json:"is_deleted"    example:"false"`
	UpdatedAt             time.Time        `json:"updated_at"    example:"2026-01-01T00:00:00Z"`
} // @name entity.Category

// Author describes a classical book author.
type Author struct {
	ID                    int              `json:"id"           example:"177"`
	Name                  string           `json:"name"         example:"فضل الرحمن صافي"`
	Biography             *string          `json:"biography"`
	DeathText             *string          `json:"death_text"   example:"1442"`
	DeathNumber           *int             `json:"death_number" example:"1442"`
	TranslationStatus     *string          `json:"translation_status,omitempty" example:"generated"`
	TranslationReviewedBy *string          `json:"translation_reviewed_by,omitempty" example:"Editor A"`
	TranslationReviewedAt *time.Time       `json:"translation_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	Localization          LocalizationMeta `json:"localization"`
	IsDeleted             bool             `json:"is_deleted"   example:"false"`
	UpdatedAt             time.Time        `json:"updated_at"   example:"2026-01-01T00:00:00Z"`
} // @name entity.Author

// Book is searchable catalog metadata plus source metadata.
type Book struct {
	ID                    int                `json:"id"             example:"797"`
	Name                  string             `json:"name"           example:"الزبد في مصطلح الحديث"`
	CategoryID            *int               `json:"category_id"    example:"10"`
	CategoryName          *string            `json:"category_name"  example:"علوم الحديث"`
	AuthorID              *int               `json:"author_id"      example:"177"`
	AuthorName            *string            `json:"author_name"    example:"فضل الرحمن صافي"`
	Type                  *int               `json:"type"           example:"1"`
	Printed               *int               `json:"printed"        example:"1"`
	MinorRelease          *int               `json:"minor_release"  example:"0"`
	MajorRelease          *int               `json:"major_release"  example:"1"`
	Bibliography          *string            `json:"bibliography"`
	Hint                  *string            `json:"hint"`
	PDFLinks              RawJSON            `json:"pdf_links,omitempty" swaggertype:"object"`
	Metadata              RawJSON            `json:"metadata,omitempty"  swaggertype:"object"`
	SourceDate            *string            `json:"source_date"     example:"02091443"`
	Description           *string            `json:"description"`
	CoverURL              *string            `json:"cover_url"`
	EditorialNotes        *string            `json:"editorial_notes"`
	TranslationStatus     *string            `json:"translation_status,omitempty" example:"generated"`
	TranslationReviewedBy *string            `json:"translation_reviewed_by,omitempty" example:"Editor A"`
	TranslationReviewedAt *time.Time         `json:"translation_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	Localization          LocalizationMeta   `json:"localization"`
	LanguageCoverage      []LanguageCoverage `json:"language_coverage,omitempty"`
	PublicationStatus     *string            `json:"publication_status" example:"published"`
	Featured              bool               `json:"featured"           example:"false"`
	SortOrder             *int               `json:"sort_order"         example:"10"`
	HasContent            bool               `json:"has_content"        example:"true"`
	IsDeleted             bool               `json:"is_deleted"         example:"false"`
	UpdatedAt             time.Time          `json:"updated_at"         example:"2026-01-01T00:00:00Z"`
}

// RawJSON is used for metadata stored as jsonb.
type RawJSON []byte

// MarshalJSON returns the raw JSON value instead of base64-encoding the bytes.
func (r RawJSON) MarshalJSON() ([]byte, error) {
	if len(r) == 0 {
		return []byte("null"), nil
	}

	if !json.Valid(r) {
		return json.Marshal(string(r))
	}

	return r, nil
}

// BookPage is one raw page row from a book DB.
type BookPage struct {
	BookID      int       `json:"book_id"      example:"797"`
	PageID      int       `json:"page_id"      example:"1"`
	Part        *string   `json:"part"         example:"1"`
	PrintedPage *string   `json:"printed_page" example:"3"`
	Number      *string   `json:"number"       example:"42"`
	ContentHTML string    `json:"content_html"`
	ContentText string    `json:"content_text"`
	Services    RawJSON   `json:"services,omitempty" swaggertype:"object"`
	IsDeleted   bool      `json:"is_deleted"   example:"false"`
	UpdatedAt   time.Time `json:"updated_at"   example:"2026-01-01T00:00:00Z"`
} // @name entity.BookPage

// BookHeading is one title/tree row from a book DB.
type BookHeading struct {
	BookID    int       `json:"book_id"    example:"797"`
	HeadingID int       `json:"heading_id" example:"10"`
	ParentID  *int      `json:"parent_id"  example:"1"`
	PageID    int       `json:"page_id"    example:"12"`
	Depth     int       `json:"depth"      example:"0"`
	Ordinal   int       `json:"ordinal"    example:"9"`
	Content   string    `json:"content"    example:"النوع الأول: الصحيح"`
	IsDeleted bool      `json:"is_deleted" example:"false"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookHeading

// BookTOCEntry is one flat TOC row with optional requested-language asset metadata.
type BookTOCEntry struct {
	BookID                    int                `json:"book_id"         example:"797"`
	HeadingID                 int                `json:"heading_id"      example:"10"`
	ParentID                  *int               `json:"parent_id"       example:"1"`
	PageID                    int                `json:"page_id"         example:"12"`
	Depth                     int                `json:"depth"           example:"0"`
	Ordinal                   int                `json:"ordinal"         example:"9"`
	Title                     string             `json:"title"           example:"النوع الأول: الصحيح"`
	RequestedLang             string             `json:"requested_lang"  example:"en"`
	TitleLang                 string             `json:"title_lang"      example:"ar"`
	IsTitleFallback           bool               `json:"is_title_fallback" example:"true"`
	Summary                   *string            `json:"summary,omitempty"`
	SummaryLang               *string            `json:"summary_lang,omitempty" example:"id"`
	HasSummary                bool               `json:"has_summary"     example:"true"`
	SummaryStatus             *string            `json:"summary_status,omitempty" example:"generated"`
	SummaryReviewedBy         *string            `json:"summary_reviewed_by,omitempty" example:"Editor A"`
	SummaryReviewedAt         *time.Time         `json:"summary_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	HasAudio                  bool               `json:"has_audio"       example:"true"`
	HasTranslation            bool               `json:"has_translation" example:"true"`
	TranslationMissing        bool               `json:"translation_missing" example:"false"`
	AvailableTranslationLangs []string           `json:"available_translation_langs" example:"id"`
	AvailableSummaryLangs     []string           `json:"available_summary_langs" example:"id"`
	TranslationStatus         *string            `json:"translation_status,omitempty" example:"generated"`
	TranslationReviewedBy     *string            `json:"translation_reviewed_by,omitempty" example:"Editor A"`
	TranslationReviewedAt     *time.Time         `json:"translation_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	Audio                     *SectionAudio      `json:"audio,omitempty"`
	Availability              ReaderAvailability `json:"availability"`
} // @name entity.BookTOCEntry

// BookTOCNode is a nested TOC response node.
type BookTOCNode struct {
	BookID                    int                `json:"book_id"         example:"797"`
	HeadingID                 int                `json:"heading_id"      example:"10"`
	ParentID                  *int               `json:"parent_id"       example:"1"`
	PageID                    int                `json:"page_id"         example:"12"`
	Depth                     int                `json:"depth"           example:"0"`
	Ordinal                   int                `json:"ordinal"         example:"9"`
	Title                     string             `json:"title"           example:"النوع الأول: الصحيح"`
	RequestedLang             string             `json:"requested_lang"  example:"en"`
	TitleLang                 string             `json:"title_lang"      example:"ar"`
	IsTitleFallback           bool               `json:"is_title_fallback" example:"true"`
	Summary                   *string            `json:"summary,omitempty"`
	SummaryLang               *string            `json:"summary_lang,omitempty" example:"id"`
	HasSummary                bool               `json:"has_summary"     example:"true"`
	SummaryStatus             *string            `json:"summary_status,omitempty" example:"generated"`
	SummaryReviewedBy         *string            `json:"summary_reviewed_by,omitempty" example:"Editor A"`
	SummaryReviewedAt         *time.Time         `json:"summary_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	HasAudio                  bool               `json:"has_audio"       example:"true"`
	HasTranslation            bool               `json:"has_translation" example:"true"`
	TranslationMissing        bool               `json:"translation_missing" example:"false"`
	AvailableTranslationLangs []string           `json:"available_translation_langs" example:"id"`
	AvailableSummaryLangs     []string           `json:"available_summary_langs" example:"id"`
	TranslationStatus         *string            `json:"translation_status,omitempty" example:"generated"`
	TranslationReviewedBy     *string            `json:"translation_reviewed_by,omitempty" example:"Editor A"`
	TranslationReviewedAt     *time.Time         `json:"translation_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	Audio                     *SectionAudio      `json:"audio,omitempty"`
	Availability              ReaderAvailability `json:"availability"`
	Children                  []BookTOCNode      `json:"children"`
} // @name entity.BookTOCNode

// BookTOCLink is a compact TOC pointer for navigation context.
type BookTOCLink struct {
	HeadingID                 int                `json:"heading_id"      example:"10"`
	Title                     string             `json:"title"           example:"النوع الأول: الصحيح"`
	RequestedLang             string             `json:"requested_lang"  example:"en"`
	TitleLang                 string             `json:"title_lang"      example:"ar"`
	IsTitleFallback           bool               `json:"is_title_fallback" example:"true"`
	ParentID                  *int               `json:"parent_id"       example:"1"`
	PageID                    int                `json:"page_id"         example:"12"`
	Depth                     int                `json:"depth"           example:"0"`
	Ordinal                   int                `json:"ordinal"         example:"9"`
	Summary                   *string            `json:"summary,omitempty"`
	SummaryLang               *string            `json:"summary_lang,omitempty" example:"id"`
	HasSummary                bool               `json:"has_summary"     example:"true"`
	SummaryStatus             *string            `json:"summary_status,omitempty" example:"generated"`
	SummaryReviewedBy         *string            `json:"summary_reviewed_by,omitempty" example:"Editor A"`
	SummaryReviewedAt         *time.Time         `json:"summary_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	HasAudio                  bool               `json:"has_audio"       example:"true"`
	HasTranslation            bool               `json:"has_translation" example:"true"`
	TranslationMissing        bool               `json:"translation_missing" example:"false"`
	AvailableTranslationLangs []string           `json:"available_translation_langs" example:"id"`
	AvailableSummaryLangs     []string           `json:"available_summary_langs" example:"id"`
	TranslationStatus         *string            `json:"translation_status,omitempty" example:"generated"`
	TranslationReviewedBy     *string            `json:"translation_reviewed_by,omitempty" example:"Editor A"`
	TranslationReviewedAt     *time.Time         `json:"translation_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	Availability              ReaderAvailability `json:"availability"`
} // @name entity.BookTOCLink

// SectionTranslation is optional translated content for a heading section.
type SectionTranslation struct {
	BookID     int        `json:"book_id"    example:"797"`
	HeadingID  int        `json:"heading_id" example:"10"`
	Lang       string     `json:"lang"       example:"id"`
	Title      *string    `json:"title"`
	Content    string     `json:"content"`
	Source     *string    `json:"source"     example:"manual"`
	Status     string     `json:"translation_status" example:"generated"`
	ReviewedBy *string    `json:"translation_reviewed_by,omitempty" example:"Editor A"`
	ReviewedAt *time.Time `json:"translation_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	Metadata   RawJSON    `json:"metadata,omitempty" swaggertype:"object"`
	UpdatedAt  time.Time  `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.SectionTranslation

// TranslationFeedback stores a reader signal for generated/reviewed translation quality.
type TranslationFeedback struct {
	ID        string    `json:"id"         example:"550e8400-e29b-41d4-a716-446655440000"`
	BookID    int       `json:"book_id"    example:"797"`
	HeadingID int       `json:"heading_id" example:"10"`
	Lang      string    `json:"lang"       example:"id"`
	UserID    *string   `json:"user_id,omitempty"`
	ClientID  *string   `json:"client_id,omitempty"`
	Vote      string    `json:"vote"       example:"dislike"`
	Reason    *string   `json:"reason,omitempty" example:"style"`
	Note      *string   `json:"note,omitempty"`
	UserAgent *string   `json:"user_agent,omitempty"`
	ClientIP  *string   `json:"client_ip,omitempty"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.TranslationFeedback

// SectionAudio is optional audiobook metadata for a heading section.
type SectionAudio struct {
	BookID          int       `json:"book_id"          example:"797"`
	HeadingID       int       `json:"heading_id"       example:"10"`
	Lang            string    `json:"lang"             example:"id"`
	URL             string    `json:"url"              example:"https://cdn.example/audio.mp3"`
	Narrator        *string   `json:"narrator"`
	DurationSeconds *int      `json:"duration_seconds" example:"120"`
	MIMEType        *string   `json:"mime_type"        example:"audio/mpeg"`
	Metadata        RawJSON   `json:"metadata,omitempty" swaggertype:"object"`
	UpdatedAt       time.Time `json:"updated_at"       example:"2026-01-01T00:00:00Z"`
} // @name entity.SectionAudio

// BookSection is the reader response for one heading.
type BookSection struct {
	BookID                    int                 `json:"book_id"       example:"797"`
	HeadingID                 int                 `json:"heading_id"    example:"10"`
	Heading                   BookHeading         `json:"heading"`
	RequestedLang             string              `json:"requested_lang" example:"en"`
	TitleLang                 string              `json:"title_lang"     example:"ar"`
	IsTitleFallback           bool                `json:"is_title_fallback" example:"true"`
	TranslationMissing        bool                `json:"translation_missing" example:"false"`
	AvailableTranslationLangs []string            `json:"available_translation_langs" example:"id"`
	AvailableSummaryLangs     []string            `json:"available_summary_langs" example:"id"`
	StartPageID               int                 `json:"start_page_id" example:"12"`
	EndPageID                 int                 `json:"end_page_id"   example:"15"`
	OriginalHTML              string              `json:"original_html"`
	OriginalText              string              `json:"original_text"`
	Translation               *SectionTranslation `json:"translation"`
	Audio                     *SectionAudio       `json:"audio"`
	Availability              ReaderAvailability  `json:"availability"`
} // @name entity.BookSection

// BookTOCRead is an article-like section response with TOC navigation context.
type BookTOCRead struct {
	BookID                    int                 `json:"book_id"       example:"797"`
	HeadingID                 int                 `json:"heading_id"    example:"10"`
	Title                     string              `json:"title"         example:"باب النية"`
	RequestedLang             string              `json:"requested_lang" example:"en"`
	TitleLang                 string              `json:"title_lang"     example:"ar"`
	IsTitleFallback           bool                `json:"is_title_fallback" example:"true"`
	Summary                   *string             `json:"summary,omitempty"`
	SummaryLang               *string             `json:"summary_lang,omitempty" example:"id"`
	HasSummary                bool                `json:"has_summary"   example:"true"`
	TranslationMissing        bool                `json:"translation_missing" example:"false"`
	AvailableTranslationLangs []string            `json:"available_translation_langs" example:"id"`
	AvailableSummaryLangs     []string            `json:"available_summary_langs" example:"id"`
	Breadcrumb                []BookTOCLink       `json:"breadcrumb"`
	Children                  []BookTOCLink       `json:"children"`
	Previous                  *BookTOCLink        `json:"previous"`
	Next                      *BookTOCLink        `json:"next"`
	StartPageID               int                 `json:"start_page_id" example:"12"`
	EndPageID                 int                 `json:"end_page_id"   example:"15"`
	OriginalHTML              string              `json:"original_html"`
	OriginalText              string              `json:"original_text"`
	Translation               *SectionTranslation `json:"translation"`
	Audio                     *SectionAudio       `json:"audio"`
	Availability              ReaderAvailability  `json:"availability"`
} // @name entity.BookTOCRead

// BookTOCPlaylist is a continuous audiobook manifest for one TOC subtree.
type BookTOCPlaylist struct {
	BookID               int                   `json:"book_id"                example:"797"`
	HeadingID            int                   `json:"heading_id"             example:"10"`
	Lang                 string                `json:"lang"                   example:"id"`
	Items                []BookTOCPlaylistItem `json:"items"`
	TotalDurationSeconds int                   `json:"total_duration_seconds" example:"320"`
	MissingCount         int                   `json:"missing_count"          example:"0"`
} // @name entity.BookTOCPlaylist

// BookTOCPlaylistItem is one playable audio item in a TOC playlist.
type BookTOCPlaylistItem struct {
	HeadingID       int     `json:"heading_id"       example:"10"`
	Title           string  `json:"title"            example:"باب النية"`
	URL             string  `json:"url"              example:"https://cdn.example/audio.mp3"`
	DurationSeconds *int    `json:"duration_seconds" example:"320"`
	Narrator        *string `json:"narrator"`
	MIMEType        *string `json:"mime_type"         example:"audio/mpeg"`
} // @name entity.BookTOCPlaylistItem

// ReadingProgress stores a user's last reader location for a book.
type ReadingProgress struct {
	UserID          string    `json:"user_id"          example:"550e8400-e29b-41d4-a716-446655440000"`
	BookID          int       `json:"book_id"          example:"797"`
	PageID          *int      `json:"page_id"          example:"12"`
	HeadingID       *int      `json:"heading_id"       example:"10"`
	ProgressPercent *float64  `json:"progress_percent" example:"32.50"`
	UpdatedAt       time.Time `json:"updated_at"       example:"2026-01-01T00:00:00Z"`
} // @name entity.ReadingProgress

// Bookmark stores a saved reader location.
type Bookmark struct {
	ID        string    `json:"id"         example:"550e8400-e29b-41d4-a716-446655440000"`
	UserID    string    `json:"user_id"    example:"550e8400-e29b-41d4-a716-446655440000"`
	BookID    int       `json:"book_id"    example:"797"`
	PageID    *int      `json:"page_id"    example:"12"`
	HeadingID *int      `json:"heading_id" example:"10"`
	Label     *string   `json:"label"`
	Note      *string   `json:"note"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
	UpdatedAt time.Time `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.Bookmark
