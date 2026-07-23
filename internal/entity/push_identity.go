package entity

import "time"

const PushDataSchemaV1 = "surau.push.v1"

// PushIdentityToken is the authenticated OneSignal identity credential returned to iOS.
type PushIdentityToken struct {
	SchemaVersion   string    `json:"schema_version" example:"surau.push.identity.v1"`
	IdentityToken   string    `json:"identity_token"`
	ExternalID      string    `json:"external_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	OwnerBinding    string    `json:"owner_binding" example:"ob1.opaque-value"`
	ExpiresAt       time.Time `json:"expires_at" example:"2026-01-01T00:15:00Z"`
	ExpiresIn       int64     `json:"expires_in" example:"900"`
	EligibleIntents []string  `json:"eligible_intents" example:"notify_khatam_milestones"`
}

// PushRouteInput is intentionally free of external_id: identity always comes from auth.
type PushRouteInput struct {
	SchemaVersion string `json:"schema_version" validate:"required,eq=surau.push.v1"`
	Scope         string `json:"scope" validate:"required,oneof=public personal"`
	Intent        string `json:"intent" validate:"required,max=80"`
	OwnerBinding  string `json:"owner_binding" validate:"omitempty,max=128"`
}

// PushRouteResolution is fail-closed: Home is returned for unknown, missing, or stale bindings.
type PushRouteResolution struct {
	Destination string `json:"destination" example:"home"`
	Intent      string `json:"intent,omitempty" example:"open_khatam_progress"`
}
