package entity

import "time"

const (
	PublicationStatusHidden    = "hidden"
	PublicationStatusDraft     = "draft"
	PublicationStatusPublished = "published"
	PublicationStatusArchived  = "archived"

	EditStatusDraft     = "draft"
	EditStatusPublished = "published"

	FeedbackStatusOpen     = "open"
	FeedbackStatusResolved = "resolved"

	MissingAssetBookMetadata       = "book_metadata"
	MissingAssetCategoryMetadata   = "category_metadata"
	MissingAssetAuthorMetadata     = "author_metadata"
	MissingAssetSectionTranslation = "section_translation"
	MissingAssetHeadingSummary     = "heading_summary"
	MissingAssetSectionAudio       = "section_audio"

	MissingQuranAssetSurahInfo         = "surah_info"
	MissingQuranAssetAyahTranslation   = "ayah_translation"
	MissingQuranAssetTranslationSource = "translation_source"
	MissingQuranAssetAudioPublic       = "audio_public"
)

// BookPublication controls public visibility for one book.
type BookPublication struct {
	BookID      int        `json:"book_id"      example:"797"`
	Status      string     `json:"status"       example:"published"`
	Featured    bool       `json:"featured"     example:"true"`
	SortOrder   *int       `json:"sort_order"   example:"10"`
	PublishedAt *time.Time `json:"published_at" example:"2026-01-01T00:00:00Z"`
	UpdatedBy   *string    `json:"updated_by"`
	UpdatedAt   time.Time  `json:"updated_at"   example:"2026-01-01T00:00:00Z"`
} // @name entity.BookPublication

// BookMetadataEdit stores draft or published catalog overrides.
type BookMetadataEdit struct {
	BookID       int        `json:"book_id"       example:"797"`
	Status       string     `json:"status"        example:"draft"`
	DisplayTitle *string    `json:"display_title"`
	Description  *string    `json:"description"`
	CoverURL     *string    `json:"cover_url"`
	CategoryID   *int       `json:"category_id"`
	Notes        *string    `json:"notes"`
	UpdatedBy    *string    `json:"updated_by"`
	UpdatedAt    time.Time  `json:"updated_at"     example:"2026-01-01T00:00:00Z"`
	PublishedAt  *time.Time `json:"published_at"   example:"2026-01-01T00:00:00Z"`
} // @name entity.BookMetadataEdit

