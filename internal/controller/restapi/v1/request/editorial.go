package request

import (
	"encoding/json"
	"time"
)

// UpdatePublication -.
type UpdatePublication struct {
	Status    string `json:"status"     validate:"required" example:"published"`
	Featured  bool   `json:"featured"   example:"true"`
	SortOrder *int   `json:"sort_order" example:"10"`
} // @name v1.UpdatePublication

// UpdateBookLicense records one evidence-backed kitab license decision.
type UpdateBookLicense struct {
	LicenseStatus string  `json:"license_status" validate:"required,oneof=unknown needs_review permitted restricted public_domain" example:"permitted"`
	Reason        string  `json:"reason" validate:"required,max=2000" example:"Publisher permission received"`
	EvidenceURL   *string `json:"evidence_url" validate:"omitempty,max=2048" example:"https://example.org/license"`
} // @name v1.UpdateBookLicense

// SaveMetadataDraft -.
type SaveMetadataDraft struct {
	DisplayTitle *string `json:"display_title" validate:"omitempty,max=500"`
	Bibliography *string `json:"bibliography"   validate:"omitempty,max=10000"`
	Hint         *string `json:"hint"           validate:"omitempty,max=10000"`
	Description  *string `json:"description"   validate:"omitempty,max=10000"`
	CoverURL     *string `json:"cover_url"     validate:"omitempty,url,max=2000"`
	CategoryID   *int    `json:"category_id"   validate:"omitempty,min=1"`
	Notes        *string `json:"notes"         validate:"omitempty,max=10000"`
} // @name v1.SaveMetadataDraft

// SavePageDraft -.
type SavePageDraft struct {
	ContentHTML string `json:"content_html" validate:"required,max=300000"`
} // @name v1.SavePageDraft

// SaveHeadingDraft -.
type SaveHeadingDraft struct {
	Content string `json:"content" validate:"required,max=2000"`
} // @name v1.SaveHeadingDraft

// SaveQuranSurahEditorialDraft is a complete editable snapshot for one
// language-specific surah draft. Workflow identity/status fields come from
// the route and use case, never from the client body.
type SaveQuranSurahEditorialDraft struct {
	MetaTitle       *string         `json:"meta_title" validate:"omitempty,max=500"`
	MetaDescription *string         `json:"meta_description" validate:"omitempty,max=10000"`
	ArtiNama        *string         `json:"arti_nama" validate:"omitempty,max=500"`
	Keutamaan       *string         `json:"keutamaan_html" validate:"omitempty,max=300000"`
	AsbabunNuzul    *string         `json:"asbabun_nuzul_html" validate:"omitempty,max=300000"`
	PokokKandungan  *string         `json:"pokok_kandungan_html" validate:"omitempty,max=300000"`
	AuthorName      *string         `json:"author_name" validate:"omitempty,max=500"`
	ReviewedBy      *string         `json:"reviewed_by" validate:"omitempty,max=500"`
	ReviewedAt      *time.Time      `json:"reviewed_at"`
	LicenseStatus   string          `json:"license_status" validate:"required,oneof=unknown needs_review permitted restricted public_domain" example:"needs_review"`
	Metadata        json.RawMessage `json:"metadata" swaggertype:"object"`
} // @name v1.SaveQuranSurahEditorialDraft

// QuranAyahEditorialFAQ is one validated FAQ entry in an ayah draft.
type QuranAyahEditorialFAQ struct {
	Question   string `json:"question" validate:"required,max=2000"`
	AnswerHTML string `json:"answer_html" validate:"required,max=300000"`
} // @name v1.QuranAyahEditorialFAQ

// SaveQuranAyahEditorialDraft is a complete editable snapshot for one
// language-specific ayah draft.
type SaveQuranAyahEditorialDraft struct {
	MetaTitle       *string                 `json:"meta_title" validate:"omitempty,max=500"`
	MetaDescription *string                 `json:"meta_description" validate:"omitempty,max=10000"`
	Intisari        *string                 `json:"intisari_html" validate:"omitempty,max=300000"`
	Keutamaan       *string                 `json:"keutamaan_html" validate:"omitempty,max=300000"`
	FAQ             []QuranAyahEditorialFAQ `json:"faq" validate:"max=100,dive"`
	TafsirRange     *string                 `json:"tafsir_range" validate:"omitempty,max=50" example:"255"`
	AuthorName      *string                 `json:"author_name" validate:"omitempty,max=500"`
	ReviewedBy      *string                 `json:"reviewed_by" validate:"omitempty,max=500"`
	ReviewedAt      *time.Time              `json:"reviewed_at"`
	LicenseStatus   string                  `json:"license_status" validate:"required,oneof=unknown needs_review permitted restricted public_domain" example:"needs_review"`
	Metadata        json.RawMessage         `json:"metadata" swaggertype:"object"`
} // @name v1.SaveQuranAyahEditorialDraft

// CollabSavePageDraft is the internal payload the collab server uses to sync
// a merged collaborative document into the page draft pipeline. actor_id is
// the last mutating editor; contributors lists everyone connected during the
// debounce window (recorded for attribution, not authorization — the service
// token is the trust boundary).
type CollabSavePageDraft struct {
	ContentHTML  string   `json:"content_html" validate:"required,max=300000"`
	ActorID      string   `json:"actor_id" validate:"required,uuid"`
	Contributors []string `json:"contributors" validate:"omitempty,dive,uuid"`
} // @name v1.CollabSavePageDraft

