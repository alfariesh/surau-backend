package request

import "time"

// SaveProgress -.
type SaveProgress struct {
	PageID           *int       `json:"page_id"            validate:"omitempty,min=1"         example:"12"`
	HeadingID        *int       `json:"heading_id"         validate:"omitempty,min=1"         example:"10"`
	ProgressPercent  *float64   `json:"progress_percent"   validate:"omitempty,min=0,max=100" example:"32.5"`
	ClientObservedAt *time.Time `json:"client_observed_at" validate:"omitempty"               example:"2026-01-01T00:00:00Z"`
} // @name v1.SaveProgress

// SaveTOCProgress -.
type SaveTOCProgress struct {
	ProgressPercent  *float64   `json:"progress_percent"   validate:"omitempty,min=0,max=100" example:"32.5"`
	ClientObservedAt *time.Time `json:"client_observed_at" validate:"omitempty"               example:"2026-01-01T00:00:00Z"`
} // @name v1.SaveTOCProgress

// SaveQuranProgress -.
type SaveQuranProgress struct {
	AyahKey          string     `json:"ayah_key"            validate:"required,max=16" example:"73:4"`
	ClientObservedAt *time.Time `json:"client_observed_at" validate:"omitempty"       example:"2026-01-01T00:00:00Z"`
} // @name v1.SaveQuranProgress

// UpsertSavedItem -.
type UpsertSavedItem struct {
	ItemType       string   `json:"item_type"        validate:"required,oneof=book_page book_heading quran_ayah quran_range" example:"quran_ayah"`
	BookID         *int     `json:"book_id"          validate:"omitempty,min=1" example:"797"`
	PageID         *int     `json:"page_id"          validate:"omitempty,min=1" example:"12"`
	HeadingID      *int     `json:"heading_id"       validate:"omitempty,min=1" example:"10"`
	SurahID        *int     `json:"surah_id"         validate:"omitempty,min=1,max=114" example:"73"`
	AyahKey        *string  `json:"ayah_key"         validate:"omitempty,max=16" example:"73:4"`
	FromAyahNumber *int     `json:"from_ayah_number" validate:"omitempty,min=1" example:"4"`
	ToAyahNumber   *int     `json:"to_ayah_number"   validate:"omitempty,min=1" example:"6"`
	Label          *string  `json:"label"            validate:"omitempty,max=255"`
	Note           *string  `json:"note"             validate:"omitempty,max=2000"`
	Tags           []string `json:"tags"             validate:"omitempty"`
} // @name v1.UpsertSavedItem

// UpdateSavedItem is a true partial update: absent fields stay unchanged,
// explicit null clears. Length limits are enforced in the usecase because
// validator tags cannot see through Optional.
type UpdateSavedItem struct {
	Label Optional[string]   `json:"label" swaggertype:"string"`
	Note  Optional[string]   `json:"note"  swaggertype:"string"`
	Tags  Optional[[]string] `json:"tags"  swaggertype:"array,string"`
} // @name v1.UpdateSavedItem

// StartKhatamCycle -.
type StartKhatamCycle struct {
	Notes *string `json:"notes" validate:"omitempty,max=2000" example:"Khatam Ramadhan"`
} // @name v1.StartKhatamCycle

// CreateTranslationFeedback -.
type CreateTranslationFeedback struct {
	Vote     string  `json:"vote"      validate:"required,oneof=like dislike" example:"dislike"`
	Reason   *string `json:"reason"    validate:"omitempty,oneof=inaccurate unclear style typo formatting other"`
	Note     *string `json:"note"      validate:"omitempty,max=2000"`
	ClientID *string `json:"client_id" validate:"omitempty,max=128"`
} // @name v1.CreateTranslationFeedback

// BookRAG -.
type BookRAG struct {
	Question     string `json:"question"      validate:"required,min=2,max=4000" example:"Apa definisi hadis sahih?"`
	Stream       bool   `json:"stream"        example:"false"`
	IncludeTrace bool   `json:"include_trace" example:"false"`
	MaxCitations int    `json:"max_citations" validate:"omitempty,min=1,max=10" example:"5"`
} // @name v1.BookRAG

// ResolveTranslationFeedback -.
type ResolveTranslationFeedback struct {
	Note *string `json:"note" validate:"omitempty,max=2000"`
} // @name v1.ResolveTranslationFeedback
