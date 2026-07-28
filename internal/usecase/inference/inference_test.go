package inference

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedManifestCoversFrozenTaskClassesAndPolicies(t *testing.T) {
	t.Parallel()

	manifests, err := promptManifests()
	require.NoError(t, err)
	require.NotEmpty(t, manifests)

	classes := make(map[string]bool)
	versions := make(map[string]string)

	for i := range manifests {
		classes[manifests[i].TaskClass] = true
		versions[manifests[i].TaskKey] = manifests[i].PromptVersion

		switch manifests[i].TaskClass {
		case entity.InferenceTaskClassRewrite:
			if !manifests[i].PersistentEnrichment {
				assert.Equal(t, 24*60*60, manifests[i].CacheTTLSeconds)
			}
		case entity.InferenceTaskClassRerank:
			assert.Equal(t, 60*60, manifests[i].CacheTTLSeconds)
		case entity.InferenceTaskClassEmbed:
			assert.Equal(t, 30*24*60*60, manifests[i].CacheTTLSeconds)
		case entity.InferenceTaskClassAnswer:
			assert.Equal(t, 10*60, manifests[i].CacheTTLSeconds)
		case entity.InferenceTaskClassJudge:
			assert.Zero(t, manifests[i].CacheTTLSeconds)
		}
	}

	assert.Equal(t, map[string]bool{
		entity.InferenceTaskClassRewrite: true,
		entity.InferenceTaskClassRerank:  true,
		entity.InferenceTaskClassEmbed:   true,
		entity.InferenceTaskClassAnswer:  true,
		entity.InferenceTaskClassJudge:   true,
	}, classes)
	assert.Equal(t, "mentions_v2", versions["langextract-mentions"])
	assert.Equal(t, "terms_v2", versions["langextract-terms"])
	assert.Equal(t, "citations_v3", versions["langextract-citations"])
	assert.Equal(t, "relations_v1", versions["langextract-relations"])
	assert.Equal(t, "reader-summary-v1", versions["reader-summary"])
	assert.Equal(t, "catalog-translation-v1", versions["catalog-translation"])
}

func TestRegisterPromptRejectsIncompleteImmutableManifest(t *testing.T) {
	t.Parallel()

	uc, err := New(&helperRepo{}, &helperProvider{}, Options{
		CacheSeed: "u0-register-prompt-test-cache-seed-at-least-thirty-two-bytes",
	})
	require.NoError(t, err)

	base := entity.InferencePromptManifest{
		TaskKey: "test-prompt", TaskClass: entity.InferenceTaskClassAnswer,
		PromptVersion:         "test-prompt-v1",
		Messages:              []entity.InferenceMessage{{Role: "user", Content: "{{.user}}"}},
		ResponseSchemaVersion: "test-prompt-v1",
		ResponseSchema:        entity.RawJSON(`{"type":"object"}`),
	}

	invalidClass := base
	invalidClass.TaskClass = "ad-hoc"
	require.ErrorIs(t, uc.RegisterPrompt(t.Context(), invalidClass), entity.ErrInferenceRegistryConflict)

	missingSchema := base
	missingSchema.ResponseSchema = nil
	require.ErrorIs(t, uc.RegisterPrompt(t.Context(), missingSchema), entity.ErrInferenceRegistryConflict)
}

//nolint:paralleltest // The test intentionally verifies process-global Prometheus series cleanup.
func TestBudgetMetricsClearStaleEnforceSeries(t *testing.T) {
	dailyCap, monthlyCap := int64(100), int64(1_000)
	uc := &UseCase{now: time.Now}

	uc.recordBudgetMetrics(&entity.InferenceBudgetStatus{
		Mode: "enforce", DailyCapNanoUSD: &dailyCap, MonthlyCapNanoUSD: &monthlyCap,
		DailyUsedNanoUSD: 80, MonthlyUsedNanoUSD: 800,
	})
	assert.InDelta(
		t,
		0.8,
		testutil.ToFloat64(inferenceBudgetRatio.WithLabelValues("daily", "enforce")),
		0.0001,
	)

	uc.recordBudgetMetrics(&entity.InferenceBudgetStatus{Mode: "disabled"})
	assert.Zero(
		t,
		testutil.ToFloat64(inferenceBudgetRatio.WithLabelValues("daily", "enforce")),
	)
	assert.Zero(
		t,
		testutil.ToFloat64(inferenceBudgetRatio.WithLabelValues("monthly", "enforce")),
	)
}

