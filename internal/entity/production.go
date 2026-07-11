package entity

import (
	"strings"
	"time"
)

const (
	ProductionWorkflowCandidate = "candidate"
	ProductionWorkflowDrafting  = "drafting"
	ProductionWorkflowInReview  = "in_review"
	ProductionWorkflowReady     = "ready"
	ProductionWorkflowPublished = "published"
	ProductionWorkflowArchived  = "archived"

	ProductionPublicationHidden    = "hidden"
	ProductionPublicationPublished = "published"
	ProductionPublicationArchived  = "archived"

	ProductionReviewDraft         = "draft"
	ProductionReviewPendingReview = "pending_review"
	ProductionReviewApproved      = "approved"
	ProductionReviewRejected      = "rejected"

	ProductionReviewDecisionSubmit  = "submit"
	ProductionReviewDecisionApprove = "approve"
	ProductionReviewDecisionReject  = "reject"

	ProductionAssetBookMetadata       = "book_metadata"
	ProductionAssetAuthorMetadata     = "author_metadata"
	ProductionAssetCategoryMetadata   = "category_metadata"
	ProductionAssetSectionTranslation = "section_translation"
	ProductionAssetHeadingSummary     = "heading_summary"
	ProductionAssetSectionAudio       = "section_audio"

	ProductionEventProjectCreate    = "production_project.create"
	ProductionEventProjectUpdate    = "production_project.update"
	ProductionEventDraftSave        = "production_asset.draft_save"
	ProductionEventDraftDelete      = "production_asset.draft_delete"
	ProductionEventDraftRestore     = "production_asset.draft_restore"
	ProductionEventReview           = "production_asset.review"
	ProductionEventProjectPublish   = "production_project.publish"
	ProductionEventProjectUnpublish = "production_project.unpublish"
	ProductionEventFinalDelete      = "production_asset.final_delete"
)

// BookProductionProject tracks one translation production workflow for a book and target language.
type BookProductionProject struct {
	ID                string                  `json:"id"                 example:"550e8400-e29b-41d4-a716-446655440000"`
	BookID            int                     `json:"book_id"            example:"797"`
	Lang              string                  `json:"lang"               example:"id"`
	WorkflowStatus    string                  `json:"workflow_status"    example:"drafting"`
	PublicationStatus string                  `json:"publication_status" example:"hidden"`
	RequiresReview    bool                    `json:"requires_review"    example:"true"`
	RequiresAudio     bool                    `json:"requires_audio"     example:"false"`
	Priority          int                     `json:"priority"           example:"10"`
	OwnerID           *string                 `json:"owner_id,omitempty"`
	Owner             *ProductionProjectOwner `json:"owner,omitempty"`
	Notes             *string                 `json:"notes,omitempty"`
	CreatedBy         *string                 `json:"created_by,omitempty"`
	UpdatedBy         *string                 `json:"updated_by,omitempty"`
	PublishedBy       *string                 `json:"published_by,omitempty"`
	CreatedAt         time.Time               `json:"created_at"         example:"2026-01-01T00:00:00Z"`
	UpdatedAt         time.Time               `json:"updated_at"         example:"2026-01-01T00:00:00Z"`
	PublishedAt       *time.Time              `json:"published_at,omitempty" example:"2026-01-01T00:00:00Z"`
	ArchivedAt        *time.Time              `json:"archived_at,omitempty"  example:"2026-01-01T00:00:00Z"`
} // @name entity.BookProductionProject

// ProductionProjectOwner is a lightweight owner summary for production UI displays.
type ProductionProjectOwner struct {
	ID          string  `json:"id"           example:"550e8400-e29b-41d4-a716-446655440000"`
	Email       string  `json:"email"        example:"editor@example.com"`
	DisplayName *string `json:"display_name,omitempty" example:"Editor Name"`
} // @name entity.ProductionProjectOwner

// BookProductionProjectPatch updates mutable project workflow settings.
type BookProductionProjectPatch struct {
	WorkflowStatus *string
	RequiresReview *bool
	RequiresAudio  *bool
	Priority       *int
	OwnerID        *string
	Notes          *string
}

