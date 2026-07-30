package v1

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferenceInternalRouteManifestFreezesA2Scopes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []struct {
		Method string
		Path   string
		Scope  string
	}{
		{fiber.MethodPost, "/inference/invoke", entity.ServiceScopeInferenceInvoke},
		{fiber.MethodPost, "/inference/sessions", entity.ServiceScopeInferenceInvoke},
		{fiber.MethodGet, "/inference/prompts", entity.ServiceScopePromptRegistryManage},
		{fiber.MethodPost, "/inference/prompts", entity.ServiceScopePromptRegistryManage},
		{fiber.MethodGet, "/inference/budget", entity.ServiceScopeInferenceBudgetManage},
		{fiber.MethodPatch, "/inference/budget", entity.ServiceScopeInferenceBudgetManage},
	}, InferenceInternalRouteManifest)
}

func TestInternalInferenceInvokeReturnsFullAttribution(t *testing.T) {
	t.Parallel()

	generation := entity.GenerationIdentity{
		RunID:   "44444444-4444-4444-8444-444444444444",
		ModelID: "deepseek-v4-flash", PromptVersion: "bookrag-answer-v1",
	}
	controller := &inferenceInternal{inference: &inferenceControllerFake{
		result: entity.InferenceResult{
			CallID: "55555555-5555-4555-8555-555555555555",
			Output: `{"answer":"ok","citations":[]}`, Generation: generation,
			Provider: "deepseek", Model: "deepseek-v4-flash",
			Prompt: "bookrag-answer-v1", Schema: "bookrag-answer-v1",
			Usage: entity.InferenceUsage{
				InputTokens: 12, CachedInputTokens: 2, OutputTokens: 3,
				Source: entity.InferenceUsageProvider,
			},
			Cost: entity.InferenceCost{
				NanoUSD: 2_000, USD: "0.000002000",
				Source: entity.InferenceCostRegistry, PriceVersion: "official-v1",
			},
			CacheStatus: "miss", Failover: true,
		},
	}}
	app := fiber.New()
	app.Post("/internal/inference/invoke", controller.invoke)

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/internal/inference/invoke",
		bytes.NewBufferString(`{"task_key":"bookrag-answer","variables":{"user":"question"}}`),
	)
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Equal(t, http.StatusOK, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	for _, fragment := range []string{
		`"run_id":"44444444-4444-4444-8444-444444444444"`,
		`"provider":"deepseek"`, `"model":"deepseek-v4-flash"`,
		`"prompt_version":"bookrag-answer-v1"`,
		`"response_schema_version":"bookrag-answer-v1"`,
		`"input_tokens":12`, `"cached_input_tokens":2`, `"output_tokens":3`,
		`"nano_usd":2000`, `"failover":true`,
	} {
		assert.Contains(t, string(body), fragment)
	}
}

func TestAdminBudgetPatchRequiresETagAndCreatesRevision(t *testing.T) {
	t.Parallel()

	daily, monthly := int64(100), int64(1000)
	fake := &inferenceControllerFake{
		budgetStatus: entity.InferenceBudgetStatus{Revision: 7},
		updatedBudget: entity.InferenceBudgetStatus{
			Revision: 8, Mode: "enforce",
			DailyCapNanoUSD: &daily, MonthlyCapNanoUSD: &monthly,
		},
	}
	controller := &V1{inference: fake}
	app := fiber.New()
	app.Use(func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "44444444-4444-4444-8444-444444444444")

		return ctx.Next()
	})
	app.Patch("/v1/admin/inference/budget", controller.adminUpdateInferenceBudget)

	body := `{"mode":"enforce","daily_cap_nano_usd":100,` +
		`"monthly_cap_nano_usd":1000,"reason":"operator override"}`

	missing := httptest.NewRequestWithContext(
		t.Context(), http.MethodPatch, "/v1/admin/inference/budget", bytes.NewBufferString(body),
	)
	missing.Header.Set("Content-Type", "application/json")
	response, err := app.Test(missing)
	require.NoError(t, err)
	assert.Equal(t, http.StatusPreconditionRequired, response.StatusCode)
	require.NoError(t, response.Body.Close())

	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPatch, "/v1/admin/inference/budget", bytes.NewBufferString(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", `"inference-budget-7"`)
	response, err = app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, `"inference-budget-8"`, response.Header.Get("ETag"))
	assert.Equal(t, int64(7), fake.expectedRevision)
	assert.Equal(t, "operator override", fake.patch.Reason)
}

type inferenceControllerFake struct {
	usecase.Inference
	result           entity.InferenceResult
	invokeErr        error
	budgetStatus     entity.InferenceBudgetStatus
	updatedBudget    entity.InferenceBudgetStatus
	expectedRevision int64
	patch            entity.InferenceBudgetPatch
}

func (f *inferenceControllerFake) Invoke(
	context.Context,
	entity.InferenceInvoke,
) (entity.InferenceResult, error) {
	return f.result, f.invokeErr
}

func (f *inferenceControllerFake) Budget(
	context.Context,
) (entity.InferenceBudgetStatus, error) {
	return f.budgetStatus, nil
}

func (f *inferenceControllerFake) UpdateBudget(
	_ context.Context,
	_ string,
	expectedRevision int64,
	patch entity.InferenceBudgetPatch,
) (entity.InferenceBudgetStatus, error) {
	f.expectedRevision = expectedRevision
	f.patch = patch

	return f.updatedBudget, nil
}

func (f *inferenceControllerFake) Usage(
	context.Context,
	time.Time,
	time.Time,
	string,
) ([]entity.InferenceUsageRow, error) {
	return nil, nil
}