//nolint:gosec // Test uses variable names and dummy cache material, never a live secret.
func TestInitializeSynchronizesExactSumoPodCatalogPrice(t *testing.T) {
	t.Parallel()

	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Empty(t, request.Header.Get("Authorization"))

		if _, writeErr := w.Write([]byte(`[
				{"model_name":"other","input_cost_per_token":9,"output_cost_per_token":9},
				{"model_name":"glm-5.1","input_cost_per_token":"0.0000001","output_cost_per_token":0.00000032}
			]`)); writeErr != nil {
			t.Error(writeErr)
		}
	}))
	defer catalog.Close()

	registry := &catalogCaptureRepo{}
	uc, err := New(registry, &helperProvider{}, Options{
		Driver:             "openai-compatible",
		PrimaryCatalogURL:  catalog.URL,
		PrimaryBaseURL:     "https://ai.sumopod.com/v1",
		PrimaryModel:       "glm-5.1",
		PrimaryAPIKeyEnv:   "U0_PRIMARY_CREDENTIAL_ENV",
		SecondaryBaseURL:   "https://api.deepseek.com",
		SecondaryModel:     "deepseek-v4-flash",
		SecondaryAPIKeyEnv: "U0_SECONDARY_CREDENTIAL_ENV",
		CacheSeed:          "u0-catalog-test-cache-seed-at-least-thirty-two-bytes",
		Timeout:            time.Second,
		MaxOutputTokens:    100,
	})
	require.NoError(t, err)
	require.NoError(t, uc.Initialize(t.Context()))
	require.NotEmpty(t, registry.routes)
	primary := registry.routes[0]
	assert.Equal(t, "sumopod", primary.ProviderKey)
	assert.Regexp(t, `^sumopod-catalog-[0-9a-f]{16}$`, primary.PriceVersion)
	assert.EqualValues(t, 100_000_000, primary.InputNanoPerMillion)
	assert.EqualValues(t, 100_000_000, primary.CachedNanoPerMillion)
	assert.EqualValues(t, 320_000_000, primary.OutputNanoPerMillion)
}