// BookProductionCandidate summarizes one raw source kitab for production selection.
type BookProductionCandidate struct {
	BookID                    int        `json:"book_id" example:"797"`
	Name                      string     `json:"name"`
	CategoryID                *int       `json:"category_id,omitempty" example:"10"`
	CategoryName              *string    `json:"category_name,omitempty"`
	AuthorID                  *int       `json:"author_id,omitempty" example:"177"`
	AuthorName                *string    `json:"author_name,omitempty"`
	HasContent                bool       `json:"has_content" example:"true"`
	HeadingCount              int        `json:"heading_count" example:"120"`
	PageCount                 int        `json:"page_count" example:"300"`
	ExistingProjectID         *string    `json:"existing_project_id,omitempty"`
	ExistingWorkflowStatus    *string    `json:"existing_workflow_status,omitempty"`
	ExistingPublicationStatus *string    `json:"existing_publication_status,omitempty"`
	ExistingProjectUpdatedAt  *time.Time `json:"existing_project_updated_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookProductionCandidate

// BookProductionCompleteness describes whether a project can be published.
type BookProductionCompleteness struct {
	Project       BookProductionProject        `json:"project"`
	Ready         bool                         `json:"ready"          example:"false"`
	RequiredCount int                          `json:"required_count" example:"42"`
	CompleteCount int                          `json:"complete_count" example:"40"`
	MissingCount  int                          `json:"missing_count"  example:"2"`
	Missing       []BookProductionMissingAsset `json:"missing"`
} // @name entity.BookProductionCompleteness

// BookProductionDraftRevision stores one immutable draft snapshot.
type BookProductionDraftRevision struct {
	ID        string    `json:"id"         example:"550e8400-e29b-41d4-a716-446655440000"`
	ProjectID string    `json:"project_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	AssetType string    `json:"asset_type" example:"section_translation"`
	HeadingID *int      `json:"heading_id,omitempty" example:"10"`
	Version   int       `json:"version" example:"3"`
	ActorID   *string   `json:"actor_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	Snapshot  RawJSON   `json:"snapshot" swaggertype:"object"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookProductionDraftRevision

// BookProductionWorkspace is a compact editor view for one production project.
type BookProductionWorkspace struct {
	Project      BookProductionProject            `json:"project"`
	Book         BookProductionWorkspaceBook      `json:"book"`
	Completeness BookProductionCompleteness       `json:"completeness"`
	Metadata     ProductionAssetStatus            `json:"metadata"`
	Author       *ProductionAssetStatus           `json:"author,omitempty"`
	Category     *ProductionAssetStatus           `json:"category,omitempty"`
	Headings     []BookProductionWorkspaceHeading `json:"headings"`
} // @name entity.BookProductionWorkspace

// BookProductionWorkspaceBook contains the source book fields needed by production UI.
type BookProductionWorkspaceBook struct {
	ID           int     `json:"id"          example:"797"`
	Name         string  `json:"name"`
	CategoryID   *int    `json:"category_id,omitempty" example:"10"`
	CategoryName *string `json:"category_name,omitempty"`
	AuthorID     *int    `json:"author_id,omitempty" example:"177"`
	AuthorName   *string `json:"author_name,omitempty"`
	HasContent   bool    `json:"has_content" example:"true"`
} // @name entity.BookProductionWorkspaceBook

// BookProductionWorkspaceHeading summarizes draft status for one TOC heading.
type BookProductionWorkspaceHeading struct {
	BookID      int                   `json:"book_id"    example:"797"`
	HeadingID   int                   `json:"heading_id" example:"10"`
	ParentID    *int                  `json:"parent_id,omitempty" example:"1"`
	PageID      int                   `json:"page_id"    example:"12"`
	Depth       int                   `json:"depth"      example:"0"`
	Ordinal     int                   `json:"ordinal"    example:"9"`
	SourceTitle string                `json:"source_title"`
	Translation ProductionAssetStatus `json:"translation"`
	Summary     ProductionAssetStatus `json:"summary"`
	Audio       ProductionAssetStatus `json:"audio"`
} // @name entity.BookProductionWorkspaceHeading

// ProductionAssetStatus summarizes one draft/final asset slot.
type ProductionAssetStatus struct {
	AssetType    string     `json:"asset_type" example:"section_translation"`
	HeadingID    *int       `json:"heading_id,omitempty" example:"10"`
	Required     bool       `json:"required" example:"true"`
	Exists       bool       `json:"exists" example:"true"`
	Complete     bool       `json:"complete" example:"true"`
	ReviewStatus *string    `json:"review_status,omitempty" example:"approved"`
	UpdatedAt    *time.Time `json:"updated_at,omitempty" example:"2026-01-01T00:00:00Z"`
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	FinalExists  bool       `json:"final_exists" example:"false"`
} // @name entity.ProductionAssetStatus

// BookProductionEvent stores one operational timeline event for a production project.
type BookProductionEvent struct {
	ID        string    `json:"id"         example:"550e8400-e29b-41d4-a716-446655440000"`
	ProjectID string    `json:"project_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	ActorID   *string   `json:"actor_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	EventType string    `json:"event_type" example:"production_asset.review"`
	AssetType *string   `json:"asset_type,omitempty" example:"section_translation"`
	HeadingID *int      `json:"heading_id,omitempty" example:"10"`
	Note      *string   `json:"note,omitempty"`
	Payload   RawJSON   `json:"payload,omitempty" swaggertype:"object"`
	CreatedAt time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookProductionEvent

// BookProductionDashboard gives editors a compact operational overview for one target language.
type BookProductionDashboard struct {
	Lang                string                `json:"lang"                   example:"id"`
	CandidateCount      int                   `json:"candidate_count"        example:"120"`
	ActiveProjectCount  int                   `json:"active_project_count"   example:"18"`
	NeedsWorkCount      int                   `json:"needs_work_count"       example:"12"`
	ReadyToPublishCount int                   `json:"ready_to_publish_count" example:"3"`
	PublishedCount      int                   `json:"published_count"        example:"25"`
	RecentEvents        []BookProductionEvent `json:"recent_events"`
	RecentEventsTotal   int                   `json:"recent_events_total"    example:"42"`
} // @name entity.BookProductionDashboard

// BookProductionPublishCheck explains whether a production project can be published.
type BookProductionPublishCheck struct {
	Project        BookProductionProject        `json:"project"`
	Ready          bool                         `json:"ready"          example:"false"`
	CanPublish     bool                         `json:"can_publish"    example:"false"`
	LicenseStatus  string                       `json:"license_status" example:"needs_review"`
	RequiredCount  int                          `json:"required_count" example:"42"`
	CompleteCount  int                          `json:"complete_count" example:"40"`
	MissingCount   int                          `json:"missing_count"  example:"2"`
	Missing        []BookProductionMissingAsset `json:"missing"`
	BlockingErrors []BookProductionBlocking     `json:"blocking_errors"`
} // @name entity.BookProductionPublishCheck

// BookProductionBlocking is a structured publish blocker for editorial UI.
type BookProductionBlocking struct {
	Code      string `json:"code" example:"missing_required_asset"`
	AssetType string `json:"asset_type,omitempty" example:"section_translation"`
	HeadingID *int   `json:"heading_id,omitempty" example:"10"`
	Message   string `json:"message" example:"section translation draft is missing"`
} // @name entity.BookProductionBlocking

// BookProductionMissingAsset describes one missing requirement for publish.
type BookProductionMissingAsset struct {
	AssetType string `json:"asset_type" example:"section_translation"`
	HeadingID *int   `json:"heading_id,omitempty" example:"10"`
	Message   string `json:"message" example:"translation draft is missing"`
} // @name entity.BookProductionMissingAsset

// BookMetadataTranslationEdit stores production draft metadata translation.
type BookMetadataTranslationEdit struct {
	ProjectID       string              `json:"project_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	DisplayTitle    string              `json:"display_title"`
	Bibliography    *string             `json:"bibliography,omitempty"`
	Hint            *string             `json:"hint,omitempty"`
	Description     *string             `json:"description,omitempty"`
	Source          *string             `json:"source,omitempty"`
	Metadata        RawJSON             `json:"metadata,omitempty" swaggertype:"object"`
	ProvenanceClass string              `json:"provenance_class" example:"editorial"`
	Generation      *GenerationIdentity `json:"generation,omitempty"`
	ReviewStatus    string              `json:"review_status" example:"draft"`
	ReviewNote      *string             `json:"review_note,omitempty"`
	UpdatedBy       *string             `json:"updated_by,omitempty"`
	ReviewedBy      *string             `json:"reviewed_by,omitempty"`
	UpdatedAt       time.Time           `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	ReviewedAt      *time.Time          `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookMetadataTranslationEdit

// AuthorTranslationEdit stores production draft author translation.
type AuthorTranslationEdit struct {
	ProjectID       string              `json:"project_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Name            string              `json:"name"`
	Biography       *string             `json:"biography,omitempty"`
	DeathText       *string             `json:"death_text,omitempty"`
	Source          *string             `json:"source,omitempty"`
	Metadata        RawJSON             `json:"metadata,omitempty" swaggertype:"object"`
	ProvenanceClass string              `json:"provenance_class" example:"editorial"`
	Generation      *GenerationIdentity `json:"generation,omitempty"`
	ReviewStatus    string              `json:"review_status" example:"draft"`
	ReviewNote      *string             `json:"review_note,omitempty"`
	UpdatedBy       *string             `json:"updated_by,omitempty"`
	ReviewedBy      *string             `json:"reviewed_by,omitempty"`
	UpdatedAt       time.Time           `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	ReviewedAt      *time.Time          `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.AuthorTranslationEdit

// CategoryTranslationEdit stores production draft category translation.
type CategoryTranslationEdit struct {
	ProjectID       string              `json:"project_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	Name            string              `json:"name"`
	Source          *string             `json:"source,omitempty"`
	Metadata        RawJSON             `json:"metadata,omitempty" swaggertype:"object"`
	ProvenanceClass string              `json:"provenance_class" example:"editorial"`
	Generation      *GenerationIdentity `json:"generation,omitempty"`
	ReviewStatus    string              `json:"review_status" example:"draft"`
	ReviewNote      *string             `json:"review_note,omitempty"`
	UpdatedBy       *string             `json:"updated_by,omitempty"`
	ReviewedBy      *string             `json:"reviewed_by,omitempty"`
	UpdatedAt       time.Time           `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	ReviewedAt      *time.Time          `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.CategoryTranslationEdit

// SectionTranslationEdit stores production draft section translation.
type SectionTranslationEdit struct {
	ProjectID       string              `json:"project_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	HeadingID       int                 `json:"heading_id" example:"10"`
	Title           *string             `json:"title,omitempty"`
	Content         string              `json:"content"`
	Source          *string             `json:"source,omitempty"`
	Metadata        RawJSON             `json:"metadata,omitempty" swaggertype:"object"`
	ProvenanceClass string              `json:"provenance_class" example:"editorial"`
	Generation      *GenerationIdentity `json:"generation,omitempty"`
	ReviewStatus    string              `json:"review_status" example:"draft"`
	ReviewNote      *string             `json:"review_note,omitempty"`
	UpdatedBy       *string             `json:"updated_by,omitempty"`
	ReviewedBy      *string             `json:"reviewed_by,omitempty"`
	UpdatedAt       time.Time           `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	ReviewedAt      *time.Time          `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.SectionTranslationEdit

// HeadingSummaryEdit stores production draft heading summary.
type HeadingSummaryEdit struct {
	ProjectID       string              `json:"project_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	HeadingID       int                 `json:"heading_id" example:"10"`
	Summary         string              `json:"summary"`
	Source          *string             `json:"source,omitempty"`
	Metadata        RawJSON             `json:"metadata,omitempty" swaggertype:"object"`
	ProvenanceClass string              `json:"provenance_class" example:"editorial"`
	Generation      *GenerationIdentity `json:"generation,omitempty"`
	ReviewStatus    string              `json:"review_status" example:"draft"`
	ReviewNote      *string             `json:"review_note,omitempty"`
	UpdatedBy       *string             `json:"updated_by,omitempty"`
	ReviewedBy      *string             `json:"reviewed_by,omitempty"`
	UpdatedAt       time.Time           `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	ReviewedAt      *time.Time          `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.HeadingSummaryEdit

// SectionAudioEdit stores production draft audio metadata.
type SectionAudioEdit struct {
	ProjectID       string     `json:"project_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	HeadingID       int        `json:"heading_id" example:"10"`
	URL             string     `json:"url"`
	Narrator        *string    `json:"narrator,omitempty"`
	DurationSeconds *int       `json:"duration_seconds,omitempty" example:"120"`
	MIMEType        *string    `json:"mime_type,omitempty" example:"audio/mpeg"`
	Metadata        RawJSON    `json:"metadata,omitempty" swaggertype:"object"`
	ReviewStatus    string     `json:"review_status" example:"draft"`
	ReviewNote      *string    `json:"review_note,omitempty"`
	UpdatedBy       *string    `json:"updated_by,omitempty"`
	ReviewedBy      *string    `json:"reviewed_by,omitempty"`
	UpdatedAt       time.Time  `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.SectionAudioEdit

// NormalizeProductionWorkflowStatus trims, lowercases, and validates workflow status.
func NormalizeProductionWorkflowStatus(status string) (string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if !IsValidProductionWorkflowStatus(status) {
		return "", ErrInvalidStatus
	}

	return status, nil
}

// IsValidProductionWorkflowStatus reports whether a workflow status is supported.
func IsValidProductionWorkflowStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case ProductionWorkflowCandidate,
		ProductionWorkflowDrafting,
		ProductionWorkflowInReview,
		ProductionWorkflowReady,
		ProductionWorkflowPublished,
		ProductionWorkflowArchived:
		return true
	default:
		return false
	}
}

// NormalizeProductionReviewDecision trims, lowercases, and validates a review action.
func NormalizeProductionReviewDecision(decision string) (string, error) {
	decision = strings.ToLower(strings.TrimSpace(decision))
	switch decision {
	case ProductionReviewDecisionSubmit,
		ProductionReviewDecisionApprove,
		ProductionReviewDecisionReject:
		return decision, nil
	default:
		return "", ErrInvalidReviewDecision
	}
}

// NormalizeProductionAssetType trims, lowercases, and validates a production asset type.
func NormalizeProductionAssetType(assetType string) (string, error) {
	assetType = strings.ToLower(strings.TrimSpace(assetType))
	if !IsProductionAssetType(assetType) {
		return "", ErrInvalidAssetType
	}

	return assetType, nil
}

// IsProductionAssetType reports whether assetType is supported by production workflow.
func IsProductionAssetType(assetType string) bool {
	switch strings.ToLower(strings.TrimSpace(assetType)) {
	case ProductionAssetBookMetadata,
		ProductionAssetAuthorMetadata,
		ProductionAssetCategoryMetadata,
		ProductionAssetSectionTranslation,
		ProductionAssetHeadingSummary,
		ProductionAssetSectionAudio:
		return true
	default:
		return false
	}
}

// IsHeadingProductionAsset reports whether an asset is scoped to one TOC heading.
func IsHeadingProductionAsset(assetType string) bool {
	switch strings.ToLower(strings.TrimSpace(assetType)) {
	case ProductionAssetSectionTranslation,
		ProductionAssetHeadingSummary,
		ProductionAssetSectionAudio:
		return true
	default:
		return false
	}
}

// IsProductionEventType reports whether eventType is a known production timeline event.
func IsProductionEventType(eventType string) bool {
	switch strings.ToLower(strings.TrimSpace(eventType)) {
	case ProductionEventProjectCreate,
		ProductionEventProjectUpdate,
		ProductionEventDraftSave,
		ProductionEventDraftDelete,
		ProductionEventDraftRestore,
		ProductionEventReview,
		ProductionEventProjectPublish,
		ProductionEventProjectUnpublish,
		ProductionEventFinalDelete:
		return true
	default:
		return false
	}
}

// NormalizeProductionDraftTarget validates asset/heading pairing for draft revision APIs.
func NormalizeProductionDraftTarget(assetType string, headingID *int) (string, *int, error) {
	assetType, err := NormalizeProductionAssetType(assetType)
	if err != nil {
		return "", nil, err
	}

	if IsHeadingProductionAsset(assetType) {
		if headingID == nil || *headingID <= 0 {
			return "", nil, ErrHeadingNotFound
		}

		return assetType, headingID, nil
	}

	if headingID != nil {
		return "", nil, ErrInvalidProductionDraft
	}

	return assetType, nil, nil
}

// NormalizeProductionLang accepts v1 target production languages only.
func NormalizeProductionLang(lang string) (string, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	lang = strings.ReplaceAll(lang, "_", "-")
	if before, _, ok := strings.Cut(lang, "-"); ok {
		lang = before
	}

	switch lang {
	case UserPreferredLangDefault, "en":
		return lang, nil
	default:
		return "", ErrUnsupportedLanguage
	}
}
