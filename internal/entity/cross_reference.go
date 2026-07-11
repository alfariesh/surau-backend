package entity

import "time"

// Cross-Reference vocabulary is intentionally closed at B-3. The values
// mirror the database CHECK constraints and are part of the public/editorial
// API contract.
const (
	CrossReferenceKindCites    = "cites"
	CrossReferenceKindQuotes   = "quotes"
	CrossReferenceKindExplains = "explains"
	CrossReferenceKindParallel = "parallel"

	CrossReferenceMethodResolver = "resolver"
	CrossReferenceMethodMachine  = "machine"
	CrossReferenceMethodHuman    = "human"

	CrossReferenceStatusPending     = "pending"
	CrossReferenceStatusApproved    = "approved"
	CrossReferenceStatusRejected    = "rejected"
	CrossReferenceStatusAmbiguous   = "ambiguous"
	CrossReferenceStatusNeedsReview = "needs_review"

	CrossReferenceDirectionIncoming = "incoming"
	CrossReferenceDirectionOutgoing = "outgoing"

	CrossReferenceOriginHuman       = "human"
	CrossReferenceOriginResolver    = "resolver"
	CrossReferenceOriginMachine     = "machine"
	CrossReferenceOriginLegacyQuran = "legacy_quran_reference"
)

// CrossReferenceMethodDetail identifies how one Cross-Reference was made.
// Resolver links carry Strategy; machine links carry the immutable generation
// identity; human links carry the authenticated ActorID. These fields are
// conditional, never interchangeable provenance labels.
type CrossReferenceMethodDetail struct {
	Strategy      string `json:"strategy,omitempty"       example:"explicit_surah_ayah"`
	ModelID       string `json:"model_id,omitempty"       example:"gpt-5-mini-2026-06-01"`
	PromptVersion string `json:"prompt_version,omitempty" example:"quran-reference-v1"`
	RunID         string `json:"run_id,omitempty"         example:"550e8400-e29b-41d4-a716-446655440000"`
	ActorID       string `json:"actor_id,omitempty"       example:"550e8400-e29b-41d4-a716-446655440000"`
} // @name entity.CrossReferenceMethodDetail

// CrossReference is one attributed claim between two canonical Anchors.
// Projection fields are persistence accelerators, not a second identity.
type CrossReference struct {
	ID                   string                     `json:"id"                                 example:"550e8400-e29b-41d4-a716-446655440000"`
	SourceAnchor         string                     `json:"source_anchor"                      example:"kitab/797/h/11"`
	TargetAnchor         string                     `json:"target_anchor"                      example:"quran/73:4"`
	SourceCorpus         string                     `json:"source_corpus"                      example:"kitab"`
	TargetCorpus         string                     `json:"target_corpus"                      example:"quran"`
	SourceWorkID         *int                       `json:"source_work_id,omitempty"            example:"797"`
	TargetWorkID         *int                       `json:"target_work_id,omitempty"            example:"798"`
	TargetQuranSurahID   *int                       `json:"target_quran_surah_id,omitempty"      example:"73"`
	TargetQuranFromAyah  *int                       `json:"target_quran_from_ayah,omitempty"     example:"4"`
	TargetQuranToAyah    *int                       `json:"target_quran_to_ayah,omitempty"       example:"10"`
	Kind                 string                     `json:"kind"                               example:"quotes"`
	Method               string                     `json:"method"                             example:"resolver"`
	MethodDetail         CrossReferenceMethodDetail `json:"method_detail"`
	GenerationRunID      *string                    `json:"-"`
	Generation           *GenerationIdentity        `json:"generation,omitempty"`
	Confidence           *float64                   `json:"confidence,omitempty"                example:"1"`
	ReviewStatus         string                     `json:"review_status"                      example:"approved"`
	EvidenceText         string                     `json:"evidence_text"`
	EvidenceNormalized   string                     `json:"evidence_normalized"`
	NormalizationVersion int                        `json:"normalization_version"               example:"1"`
	Origin               string                     `json:"origin"                             example:"legacy_quran_reference"`
	OriginKey            string                     `json:"origin_key"                         example:"550e8400-e29b-41d4-a716-446655440000"`
	CreatedBy            *string                    `json:"created_by,omitempty"`
	ReviewedBy           *string                    `json:"reviewed_by,omitempty"`
	ReviewedAt           *time.Time                 `json:"reviewed_at,omitempty"`
	Metadata             RawJSON                    `json:"metadata,omitempty" swaggertype:"object"`
	CreatedAt            time.Time                  `json:"created_at"                         example:"2026-07-10T12:00:00Z"`
	UpdatedAt            time.Time                  `json:"updated_at"                         example:"2026-07-10T12:00:00Z"`
} // @name entity.CrossReference

// CrossReferenceCreateInput is the authenticated human-authoring surface.
// Actor, method, review status, origin, ids, and derived projections are set by
// the service rather than trusted from the request payload.
type CrossReferenceCreateInput struct {
	SourceAnchor string  `json:"source_anchor"`
	TargetAnchor string  `json:"target_anchor"`
	Kind         string  `json:"kind"`
	Confidence   float64 `json:"confidence"`
	EvidenceText string  `json:"evidence_text"`
	Metadata     RawJSON `json:"metadata,omitempty" swaggertype:"object"`
} // @name entity.CrossReferenceCreateInput

// CrossReferenceList is the stable list envelope. WorkTotal counts distinct
// Works on the opposite side of the requested direction; it intentionally
// differs from Total when one Work contributes multiple edges.
type CrossReferenceList struct {
	Items     []CrossReference `json:"items"`
	Total     int              `json:"total"`
	WorkTotal int              `json:"work_total"`
} // @name entity.CrossReferenceList

// QuranCrossReferenceBridge preserves the legacy Quran reference locator and
// wire fields while the generic registry becomes the source of truth. ID is
// the same UUID as both cross_references.id and quran_book_references.id.
type QuranCrossReferenceBridge struct {
	ID                 string
	BookID             int
	PageID             int
	HeadingID          *int
	KnowledgeMentionID *string
	SourceText         string
	NormalizedText     string
	ReferenceKind      string
	SurahID            *int
	FromAyahNumber     *int
	ToAyahNumber       *int
	FromAyahKey        *string
	ToAyahKey          *string
	MatchStrategy      string
	Metadata           RawJSON
	CreatedAt          time.Time
	UpdatedAt          time.Time
}
