package entity

import "time"

const ProvenanceClassLegacyUnknown = "legacy_unknown"

// GenerationIdentity is the public attribution tuple for one persisted LLM
// output. Task/provider metadata stays on GenerationRun and is not duplicated
// on every generated asset.
type GenerationIdentity struct {
	RunID         string `json:"run_id"         example:"550e8400-e29b-41d4-a716-446655440000"`
	ModelID       string `json:"model_id"       example:"gpt-5-mini-2026-06-01"`
	PromptVersion string `json:"prompt_version" example:"reader-translation-v1"`
} // @name entity.GenerationIdentity

// GenerationRun is an immutable descriptor registered before any output from
// that run is persisted. Metadata is an object containing optional task-level
// settings, never the model/prompt identity itself.
type GenerationRun struct {
	ID            string    `json:"id"`
	TaskName      string    `json:"task_name"`
	ModelID       string    `json:"model_id"`
	PromptVersion string    `json:"prompt_version"`
	Provider      *string   `json:"provider,omitempty"`
	Metadata      RawJSON   `json:"metadata,omitempty" swaggertype:"object"`
	CreatedAt     time.Time `json:"created_at"`
} // @name entity.GenerationRun

// Identity returns the typed attribution embedded in curation API responses.
func (r *GenerationRun) Identity() GenerationIdentity {
	return GenerationIdentity{
		RunID:         r.ID,
		ModelID:       r.ModelID,
		PromptVersion: r.PromptVersion,
	}
}