func TestCatalogPriceFailsClosed(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		payload string
	}{
		{name: "missing model", payload: `[{"model_name":"other","input_cost_per_token":1,"output_cost_per_token":1}]`},
		{name: "negative", payload: `[{"model_name":"glm-5.1","input_cost_per_token":-1,"output_cost_per_token":1}]`},
		{name: "invalid payload", payload: `{}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseCatalogPrice([]byte(testCase.payload), "glm-5.1")
			require.Error(t, err)
		})
	}
}

func TestRenderValidateUsageAndCostHelpers(t *testing.T) {
	t.Parallel()

	messages, err := renderMessages(
		entity.RawJSON(`[{"role":"system","content":"fixed"},{"role":"user","content":"{{.user}}"}]`),
		map[string]any{"user": "مرحبا"},
	)
	require.NoError(t, err)
	assert.Equal(t, "مرحبا", messages[1].Content)

	_, err = renderMessages(
		entity.RawJSON(`[{"role":"user","content":"{{.missing}}"}]`),
		map[string]any{},
	)
	require.Error(t, err)

	schema := entity.RawJSON(
		`{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`,
	)
	require.NoError(t, validateResponse(schema, `{"answer":"ok"}`))
	require.ErrorIs(t, validateResponse(schema, `{"wrong":true}`), entity.ErrInferenceSchemaInvalid)
	require.ErrorIs(t, validateResponse(schema, `not-json`), entity.ErrInferenceSchemaInvalid)
	assert.Equal(t, `{"answer":"ok"}`, cleanJSONOutput("```json\n{\"answer\":\"ok\"}\n```"))

	estimated := normalizedUsage(messages, entity.InferenceProviderResponse{Output: "answer"})
	assert.Equal(t, entity.InferenceUsageEstimated, estimated.Source)
	assert.Positive(t, estimated.InputTokens)
	assert.Positive(t, estimated.OutputTokens)
	reported := normalizedUsage(messages, entity.InferenceProviderResponse{
		Output: "answer", InputTokens: 10, CachedInputTokens: 2, OutputTokens: 3,
	})
	assert.Equal(t, entity.InferenceUsageProvider, reported.Source)

	route := entity.InferenceRoute{
		InputNanoPerMillion: 100_000_000, CachedNanoPerMillion: 10_000_000,
		OutputNanoPerMillion: 200_000_000,
	}
	cost, source := routeCost(route, reported, nil)
	assert.Equal(t, entity.InferenceCostRegistry, source)
	assert.EqualValues(t, 1_420, cost)

	providerCost := int64(321)
	cost, source = routeCost(route, reported, &providerCost)
	assert.EqualValues(t, providerCost, cost)
	assert.Equal(t, entity.InferenceCostProvider, source)
	assert.Equal(t, "1.234567890", nanoUSDString(1_234_567_890))
}

func TestCacheKeyVariesAcrossEverySafetyDimension(t *testing.T) {
	t.Parallel()

	uc, err := New(&helperRepo{}, &helperProvider{}, Options{
		CacheSeed: "u0-helper-cache-seed-at-least-thirty-two-bytes",
	})
	require.NoError(t, err)

	baseRoute := entity.InferenceRoute{
		TaskKey: "answer", ProviderKey: "p", ProviderModelID: "m1",
		PromptVersion: "p1", PromptSHA256: strings.Repeat("a", 64),
		ResponseSchemaVersion: "s1", ResponseSchemaSHA256: strings.Repeat("b", 64),
		MaxOutputTokens: 100, Temperature: 0.1,
	}
	baseInput := entity.InferenceInvoke{
		Variables: map[string]any{"user": "q"},
		CacheVary: map[string]any{
			"language": "id", "filter": "f1", "lens": "l1", "style": "compact",
		},
		IndexVersion: "i1", SourceVersion: "s1",
	}
	base, err := uc.cacheKey(baseRoute, baseInput)
	require.NoError(t, err)

	keys := map[string]bool{base: true}
	add := func(route entity.InferenceRoute, input entity.InferenceInvoke) {
		key, keyErr := uc.cacheKey(route, input)
		require.NoError(t, keyErr)
		assert.False(t, keys[key], "cache safety dimension collided")
		keys[key] = true
	}

	for _, mutate := range []func(*entity.InferenceRoute){
		func(route *entity.InferenceRoute) { route.TaskKey = "rewrite" },
		func(route *entity.InferenceRoute) { route.ProviderModelID = "m2" },
		func(route *entity.InferenceRoute) { route.PromptVersion = "p2" },
		func(route *entity.InferenceRoute) { route.PromptSHA256 = strings.Repeat("c", 64) },
		func(route *entity.InferenceRoute) { route.ResponseSchemaVersion = "s2" },
		func(route *entity.InferenceRoute) { route.Temperature = 0.2 },
	} {
		route := baseRoute
		mutate(&route)
		add(route, baseInput)
	}

	for _, field := range []string{"language", "filter", "lens", "style"} {
		input := cloneInferenceInput(baseInput)
		input.CacheVary[field] = "different"
		add(baseRoute, input)
	}

	input := cloneInferenceInput(baseInput)
	input.IndexVersion = "i2"
	add(baseRoute, input)
	input = cloneInferenceInput(baseInput)
	input.SourceVersion = "s2"
	add(baseRoute, input)
	input = cloneInferenceInput(baseInput)
	input.Variables["user"] = "different"
	add(baseRoute, input)
}

//nolint:gocritic // Test helper intentionally clones the complete immutable input value.
func cloneInferenceInput(input entity.InferenceInvoke) entity.InferenceInvoke {
	cloned := input

	cloned.Variables = make(map[string]any, len(input.Variables))

	maps.Copy(cloned.Variables, input.Variables)

	cloned.CacheVary = make(map[string]any, len(input.CacheVary))

	maps.Copy(cloned.CacheVary, input.CacheVary)

	return cloned
}

// These minimal implementations are only used for constructor/helper tests.
// The pipeline tests use the complete in-memory repo in inference_integration_test.go.
type helperRepo struct{ repo.InferenceRepo }

type helperProvider struct{}

type catalogCaptureRepo struct {
	repo.InferenceRepo
	routes []entity.InferenceRoute
}

func (*catalogCaptureRepo) SyncManifest(
	context.Context,
	[]entity.InferencePromptManifest,
) error {
	return nil
}

func (capture *catalogCaptureRepo) SyncRoutes(
	_ context.Context,
	routes []entity.InferenceRoute,
) error {
	capture.routes = routes

	return nil
}

func (*helperProvider) Chat(
	context.Context,
	entity.InferenceRoute,
	entity.InferenceProviderRequest,
) (entity.InferenceProviderResponse, error) {
	return entity.InferenceProviderResponse{}, nil
}

func (*helperProvider) Embed(
	context.Context,
	entity.InferenceRoute,
	entity.InferenceProviderRequest,
) (entity.InferenceProviderResponse, error) {
	return entity.InferenceProviderResponse{}, nil
}
