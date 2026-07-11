package entity

import (
	"bytes"
	"encoding/json"
	"errors"
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
	ID                          int                `json:"id"             example:"797"`
	Name                        string             `json:"name"           example:"الزبد في مصطلح الحديث"`
	LicenseStatus               string             `json:"license_status" example:"permitted"`
	CategoryID                  *int               `json:"category_id"    example:"10"`
	CategoryName                *string            `json:"category_name"  example:"علوم الحديث"`
	AuthorID                    *int               `json:"author_id"      example:"177"`
	AuthorName                  *string            `json:"author_name"    example:"فضل الرحمن صافي"`
	Type                        *int               `json:"type"           example:"1"`
	Printed                     *int               `json:"printed"        example:"1"`
	MinorRelease                *int               `json:"minor_release"  example:"0"`
	MajorRelease                *int               `json:"major_release"  example:"1"`
	Bibliography                *string            `json:"bibliography"`
	Hint                        *string            `json:"hint"`
	PDFLinks                    RawJSON            `json:"pdf_links,omitempty" swaggertype:"object"`
	Metadata                    RawJSON            `json:"metadata,omitempty"  swaggertype:"object"`
	SourceDate                  *string            `json:"source_date"     example:"02091443"`
	Description                 *string            `json:"description"`
	CoverURL                    *string            `json:"cover_url"`
	EditorialNotes              *string            `json:"editorial_notes"`
	TranslationStatus           *string            `json:"translation_status,omitempty" example:"generated"`
	TranslationReviewedBy       *string            `json:"translation_reviewed_by,omitempty" example:"Editor A"`
	TranslationReviewedAt       *time.Time         `json:"translation_reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	Localization                LocalizationMeta   `json:"localization"`
	LanguageCoverage            []LanguageCoverage `json:"language_coverage,omitempty"`
	PublicationStatus           *string            `json:"publication_status" example:"published"`
	CatalogPublicationStatus    *string            `json:"catalog_publication_status,omitempty" example:"published"`
	CatalogPublished            bool               `json:"catalog_published" example:"true"`
	ProductionWorkflowStatus    *string            `json:"production_workflow_status,omitempty" example:"drafting"`
	ProductionPublicationStatus *string            `json:"production_publication_status,omitempty" example:"hidden"`
	ProductionPublished         bool               `json:"production_published" example:"false"`
	ProductionStatus            *string            `json:"production_status,omitempty" example:"candidate"`
	Featured                    bool               `json:"featured"           example:"false"`
	SortOrder                   *int               `json:"sort_order"         example:"10"`
	HasContent                  bool               `json:"has_content"        example:"true"`
	IsDeleted                   bool               `json:"is_deleted"         example:"false"`
	UpdatedAt                   time.Time          `json:"updated_at"         example:"2026-01-01T00:00:00Z"`
}

// BookCatalogStats summarizes the full published catalog independently from pagination.
type BookCatalogStats struct {
	Scope                    string             `json:"scope" example:"catalog_global"`
	TotalBooks               int                `json:"total_books" example:"120"`
	PublishedCount           int                `json:"published_count" example:"120"`
	CatalogPublishedCount    int                `json:"catalog_published_count" example:"120"`
	ProductionPublishedCount int                `json:"production_published_count" example:"25"`
	AuthorCount              int                `json:"author_count" example:"35"`
	CategoryCount            int                `json:"category_count" example:"12"`
	WithContentCount         int                `json:"with_content_count" example:"90"`
	CoverageCount            int                `json:"coverage_count" example:"25"`
	ByCategory               []BookCategoryStat `json:"by_category"`
} // @name entity.BookCatalogStats

// BookCategoryStat summarizes published catalog counts for one category.
type BookCategoryStat struct {
	CategoryID               *int    `json:"category_id" example:"10"`
	CategoryName             *string `json:"category_name"`
	Total                    int     `json:"total" example:"20"`
	PublishedCount           int     `json:"published_count" example:"20"`
	CatalogPublishedCount    int     `json:"catalog_published_count" example:"20"`
	ProductionPublishedCount int     `json:"production_published_count" example:"8"`
	CoverageCount            int     `json:"coverage_count" example:"8"`
} // @name entity.BookCategoryStat

// RawJSON is used for metadata stored as jsonb.
type RawJSON []byte

var (
	errRawJSONNilReceiver = errors.New("entity.RawJSON: unmarshal into nil receiver")
	errRawJSONInvalid     = errors.New("entity.RawJSON: invalid JSON")
)

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

// UnmarshalJSON preserves JSON objects and arrays as raw bytes.
func (r *RawJSON) UnmarshalJSON(data []byte) error {
	if r == nil {
		return errRawJSONNilReceiver
	}

	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*r = nil

		return nil
	}

	if !json.Valid(data) {
		return errRawJSONInvalid
	}

	*r = append((*r)[:0], data...)

	return nil
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

// SourceQuranCitation is a best-effort inline Quran citation detected in source text.
type SourceQuranCitation struct {
	Quote     string `json:"quote"`
	Reference string `json:"reference" example:"البقرة- ٢٥٥"`
} // @name entity.SourceQuranCitation

// SourceBlock is one semantic source content block for stable reader rendering.
type SourceBlock struct {
	Type           string                `json:"type" example:"paragraph"`
	Text           string                `json:"text"`
	HTML           string                `json:"html"`
	QuranCitations []SourceQuranCitation `json:"quran_citations,omitempty"`
} // @name entity.SourceBlock

// SourceFootnote is one source footnote extracted from plain kitab text.
type SourceFootnote struct {
	Marker string `json:"marker" example:"(¬١)"`
	Text   string `json:"text"`
	HTML   string `json:"html"`
} // @name entity.SourceFootnote

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
	OriginalFormat            string              `json:"original_format" example:"plain_text"`
	OriginalBlocks            []SourceBlock       `json:"original_blocks"`
	OriginalFootnotes         []SourceFootnote    `json:"original_footnotes"`
	Translation               *SectionTranslation `json:"translation"`
	Audio                     *SectionAudio       `json:"audio"`
	Availability              ReaderAvailability  `json:"availability"`
} // @name entity.BookSection

// BookTOCRead is an article-like section response with TOC navigation context.
type BookTOCRead struct {
	BookID                    int                  `json:"book_id"       example:"797"`
	HeadingID                 int                  `json:"heading_id"    example:"10"`
	Title                     string               `json:"title"         example:"باب النية"`
	RequestedLang             string               `json:"requested_lang" example:"en"`
	TitleLang                 string               `json:"title_lang"     example:"ar"`
	IsTitleFallback           bool                 `json:"is_title_fallback" example:"true"`
	Summary                   *string              `json:"summary,omitempty"`
	SummaryLang               *string              `json:"summary_lang,omitempty" example:"id"`
	HasSummary                bool                 `json:"has_summary"   example:"true"`
	TranslationMissing        bool                 `json:"translation_missing" example:"false"`
	AvailableTranslationLangs []string             `json:"available_translation_langs" example:"id"`
	AvailableSummaryLangs     []string             `json:"available_summary_langs" example:"id"`
	Breadcrumb                []BookTOCLink        `json:"breadcrumb"`
	Children                  []BookTOCLink        `json:"children"`
	Previous                  *BookTOCLink         `json:"previous"`
	Next                      *BookTOCLink         `json:"next"`
	StartPageID               int                  `json:"start_page_id" example:"12"`
	EndPageID                 int                  `json:"end_page_id"   example:"15"`
	OriginalHTML              string               `json:"original_html"`
	OriginalText              string               `json:"original_text"`
	OriginalFormat            string               `json:"original_format" example:"plain_text"`
	OriginalBlocks            []SourceBlock        `json:"original_blocks"`
	OriginalFootnotes         []SourceFootnote     `json:"original_footnotes"`
	Translation               *SectionTranslation  `json:"translation"`
	Audio                     *SectionAudio        `json:"audio"`
	QuranReferences           []BookQuranReference `json:"quran_references,omitempty"`
	Availability              ReaderAvailability   `json:"availability"`
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
	ObservedAt      time.Time `json:"observed_at"      example:"2026-01-01T00:00:00Z"`
	UpdatedAt       time.Time `json:"updated_at"       example:"2026-01-01T00:00:00Z"`
} // @name entity.ReadingProgress

// ReadingProgressBookSummary is light book metadata for the continue-reading shelf.
type ReadingProgressBookSummary struct {
	BookID     int     `json:"book_id"               example:"797"`
	Name       string  `json:"name"                  example:"صحيح البخاري"`
	CoverURL   *string `json:"cover_url,omitempty"   example:"https://cdn.example/cover.jpg"`
	AuthorName *string `json:"author_name,omitempty" example:"الإمام البخاري"`
} // @name entity.ReadingProgressBookSummary

// ContinueReadingEntry is one in-progress book with resume metadata.
type ContinueReadingEntry struct {
	ReadingProgress
	Book ReadingProgressBookSummary `json:"book"`
} // @name entity.ContinueReadingEntry
