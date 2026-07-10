package request

import "encoding/json"

// CreateCrossReference is the authenticated human-authoring payload. Method,
// actor, review status, normalization, and origin are assigned by the shared
// service so callers cannot forge attribution.
type CreateCrossReference struct {
	SourceAnchor string          `json:"source_anchor" validate:"required,max=500" example:"kitab/797/h/11"`
	TargetAnchor string          `json:"target_anchor" validate:"required,max=500" example:"kitab/798"`
	Kind         string          `json:"kind" validate:"required,oneof=cites quotes explains parallel" example:"quotes"`
	Confidence   *float64        `json:"confidence" validate:"required,gte=0,lte=1" example:"0.95"`
	EvidenceText string          `json:"evidence_text" validate:"required,max=100000"`
	Metadata     json.RawMessage `json:"metadata" swaggertype:"object"`
} // @name v1.CreateCrossReference

// ReviewCrossReference records a curator decision. All five existing review
// states are accepted so an approved claim can be reopened without erasing its
// audit trail.
type ReviewCrossReference struct {
	ReviewStatus string `json:"review_status" validate:"required,oneof=pending approved rejected ambiguous needs_review" example:"approved"`
} // @name v1.ReviewCrossReference
