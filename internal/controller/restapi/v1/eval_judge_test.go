//nolint:wsl_v5 // Endpoint setup and response assertions stay grouped by authentication outcome.
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/evalrubric"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalJudgeRequiresRAGEvalScopeAndReturnsAttribution(t *testing.T) {
	t.Parallel()

	generation := entity.GenerationIdentity{
		RunID:         "44444444-4444-4444-8444-444444444444",
		ModelID:       "judge-model",
		PromptVersion: "rag-judge-v1",
	}
	inference := &inferenceControllerFake{result: entity.InferenceResult{
		CallID:     "55555555-5555-4555-8555-555555555555",
		Output:     `{"passed":true,"reason":"Every claim is supported."}`,
		Generation: generation, Provider: "deterministic", Model: "judge-model",
		Prompt: "rag-judge-v1", Schema: "rag-judge-v1",
		Usage:       entity.InferenceUsage{InputTokens: 10, OutputTokens: 4, Source: "provider"},
		Cost:        entity.InferenceCost{NanoUSD: 120, USD: "0.000000120", Source: "registry"},
		CacheStatus: "miss",
	}}
	identity := &evalJudgeIdentityFake{}
	app := newEvalJudgeTestApp(inference, identity)
	body := `{"case_id":"kitab-1","rubric_version":"groundedness-v1",` +
		`"question":"What?","answer":"Answer [S1].",` +
		`"evidence":[{"ref":"S1","quote":"Answer","anchor":"book/1/page/1"}]}`

	unauthorized := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/eval/judge", bytes.NewBufferString(body),
	)
	unauthorized.Header.Set("Content-Type", "application/json")
	response, err := app.Test(unauthorized)
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NoError(t, response.Body.Close())

	identity.denyScope = true
	forbidden := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/eval/judge", bytes.NewBufferString(body),
	)
	forbidden.Header.Set("Content-Type", "application/json")
	forbidden.Header.Set(middleware.ServiceTokenHeader, testServiceToken)
	response, err = app.Test(forbidden)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, response.StatusCode)
	require.NoError(t, response.Body.Close())

	identity.denyScope = false
	allowed := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/eval/judge", bytes.NewBufferString(body),
	)
	allowed.Header.Set("Content-Type", "application/json")
	allowed.Header.Set(middleware.ServiceTokenHeader, testServiceToken)
	response, err = app.Test(allowed)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)

	var payload entity.EvalJudgeResponse
	decodeJSONBody(t, response, &payload)
	rubric, err := evalrubric.Resolve(evalrubric.GroundednessV1)
	require.NoError(t, err)
	assert.Equal(t, rubric.SHA256, payload.RubricSHA256)
	assert.Equal(t, "rag-judge-v1", payload.Inference.Prompt)
	assert.Equal(t, int64(120), payload.Inference.Cost.NanoUSD)
	assert.Equal(t, evalJudgeTaskKey, inference.invoked.TaskKey)
	assert.Equal(t, "eval-rubric:"+rubric.SHA256, inference.invoked.SourceVersion)
	assert.Contains(t, inference.invoked.Variables["user"], `"rubric"`)
	assert.NotContains(t, inference.invoked.Variables, "system")
}

func TestEvalJudgeRejectsUnknownRubricAndTaskWithoutInvokingInference(t *testing.T) {
	t.Parallel()

	inference := &inferenceControllerFake{}
	app := newEvalJudgeTestApp(inference, &evalJudgeIdentityFake{})
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/eval/judge",
		bytes.NewBufferString(
			`{"case_id":"x","rubric_version":"attacker-prompt","question":"q",`+
				`"answer":"a","evidence":[{"ref":"S1","quote":"a","anchor":"book/1/page/1"}]}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(middleware.ServiceTokenHeader, testServiceToken)

	response, err := app.Test(request)
	require.NoError(t, err)
	defer response.Body.Close()

	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	assert.Empty(t, inference.invoked.TaskKey)

	request = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/eval/judge",
		bytes.NewBufferString(
			`{"case_id":"x","rubric_version":"groundedness-v1","question":"q",`+
				`"answer":"a","evidence":[{"ref":"S1","quote":"a","anchor":"book/1/page/1"}],`+
				`"task_key":"attacker-task"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(middleware.ServiceTokenHeader, testServiceToken)

	response, err = app.Test(request)
	require.NoError(t, err)
	defer response.Body.Close()

	assert.Equal(t, http.StatusBadRequest, response.StatusCode)
	assert.Empty(t, inference.invoked.TaskKey)
}

func TestEvalJudgeBudgetFailureIsTransparent(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().UTC().Add(time.Hour)
	inference := &inferenceControllerFake{invokeErr: &entity.InferenceBudgetExceededError{
		RetryAfter: time.Minute,
		ResetAt:    resetAt,
		Window:     "daily",
	}}
	app := newEvalJudgeTestApp(inference, &evalJudgeIdentityFake{})
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/v1/eval/judge",
		bytes.NewBufferString(
			`{"case_id":"x","rubric_version":"groundedness-v1","question":"q",`+
				`"answer":"a","evidence":[{"ref":"S1","quote":"a","anchor":"book/1/page/1"}]}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(middleware.ServiceTokenHeader, testServiceToken)

	response, err := app.Test(request)
	require.NoError(t, err)
	defer response.Body.Close()

	assert.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	var payload map[string]any
	require.NoError(t, json.NewDecoder(response.Body).Decode(&payload))
	assert.Equal(t, "inference_budget_exceeded", payload["code"])
}

func newEvalJudgeTestApp(
	inference usecase.Inference,
	identity usecase.ServiceIdentity,
) *fiber.App {
	log := logger.New("error")
	controller := &V1{
		inference: inference,
		l:         log,
		v:         validator.New(validator.WithRequiredStructEnabled()),
	}
	app := fiber.New()
	app.Post(
		"/v1/eval/judge",
		middleware.RequireServicePrincipal(identity, entity.ServiceScopeRAGEvalRead, log),
		controller.evalJudge,
	)

	return app
}

type evalJudgeIdentityFake struct {
	usecase.ServiceIdentity
	denyScope bool
}

func (fake *evalJudgeIdentityFake) AuthenticateServiceToken(
	_ context.Context,
	rawToken, requiredScope string,
) (entity.ServiceAuthentication, error) {
	if rawToken != testServiceToken {
		return entity.ServiceAuthentication{
			Outcome: entity.ServiceAuthOutcomeMissing,
		}, entity.ErrInvalidServiceToken
	}
	if fake.denyScope {
		return entity.ServiceAuthentication{
			PrincipalName: "wrong-scope",
			Outcome:       entity.ServiceAuthOutcomeInsufficientScope,
		}, entity.ErrInsufficientServiceScope
	}

	return entity.ServiceAuthentication{ //nolint:gosec // UUIDs identify inert test fixtures, not credentials.
		PrincipalID:   "550e8400-e29b-41d4-a716-446655440010",
		PrincipalName: "rag-eval",
		TokenID:       "550e8400-e29b-41d4-a716-446655440011",
		Scopes:        []string{requiredScope},
		ExpiresAt:     time.Now().Add(time.Hour),
		Outcome:       entity.ServiceAuthOutcomeAllowed,
	}, nil
}

func (*evalJudgeIdentityFake) CreateServiceRequestAudit(
	context.Context,
	entity.ServiceRequestAudit,
) (string, error) {
	return "550e8400-e29b-41d4-a716-446655440012", nil
}

func (*evalJudgeIdentityFake) FinishServiceRequestAudit(context.Context, string, int) error {
	return nil
}
