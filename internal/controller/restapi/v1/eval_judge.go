package v1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	_ "github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response" // for swaggo
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/evalrubric"
	"github.com/gofiber/fiber/v2"
)

const evalJudgeTaskKey = "rag-judge"

// @Summary Judge one allowlisted RAG evaluation
// @Description Service-only advisory judge. Accepts no task key or free-form rubric/prompt. Requires rag-eval:read.
// @ID eval-judge
// @Tags eval,inference
// @Accept json
// @Produce json
// @Param X-Internal-Token header string true "Service token with rag-eval:read"
// @Param request body entity.EvalJudgeRequest true "Evaluation evidence"
// @Success 200 {object} entity.EvalJudgeResponse
// @Failure 400 {object} response.Error
// @Failure 401 {object} response.Error
// @Failure 403 {object} response.Error
// @Failure 503 {object} response.Error
// @Router /eval/judge [post]
//
//nolint:cyclop,funlen,gocyclo,wsl_v5 // Validation, immutable rubric binding, inference, and attribution form one endpoint contract.
func (r *V1) evalJudge(ctx *fiber.Ctx) error {
	if r.inference == nil {
		return errorResponse(ctx, http.StatusServiceUnavailable, "inference is not configured")
	}

	var input entity.EvalJudgeRequest
	decoder := json.NewDecoder(bytes.NewReader(ctx.Body()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference request")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference request")
	}

	if err := r.v.Struct(input); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference request")
	}

	rubric, err := evalrubric.Resolve(input.RubricVersion)
	if err != nil {
		if evalrubric.IsNotFound(err) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid inference request")
		}

		r.l.Error(err, "restapi - v1 - evalJudge rubric")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	userMaterial, err := json.Marshal(struct {
		CaseID     string                     `json:"case_id"`
		Rubric     json.RawMessage            `json:"rubric"`
		RubricHash string                     `json:"rubric_sha256"`
		Question   string                     `json:"question"`
		Answer     string                     `json:"answer"`
		Evidence   []entity.EvalJudgeEvidence `json:"evidence"`
	}{
		CaseID:     strings.TrimSpace(input.CaseID),
		Rubric:     rubric.Content,
		RubricHash: rubric.SHA256,
		Question:   strings.TrimSpace(input.Question),
		Answer:     strings.TrimSpace(input.Answer),
		Evidence:   input.Evidence,
	})
	if err != nil {
		r.l.Error(err, "restapi - v1 - evalJudge marshal")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	result, err := r.inference.Invoke(ctx.UserContext(), entity.InferenceInvoke{
		TaskKey:       evalJudgeTaskKey,
		Variables:     map[string]any{"user": string(userMaterial)},
		SourceVersion: "eval-rubric:" + rubric.SHA256,
		CacheVary: map[string]any{
			"case_id":        strings.TrimSpace(input.CaseID),
			"rubric_version": rubric.Version,
		},
	})
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	var decision struct {
		Passed bool   `json:"passed"`
		Reason string `json:"reason"`
	}
	if err = json.Unmarshal([]byte(result.Output), &decision); err != nil {
		r.l.Error(err, "restapi - v1 - evalJudge decode")

		return errorResponse(ctx, http.StatusServiceUnavailable, "inference provider unavailable")
	}

	if strings.TrimSpace(decision.Reason) == "" {
		return errorResponse(ctx, http.StatusServiceUnavailable, "inference provider unavailable")
	}

	return ctx.Status(http.StatusOK).JSON(entity.EvalJudgeResponse{
		CaseID:        strings.TrimSpace(input.CaseID),
		RubricVersion: rubric.Version,
		RubricSHA256:  rubric.SHA256,
		Passed:        decision.Passed,
		Reason:        strings.TrimSpace(decision.Reason),
		Inference: entity.BookRAGInference{
			CallID: result.CallID, Generation: result.Generation,
			Provider: result.Provider, Model: result.Model, Prompt: result.Prompt,
			Schema: result.Schema, Usage: result.Usage, Cost: result.Cost,
			CacheStatus: result.CacheStatus, Failover: result.Failover,
		},
	})
}
