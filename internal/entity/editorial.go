package entity

import "time"

const (
	UserRoleUser  = "user"
	UserRoleAdmin = "admin"

	PublicationStatusHidden    = "hidden"
	PublicationStatusDraft     = "draft"
	PublicationStatusPublished = "published"
	PublicationStatusArchived  = "archived"

	EditStatusDraft     = "draft"
	EditStatusPublished = "published"
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

// AdminPageEdit shows raw page content alongside editorial overrides.
type AdminPageEdit struct {
	Raw       BookPage      `json:"raw"`
	Draft     *BookPageEdit `json:"draft"`
	Published *BookPageEdit `json:"published"`
} // @name entity.AdminPageEdit