// BookPageEdit stores draft or published page content overrides.
type BookPageEdit struct {
	BookID      int        `json:"book_id"      example:"797"`
	PageID      int        `json:"page_id"      example:"1"`
	Status      string     `json:"status"       example:"draft"`
	ContentHTML string     `json:"content_html"`
	ContentText string     `json:"content_text"`
	UpdatedBy   *string    `json:"updated_by"`
	UpdatedAt   time.Time  `json:"updated_at"   example:"2026-01-01T00:00:00Z"`
	PublishedAt *time.Time `json:"published_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookPageEdit

// BookHeadingEdit stores draft or published heading title overrides.
type BookHeadingEdit struct {
	BookID      int        `json:"book_id"      example:"797"`
	HeadingID   int        `json:"heading_id"   example:"10"`
	Status      string     `json:"status"       example:"draft"`
	Content     string     `json:"content"`
	UpdatedBy   *string    `json:"updated_by"`
	UpdatedAt   time.Time  `json:"updated_at"   example:"2026-01-01T00:00:00Z"`
	PublishedAt *time.Time `json:"published_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookHeadingEdit

// BookCollectionItem assigns a book to a curated shelf.
type BookCollectionItem struct {
	CollectionSlug string    `json:"collection_slug" example:"starter-50"`
	BookID         int       `json:"book_id"         example:"797"`
	SortOrder      *int      `json:"sort_order"      example:"10"`
	CreatedBy      *string   `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"      example:"2026-01-01T00:00:00Z"`
} // @name entity.BookCollectionItem

// EditorialPageEdit shows raw page content alongside editorial overrides.
type EditorialPageEdit struct {
	Raw       BookPage      `json:"raw"`
	Draft     *BookPageEdit `json:"draft"`
	Published *BookPageEdit `json:"published"`
} // @name entity.EditorialPageEdit

// EditorialTranslationFeedback shows one reader feedback row with editorial context.
type EditorialTranslationFeedback struct {
	ID                    string     `json:"id"         example:"550e8400-e29b-41d4-a716-446655440000"`
	BookID                int        `json:"book_id"    example:"797"`
	BookTitle             string     `json:"book_title"`
	HeadingID             int        `json:"heading_id" example:"10"`
	HeadingTitle          string     `json:"heading_title"`
	Lang                  string     `json:"lang"       example:"id"`
	UserID                *string    `json:"user_id,omitempty"`
	ClientID              *string    `json:"client_id,omitempty"`
	Vote                  string     `json:"vote"       example:"dislike"`
	Reason                *string    `json:"reason,omitempty" example:"style"`
	Note                  *string    `json:"note,omitempty"`
	Status                string     `json:"status"     example:"open"`
	ResolvedBy            *string    `json:"resolved_by,omitempty"`
	ResolvedAt            *time.Time `json:"resolved_at,omitempty"`
	ResolutionNote        *string    `json:"resolution_note,omitempty"`
	UserAgent             *string    `json:"user_agent,omitempty"`
	ClientIP              *string    `json:"client_ip,omitempty"`
	TranslationStatus     string     `json:"translation_status" example:"generated"`
	TranslationReviewedBy *string    `json:"translation_reviewed_by,omitempty"`
	TranslationReviewedAt *time.Time `json:"translation_reviewed_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at" example:"2026-01-01T00:00:00Z"`
	UpdatedAt             time.Time  `json:"updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.EditorialTranslationFeedback

// TranslationFeedbackHeadingSummary aggregates feedback for one translated heading.
type TranslationFeedbackHeadingSummary struct {
	BookID       int            `json:"book_id"    example:"797"`
	BookTitle    string         `json:"book_title"`
	HeadingID    int            `json:"heading_id" example:"10"`
	HeadingTitle string         `json:"heading_title"`
	Lang         string         `json:"lang"       example:"id"`
	Total        int            `json:"total"      example:"10"`
	Likes        int            `json:"likes"      example:"6"`
	Dislikes     int            `json:"dislikes"   example:"4"`
	Reasons      map[string]int `json:"reasons"`
} // @name entity.TranslationFeedbackHeadingSummary

// EditorialTranslationFeedbackSummary aggregates reader feedback for admin review.
type EditorialTranslationFeedbackSummary struct {
	Total               int                                 `json:"total"    example:"25"`
	Likes               int                                 `json:"likes"    example:"18"`
	Dislikes            int                                 `json:"dislikes" example:"7"`
	TopDislikedHeadings []TranslationFeedbackHeadingSummary `json:"top_disliked_headings"`
} // @name entity.EditorialTranslationFeedbackSummary

// EditorialMissingReaderAsset describes one missing localized reader asset for editorial work.
type EditorialMissingReaderAsset struct {
	AssetType       string    `json:"asset_type"        example:"section_translation"`
	TargetLang      string    `json:"target_lang"       example:"en"`
	BookID          *int      `json:"book_id"           example:"797"`
	BookTitle       *string   `json:"book_title"`
	HeadingID       *int      `json:"heading_id"        example:"10"`
	HeadingTitle    *string   `json:"heading_title"`
	CategoryID      *int      `json:"category_id"       example:"1"`
	CategoryName    *string   `json:"category_name"`
	AuthorID        *int      `json:"author_id"         example:"2"`
	AuthorName      *string   `json:"author_name"`
	AvailableLangs  []string  `json:"available_langs"   example:"id"`
	SourceUpdatedAt time.Time `json:"source_updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.EditorialMissingReaderAsset

// EditorialMissingReaderAssetCount aggregates missing reader assets by type and target language.
type EditorialMissingReaderAssetCount struct {
	AssetType  string `json:"asset_type"  example:"section_translation"`
	TargetLang string `json:"target_lang" example:"en"`
	Total      int    `json:"total"       example:"20"`
} // @name entity.EditorialMissingReaderAssetCount

// EditorialMissingReaderAssets groups paginated missing asset items and aggregate counts.
type EditorialMissingReaderAssets struct {
	Items  []EditorialMissingReaderAsset      `json:"items"`
	Total  int                                `json:"total" example:"42"`
	Counts []EditorialMissingReaderAssetCount `json:"counts"`
} // @name entity.EditorialMissingReaderAssets

// EditorialMissingQuranAsset describes one missing Quran asset for editorial work.
type EditorialMissingQuranAsset struct {
	AssetType             string    `json:"asset_type"        example:"ayah_translation"`
	TargetLang            string    `json:"target_lang"       example:"en"`
	SurahID               *int      `json:"surah_id,omitempty" example:"73"`
	SurahName             *string   `json:"surah_name,omitempty"`
	AyahNumber            *int      `json:"ayah_number,omitempty" example:"4"`
	AyahKey               *string   `json:"ayah_key,omitempty" example:"73:4"`
	TranslationSourceID   *string   `json:"translation_source_id,omitempty"`
	TranslationSourceName *string   `json:"translation_source_name,omitempty"`
	RecitationID          *string   `json:"recitation_id,omitempty"`
	TrackType             *string   `json:"track_type,omitempty" example:"ayah"`
	TrackKey              *string   `json:"track_key,omitempty" example:"73:4"`
	AvailableLangs        []string  `json:"available_langs"   example:"id"`
	SourceUpdatedAt       time.Time `json:"source_updated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.EditorialMissingQuranAsset

// EditorialMissingQuranAssetCount aggregates missing Quran assets by type and target language.
type EditorialMissingQuranAssetCount struct {
	AssetType  string `json:"asset_type"  example:"ayah_translation"`
	TargetLang string `json:"target_lang" example:"en"`
	Total      int    `json:"total"       example:"20"`
} // @name entity.EditorialMissingQuranAssetCount

// EditorialMissingQuranAssets groups paginated Quran missing asset items and aggregate counts.
type EditorialMissingQuranAssets struct {
	Items  []EditorialMissingQuranAsset      `json:"items"`
	Total  int                               `json:"total" example:"42"`
	Counts []EditorialMissingQuranAssetCount `json:"counts"`
} // @name entity.EditorialMissingQuranAssets
