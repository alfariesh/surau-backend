package entity

// EvalJudgeEvidence is the only evidence an evaluator may submit to the
// service-only U-6 judge endpoint.
type EvalJudgeEvidence struct {
	Ref        string `json:"ref" validate:"required"`
	Quote      string `json:"quote" validate:"required"`
	Anchor     string `json:"anchor" validate:"required"`
	UnitID     string `json:"unit_id,omitempty"`
	UnitAnchor string `json:"unit_anchor,omitempty"`
}

// EvalJudgeRequest cannot select an inference task or inject a system prompt.
// The server resolves RubricVersion through its immutable allowlist.
type EvalJudgeRequest struct {
	CaseID        string              `json:"case_id" validate:"required"`
	RubricVersion string              `json:"rubric_version" validate:"required"`
	Question      string              `json:"question" validate:"required"`
	Answer        string              `json:"answer" validate:"required"`
	Evidence      []EvalJudgeEvidence `json:"evidence" validate:"required,min=1,dive"`
} // @name entity.EvalJudgeRequest

// EvalJudgeResponse separates advisory judgment from deterministic gate state.
type EvalJudgeResponse struct {
	CaseID        string           `json:"case_id"`
	RubricVersion string           `json:"rubric_version"`
	RubricSHA256  string           `json:"rubric_sha256"`
	Passed        bool             `json:"passed"`
	Reason        string           `json:"reason"`
	Inference     BookRAGInference `json:"inference"`
} // @name entity.EvalJudgeResponse