// AddCollectionItem -.
type AddCollectionItem struct {
	BookID    int  `json:"book_id"    validate:"required,min=1" example:"797"`
	SortOrder *int `json:"sort_order" example:"10"`
} // @name v1.AddCollectionItem

// CreateProductionProject -.
type CreateProductionProject struct {
	BookID         int     `json:"book_id" validate:"required,min=1" example:"797"`
	Lang           string  `json:"lang" validate:"required,oneof=id en" example:"id"`
	RequiresReview *bool   `json:"requires_review" example:"true"`
	RequiresAudio  bool    `json:"requires_audio" example:"false"`
	Priority       int     `json:"priority" validate:"omitempty,min=0" example:"10"`
	OwnerID        *string `json:"owner_id" validate:"omitempty,uuid"`
	Notes          *string `json:"notes" validate:"omitempty,max=10000"`
} // @name v1.CreateProductionProject

// UpdateProductionProject -.
type UpdateProductionProject struct {
	WorkflowStatus *string `json:"workflow_status" validate:"omitempty,oneof=candidate drafting in_review ready published archived"`
	RequiresReview *bool   `json:"requires_review"`
	RequiresAudio  *bool   `json:"requires_audio"`
	Priority       *int    `json:"priority" validate:"omitempty,min=0"`
	OwnerID        *string `json:"owner_id" validate:"omitempty,uuid"`
	Notes          *string `json:"notes" validate:"omitempty,max=10000"`
} // @name v1.UpdateProductionProject

// SaveMetadataTranslationDraft -.
type SaveMetadataTranslationDraft struct {
	DisplayTitle string          `json:"display_title" validate:"required,max=500"`
	Bibliography *string         `json:"bibliography" validate:"omitempty,max=10000"`
	Hint         *string         `json:"hint" validate:"omitempty,max=10000"`
	Description  *string         `json:"description" validate:"omitempty,max=10000"`
	Source       *string         `json:"source" validate:"omitempty,max=255"`
	Metadata     json.RawMessage `json:"metadata" swaggertype:"object"`
} // @name v1.SaveMetadataTranslationDraft

// SaveAuthorTranslationDraft -.
type SaveAuthorTranslationDraft struct {
	Name      string          `json:"name" validate:"required,max=500"`
	Biography *string         `json:"biography" validate:"omitempty,max=20000"`
	DeathText *string         `json:"death_text" validate:"omitempty,max=255"`
	Source    *string         `json:"source" validate:"omitempty,max=255"`
	Metadata  json.RawMessage `json:"metadata" swaggertype:"object"`
} // @name v1.SaveAuthorTranslationDraft

// SaveCategoryTranslationDraft -.
type SaveCategoryTranslationDraft struct {
	Name     string          `json:"name" validate:"required,max=500"`
	Source   *string         `json:"source" validate:"omitempty,max=255"`
	Metadata json.RawMessage `json:"metadata" swaggertype:"object"`
} // @name v1.SaveCategoryTranslationDraft

// SaveSectionTranslationDraft -.
type SaveSectionTranslationDraft struct {
	Title    *string         `json:"title" validate:"omitempty,max=1000"`
	Content  string          `json:"content" validate:"required,max=200000"`
	Source   *string         `json:"source" validate:"omitempty,max=255"`
	Metadata json.RawMessage `json:"metadata" swaggertype:"object"`
} // @name v1.SaveSectionTranslationDraft

// SaveHeadingSummaryDraft -.
type SaveHeadingSummaryDraft struct {
	Summary  string          `json:"summary" validate:"required,max=20000"`
	Source   *string         `json:"source" validate:"omitempty,max=255"`
	Metadata json.RawMessage `json:"metadata" swaggertype:"object"`
} // @name v1.SaveHeadingSummaryDraft

// SaveSectionAudioDraft -.
type SaveSectionAudioDraft struct {
	URL             string          `json:"url" validate:"required,url,max=2000"`
	Narrator        *string         `json:"narrator" validate:"omitempty,max=255"`
	DurationSeconds *int            `json:"duration_seconds" validate:"omitempty,min=0"`
	MIMEType        *string         `json:"mime_type" validate:"omitempty,max=255"`
	Metadata        json.RawMessage `json:"metadata" swaggertype:"object"`
} // @name v1.SaveSectionAudioDraft

// ReviewProductionAsset -.
type ReviewProductionAsset struct {
	AssetType string  `json:"asset_type" validate:"required,oneof=book_metadata author_metadata category_metadata section_translation heading_summary section_audio"`
	HeadingID *int    `json:"heading_id" validate:"omitempty,min=1"`
	Decision  string  `json:"decision" validate:"required,oneof=submit approve reject"`
	Note      *string `json:"note" validate:"omitempty,max=2000"`
} // @name v1.ReviewProductionAsset

// DeleteFinalProductionAsset -.
type DeleteFinalProductionAsset struct {
	Reason *string `json:"reason" validate:"omitempty,max=2000"`
} // @name v1.DeleteFinalProductionAsset
