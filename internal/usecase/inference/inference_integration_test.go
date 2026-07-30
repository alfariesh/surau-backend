package inference_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo/webapi"
	"github.com/alfariesh/surau-backend/internal/requestmeta"
	"github.com/alfariesh/surau-backend/internal/usecase/inference"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const testCacheSeed = "u0-test-cache-seed-at-least-thirty-two-bytes"

var (
	errMemoryAttemptNotFound = errors.New("memory inference attempt not found")
	errMemoryCallNotFound    = errors.New("memory inference call not found")
)

func TestTwoProviderFailoverCarriesCompleteAttribution(t *testing.T) {
	testCases := []struct {
		name           string
		primaryHandler http.HandlerFunc
		timeoutMS      int
	}{
		{
			name: "503",
			primaryHandler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "temporary", http.StatusServiceUnavailable)
			},
			timeoutMS: 500,
		},
		{
			name: "timeout",
			primaryHandler: func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(150 * time.Millisecond)
				writeCompletion(w, `{"answer":"late","citations":[]}`)
			},
			timeoutMS: 30,
		},
		{
			name: "empty",
			primaryHandler: func(w http.ResponseWriter, _ *http.Request) {
				if _, err := w.Write([]byte(`{"choices":[],"usage":{}}`)); err != nil {
					return
				}
			},
			timeoutMS: 500,
		},
		{
			name: "schema invalid",
			primaryHandler: func(w http.ResponseWriter, _ *http.Request) {
				writeCompletion(w, `{"wrong":true}`)
			},
			timeoutMS: 500,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			primary := httptest.NewServer(testCase.primaryHandler)
			defer primary.Close()

			secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeCompletion(w, `{"answer":"ok [1]","citations":[]}`)
			}))
			defer secondary.Close()

			t.Setenv("U0_PRIMARY_TEST_KEY", "primary-secret")
			t.Setenv("U0_SECONDARY_TEST_KEY", "secondary-secret")

			store := newMemoryInferenceRepo(
				testRoute(primary.URL, "primary", "model-primary", 1, testCase.timeoutMS),
				testRoute(secondary.URL, "secondary", "model-secondary", 2, 500),
			)
			exporter := tracetest.NewInMemoryExporter()
			tracerProvider := sdktrace.NewTracerProvider(
				sdktrace.WithSyncer(exporter),
				sdktrace.WithSampler(sdktrace.AlwaysSample()),
			)
			previous := otel.GetTracerProvider()

			otel.SetTracerProvider(tracerProvider)
			t.Cleanup(func() {
				otel.SetTracerProvider(previous)
				assert.NoError(t, tracerProvider.Shutdown(context.Background()))
			})

			uc, err := inference.New(store, webapi.NewInferenceProvider(), inference.Options{
				CacheSeed: testCacheSeed,
			})
			require.NoError(t, err)
			result, err := uc.Invoke(t.Context(), entity.InferenceInvoke{
				TaskKey:   "test-answer",
				Variables: map[string]any{"user": "question"},
			})
			require.NoError(t, err)
			assert.Equal(t, "secondary", result.Provider)
			assert.Equal(t, "model-secondary", result.Model)
			assert.True(t, result.Failover)
			require.Len(t, store.attempts, 2)
			assert.Equal(t, "failed", store.attempts[0].Status)
			assert.Equal(t, "succeeded", store.attempts[1].Status)
			assert.Equal(t, store.attempts[1].Generation.ID, result.Generation.RunID)
			assert.Equal(t, store.attempts[1].Generation.ID, *store.calls[0].GenerationID)
			assert.Positive(t, store.attempts[0].Usage.InputTokens)
			assert.Positive(t, store.attempts[0].Usage.OutputTokens)
			assert.Positive(t, store.attempts[0].CostNanoUSD)
			assert.Positive(t, result.Cost.NanoUSD)

			assertAttributionSpans(t, exporter.GetSpans(), result.Generation.RunID)
		})
	}
}

func TestProvider400DoesNotFailOver(t *testing.T) {
	var secondaryCalls atomic.Int64

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondaryCalls.Add(1)
		writeCompletion(w, `{"answer":"must not run","citations":[]}`)
	}))
	defer secondary.Close()

	t.Setenv("U0_PRIMARY_TEST_KEY", "primary-secret")
	t.Setenv("U0_SECONDARY_TEST_KEY", "secondary-secret")

	store := newMemoryInferenceRepo(
		testRoute(primary.URL, "primary", "model-primary", 1, 500),
		testRoute(secondary.URL, "secondary", "model-secondary", 2, 500),
	)
	uc, err := inference.New(store, webapi.NewInferenceProvider(), inference.Options{
		CacheSeed: testCacheSeed,
	})
	require.NoError(t, err)

	_, err = uc.Invoke(t.Context(), entity.InferenceInvoke{
		TaskKey: "test-answer", Variables: map[string]any{"user": "bad"},
	})
	require.ErrorIs(t, err, entity.ErrInferenceProviderFailure)
	assert.Len(t, store.attempts, 1)
	assert.Zero(t, secondaryCalls.Load())
	assert.Equal(t, "failed", store.calls[0].Status)
}

func TestBothProvidersFailWithStableUnavailableError(t *testing.T) {
	failing := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusServiceUnavailable)
		}))
	}
	primary, secondary := failing(), failing()

	defer primary.Close()
	defer secondary.Close()

	t.Setenv("U0_PRIMARY_TEST_KEY", "secret")
	t.Setenv("U0_SECONDARY_TEST_KEY", "secret")

	store := newMemoryInferenceRepo(
		testRoute(primary.URL, "primary", "model-primary", 1, 500),
		testRoute(secondary.URL, "secondary", "model-secondary", 2, 500),
	)
	uc, err := inference.New(store, webapi.NewInferenceProvider(), inference.Options{
		CacheSeed: testCacheSeed,
	})
	require.NoError(t, err)

	_, err = uc.Invoke(t.Context(), entity.InferenceInvoke{
		TaskKey: "test-answer", Variables: map[string]any{"user": "question"},
	})
	require.ErrorIs(t, err, entity.ErrInferenceProviderFailure)
	require.Len(t, store.attempts, 2)
	assert.Equal(t, "failed", store.attempts[0].Status)
	assert.Equal(t, "failed", store.attempts[1].Status)
}

func TestBudgetRejectionTraceIsCompleteAndNeverCallsProvider(t *testing.T) {
	var providerCalls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		writeCompletion(w, `{"answer":"must not run","citations":[]}`)
	}))
	defer server.Close()

	t.Setenv("U0_PRIMARY_TEST_KEY", "secret")

	store := newMemoryInferenceRepo(
		testRoute(server.URL, "primary", "model-primary", 1, 500),
	)
	store.budgetErr = &entity.InferenceBudgetExceededError{
		Window: "daily", RetryAfter: time.Hour, ResetAt: time.Now().Add(time.Hour),
	}
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	previous := otel.GetTracerProvider()

	otel.SetTracerProvider(tracerProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		assert.NoError(t, tracerProvider.Shutdown(context.Background()))
	})

	uc, err := inference.New(store, webapi.NewInferenceProvider(), inference.Options{
		CacheSeed: testCacheSeed,
	})
	require.NoError(t, err)

	var exceeded *entity.InferenceBudgetExceededError

	_, err = uc.Invoke(t.Context(), entity.InferenceInvoke{
		TaskKey: "test-answer", Variables: map[string]any{"user": "question"},
	})

	require.ErrorAs(t, err, &exceeded)
	assert.Zero(t, providerCalls.Load())
	assert.Empty(t, store.attempts)
	require.Len(t, store.calls, 1)
	assert.Equal(t, "budget_rejected", store.calls[0].Status)

	var found bool

	for _, span := range exporter.GetSpans() {
		if span.Name != "inference.test-answer" {
			continue
		}

		found = true

		attrs := make(map[string]any)

		for _, attr := range span.Attributes {
			attrs[string(attr.Key)] = attr.Value.AsInterface()
		}

		for _, key := range []string{
			"inference.task_key", "inference.task_class", "inference.provider",
			"inference.model", "inference.prompt_version", "inference.prompt_sha256",
			"inference.response_schema_version", "inference.response_schema_sha256",
			"inference.generation_run_id", "inference.generation_status",
			"inference.input_tokens", "inference.cached_input_tokens",
			"inference.output_tokens", "inference.cost_nano_usd",
			"inference.cost_usd", "inference.usage_source", "inference.cost_source",
			"inference.cache_status", "inference.outcome", "inference.failover",
			"surau.request_id", "surau.trace_id",
		} {
			assert.Contains(t, attrs, key, "budget span is missing %s", key)
		}

		assert.Equal(t, "not_created_budget_rejected", attrs["inference.generation_status"])
	}

	assert.True(t, found)
}

func TestCacheHitSurvivesBudgetRejectionAndPreservesGeneration(t *testing.T) {
	var providerCalls atomic.Int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		writeCompletion(w, `{"answer":"cached [1]","citations":[]}`)
	}))
	defer server.Close()

	t.Setenv("U0_PRIMARY_TEST_KEY", "secret")

	route := testRoute(server.URL, "primary", "model-primary", 1, 500)
	route.CacheTTLSeconds = 600
	store := newMemoryInferenceRepo(route)
	exporter := tracetest.NewInMemoryExporter()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	previous := otel.GetTracerProvider()

	otel.SetTracerProvider(tracerProvider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		assert.NoError(t, tracerProvider.Shutdown(context.Background()))
	})

	uc, err := inference.New(store, webapi.NewInferenceProvider(), inference.Options{
		CacheSeed: testCacheSeed,
	})
	require.NoError(t, err)

	input := entity.InferenceInvoke{
		TaskKey:   "test-answer",
		Variables: map[string]any{"user": "same"},
		CacheVary: map[string]any{
			"language": "id", "filter": "book:1", "lens": "default", "style": "compact",
		},
		IndexVersion: "index-v1", SourceVersion: "source-v1",
	}
	first, err := uc.Invoke(t.Context(), input)
	require.NoError(t, err)
	require.Len(t, store.cache, 1)

	for _, entry := range store.cache {
		assert.NotContains(t, entry.Ciphertext, "cached")
		assert.NotContains(t, entry.Ciphertext, "same")
	}

	store.budgetErr = &entity.InferenceBudgetExceededError{
		RetryAfter: time.Hour, ResetAt: time.Now().Add(time.Hour), Window: "daily",
	}
	cacheContext := requestmeta.WithRequestID(t.Context(), "request-u0-cache-hit")
	second, err := uc.Invoke(cacheContext, input)
	require.NoError(t, err)
	assert.Equal(t, "hit", second.CacheStatus)
	assert.Zero(t, second.Cost.NanoUSD)
	assert.Equal(t, route.PriceVersion, second.Cost.PriceVersion)
	assert.Equal(t, first.Generation, second.Generation)
	assert.EqualValues(t, 1, providerCalls.Load())
	require.Len(t, store.calls, 2)
	require.NotNil(t, store.calls[1].RequestID)
	assert.Equal(t, "request-u0-cache-hit", *store.calls[1].RequestID)

	assertCacheHitSpan(t, exporter.GetSpans(), "request-u0-cache-hit", route.PriceVersion)

	var exceeded *entity.InferenceBudgetExceededError

	input.CacheVary["language"] = "en"
	_, err = uc.Invoke(t.Context(), input)

	require.ErrorAs(t, err, &exceeded)
	assert.EqualValues(t, 1, providerCalls.Load())

	store.budgetErr = nil
	input.CacheVary["language"] = "id"

	for key, entry := range store.cache {
		entry.Ciphertext += "tampered"
		store.cache[key] = entry
	}

	_, err = uc.Invoke(t.Context(), input)
	require.NoError(t, err)
	assert.EqualValues(t, 2, providerCalls.Load(), "tampered ciphertext is a safe miss")

	wrongKeyUC, err := inference.New(store, webapi.NewInferenceProvider(), inference.Options{
		CacheSeed: "a-different-u0-cache-key-at-least-thirty-two-bytes",
	})
	require.NoError(t, err)
	_, err = wrongKeyUC.Invoke(t.Context(), input)
	require.NoError(t, err)
	assert.EqualValues(t, 3, providerCalls.Load(), "wrong cache key is a safe miss")
}

func assertCacheHitSpan(
	t *testing.T,
	spans tracetest.SpanStubs,
	requestID string,
	priceVersion string,
) {
	t.Helper()

	for i := range spans {
		if spans[i].Name != "inference.test-answer" {
			continue
		}

		attrs := make(map[string]any)
		for _, attr := range spans[i].Attributes {
			attrs[string(attr.Key)] = attr.Value.AsInterface()
		}

		if attrs["inference.cache_status"] != "hit" {
			continue
		}

		for _, key := range []string{
			"inference.task_key", "inference.task_class", "inference.provider",
			"inference.model", "inference.prompt_version", "inference.prompt_sha256",
			"inference.response_schema_version", "inference.response_schema_sha256",
			"inference.price_version", "inference.generation_run_id",
			"inference.input_tokens", "inference.cached_input_tokens",
			"inference.output_tokens", "inference.cost_nano_usd",
			"inference.cost_usd", "inference.usage_source", "inference.cost_source",
			"inference.cache_status", "inference.outcome", "surau.request_id",
			"surau.trace_id",
		} {
			assert.Contains(t, attrs, key, "cache-hit span is missing %s", key)
		}

		assert.Equal(t, requestID, attrs["surau.request_id"])
		assert.Equal(t, priceVersion, attrs["inference.price_version"])
		assert.Equal(t, int64(0), attrs["inference.cost_nano_usd"])

		return
	}

	t.Fatal("cache-hit inference span not found")
}

func TestPinnedSessionNeverChangesProviderAfterSuccess(t *testing.T) {
	var (
		primaryFails   atomic.Bool
		secondaryCalls atomic.Int64
	)

	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if primaryFails.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)

			return
		}

		writeCompletion(w, `{"answer":"first","citations":[]}`)
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondaryCalls.Add(1)
		writeCompletion(w, `{"answer":"secondary","citations":[]}`)
	}))
	defer secondary.Close()

	t.Setenv("U0_PRIMARY_TEST_KEY", "secret")
	t.Setenv("U0_SECONDARY_TEST_KEY", "secret")

	store := newMemoryInferenceRepo(
		testRoute(primary.URL, "primary", "model-primary", 1, 500),
		testRoute(secondary.URL, "secondary", "model-secondary", 2, 500),
	)
	uc, err := inference.New(store, webapi.NewInferenceProvider(), inference.Options{
		CacheSeed: testCacheSeed,
	})
	require.NoError(t, err)
	session, err := uc.CreateSession(t.Context(), "test-answer")
	require.NoError(t, err)
	_, err = uc.Invoke(t.Context(), entity.InferenceInvoke{
		TaskKey: "test-answer", SessionID: session.ID,
		Variables: map[string]any{"user": "one"},
	})
	require.NoError(t, err)
	primaryFails.Store(true)

	_, err = uc.Invoke(t.Context(), entity.InferenceInvoke{
		TaskKey: "test-answer", SessionID: session.ID,
		Variables: map[string]any{"user": "two"},
	})
	require.ErrorIs(t, err, entity.ErrInferenceSessionPinned)
	assert.Zero(t, secondaryCalls.Load())
	assert.Equal(t, "failed", store.sessions[session.ID].Status)
}

func testRoute(baseURL, provider, model string, priority, timeoutMS int) entity.InferenceRoute {
	keyEnv := "U0_PRIMARY_TEST_KEY"
	if priority == 2 {
		keyEnv = "U0_SECONDARY_TEST_KEY"
	}

	return entity.InferenceRoute{
		TaskKey: "test-answer", TaskClass: entity.InferenceTaskClassAnswer,
		OutputKind: "structured", Priority: priority, ProviderKey: provider,
		BaseURL: baseURL, APIKeyEnv: keyEnv, ModelKey: model,
		ProviderModelID: model, PriceVersion: "test-price-v1",
		InputNanoPerMillion: 100_000_000, CachedNanoPerMillion: 10_000_000,
		OutputNanoPerMillion: 200_000_000, SupportsJSON: true,
		TimeoutMS: timeoutMS, MaxOutputTokens: 50, Temperature: 0,
		PromptVersion: "test-answer-v1", PromptSHA256: strings.Repeat("a", 64),
		MessagesTemplate: entity.RawJSON(
			`[{"role":"system","content":"safe system"},{"role":"user","content":"{{.user}}"}]`,
		),
		ResponseSchemaVersion: "test-answer-v1",
		ResponseSchemaSHA256:  strings.Repeat("b", 64),
		ResponseSchema: entity.RawJSON(
			`{"type":"object","required":["answer","citations"],"properties":{"answer":{"type":"string"},"citations":{"type":"array"}}}`,
		),
	}
}

func writeCompletion(w http.ResponseWriter, output string) {
	w.Header().Set("Content-Type", "application/json")

	if _, err := w.Write([]byte(
		`{"choices":[{"message":{"content":` + quoteJSON(output) +
			`}}],"usage":{"prompt_tokens":11,"completion_tokens":3,` +
			`"prompt_tokens_details":{"cached_tokens":2}}}`,
	)); err != nil {
		return
	}
}

func quoteJSON(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)

	return `"` + value + `"`
}

func assertAttributionSpans(
	t *testing.T,
	spans tracetest.SpanStubs,
	finalGenerationID string,
) {
	t.Helper()
	require.GreaterOrEqual(t, len(spans), 3)

	var logicalFound, finalAttemptFound bool

	for i := range spans {
		attrs := make(map[string]any)
		for _, attr := range spans[i].Attributes {
			attrs[string(attr.Key)] = attr.Value.AsInterface()
		}

		if strings.HasPrefix(spans[i].Name, "inference.provider.") {
			for _, key := range []string{
				"inference.task_key", "inference.task_class", "inference.provider",
				"inference.model", "inference.prompt_version", "inference.prompt_sha256",
				"inference.response_schema_version", "inference.generation_run_id",
				"inference.input_tokens", "inference.cached_input_tokens",
				"inference.output_tokens", "inference.cost_nano_usd", "inference.cost_usd",
				"inference.usage_source", "inference.cost_source", "inference.failover",
				"inference.cache_status", "inference.outcome", "surau.request_id",
				"surau.trace_id",
			} {
				assert.Contains(t, attrs, key, "provider attempt is missing %s", key)
			}

			if attrs["inference.generation_run_id"] == finalGenerationID {
				finalAttemptFound = true
			}
		}

		if spans[i].Name == "inference.test-answer" {
			logicalFound = true

			for _, key := range []string{
				"inference.task_key", "inference.model", "inference.prompt_version",
				"inference.generation_run_id", "inference.input_tokens",
				"inference.cached_input_tokens", "inference.output_tokens",
				"inference.cost_nano_usd", "inference.cost_usd",
				"inference.usage_source", "inference.cost_source",
				"inference.cache_status", "inference.outcome",
				"surau.request_id", "surau.trace_id",
			} {
				assert.Contains(t, attrs, key, "logical call is missing %s", key)
			}
		}
	}

	assert.True(t, logicalFound)
	assert.True(t, finalAttemptFound)
}

type memoryInferenceRepo struct {
	mu        sync.Mutex
	routes    []entity.InferenceRoute
	calls     []entity.InferenceCall
	attempts  []entity.InferenceAttempt
	cache     map[string]entity.InferenceCacheEntry
	sessions  map[string]entity.InferenceSession
	budgetErr error
}

func newMemoryInferenceRepo(routes ...entity.InferenceRoute) *memoryInferenceRepo {
	return &memoryInferenceRepo{
		routes: routes, cache: make(map[string]entity.InferenceCacheEntry),
		sessions: make(map[string]entity.InferenceSession),
	}
}

func (r *memoryInferenceRepo) SyncManifest(context.Context, []entity.InferencePromptManifest) error {
	return nil
}

func (r *memoryInferenceRepo) SyncRoutes(context.Context, []entity.InferenceRoute) error {
	return nil
}

func (r *memoryInferenceRepo) SyncEphemeralModels(
	context.Context,
	[]entity.InferenceRoute,
) error {
	return nil
}

func (r *memoryInferenceRepo) ResolveRoutes(
	_ context.Context,
	taskKey, sessionID string,
) ([]entity.InferenceRoute, error) {
	var pin entity.InferenceSession

	r.mu.Lock()
	defer r.mu.Unlock()

	if sessionID != "" {
		pin = r.sessions[sessionID]
	}

	result := make([]entity.InferenceRoute, 0, len(r.routes))
	for i := range r.routes {
		if r.routes[i].TaskKey != taskKey {
			continue
		}

		if pin.PinnedProviderKey != nil &&
			(*pin.PinnedProviderKey != r.routes[i].ProviderKey ||
				*pin.PinnedModelKey != r.routes[i].ModelKey) {
			continue
		}

		result = append(result, r.routes[i])
	}

	if len(result) == 0 {
		return nil, entity.ErrInferenceRouteMissing
	}

	return result, nil
}

//nolint:gocritic // Test double mirrors the production repository value contract.
func (r *memoryInferenceRepo) CreateCall(_ context.Context, call entity.InferenceCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls = append(r.calls, call)

	return nil
}

//nolint:gocritic // Test double mirrors the production repository value contract.
func (r *memoryInferenceRepo) CreateAttempt(
	_ context.Context,
	attempt entity.InferenceAttempt,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.attempts = append(r.attempts, attempt)

	return nil
}

//nolint:gocritic // Test double mirrors the production repository value contract.
func (r *memoryInferenceRepo) FinishAttempt(
	_ context.Context,
	attempt entity.InferenceAttempt,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.attempts {
		if r.attempts[i].ID == attempt.ID {
			r.attempts[i] = attempt

			return nil
		}
	}

	return errMemoryAttemptNotFound
}

//nolint:gocritic // Test double mirrors the production repository value contract.
func (r *memoryInferenceRepo) FinishCall(_ context.Context, call entity.InferenceCall) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.calls {
		if r.calls[i].ID == call.ID {
			r.calls[i] = call

			return nil
		}
	}

	return errMemoryCallNotFound
}

func (r *memoryInferenceRepo) ReserveBudget(
	_ context.Context,
	callID string,
	_ int64,
) (entity.InferenceBudgetStatus, error) {
	var exceeded *entity.InferenceBudgetExceededError

	r.mu.Lock()
	defer r.mu.Unlock()

	if errors.As(r.budgetErr, &exceeded) {
		for i := range r.calls {
			if r.calls[i].ID == callID {
				r.calls[i].Status = "budget_rejected"

				break
			}
		}
	}

	return entity.InferenceBudgetStatus{Mode: "baseline"}, r.budgetErr
}
func (r *memoryInferenceRepo) SettleBudget(context.Context, string) error { return nil }
func (r *memoryInferenceRepo) GetCache(
	_ context.Context,
	key string,
) (entity.InferenceCacheEntry, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.cache[key]

	return entry, ok, nil
}

//nolint:gocritic // Test double mirrors the production repository value contract.
func (r *memoryInferenceRepo) PutCache(
	_ context.Context,
	entry entity.InferenceCacheEntry,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.cache[entry.CacheKey] = entry

	return nil
}

func (r *memoryInferenceRepo) CreateSession(
	_ context.Context,
	taskKey string,
) (entity.InferenceSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	session := entity.InferenceSession{
		ID: "44444444-4444-4444-8444-444444444444", TaskKey: taskKey, Status: "open",
	}
	r.sessions[session.ID] = session

	return session, nil
}

func (r *memoryInferenceRepo) PinSession(
	_ context.Context,
	sessionID, providerKey, modelKey string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	session := r.sessions[sessionID]
	if session.PinnedProviderKey != nil &&
		(*session.PinnedProviderKey != providerKey || *session.PinnedModelKey != modelKey) {
		return entity.ErrInferenceSessionPinned
	}

	session.PinnedProviderKey = &providerKey
	session.PinnedModelKey = &modelKey
	session.Status = "pinned"
	r.sessions[sessionID] = session

	return nil
}

func (r *memoryInferenceRepo) FailSession(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	session, ok := r.sessions[sessionID]
	if !ok {
		return entity.ErrInferenceSessionPinned
	}

	session.Status = "failed"
	r.sessions[sessionID] = session

	return nil
}

func (r *memoryInferenceRepo) Budget(
	context.Context,
	time.Time,
) (entity.InferenceBudgetStatus, error) {
	return entity.InferenceBudgetStatus{Mode: "baseline"}, nil
}

func (r *memoryInferenceRepo) UpdateBudget(
	context.Context,
	string,
	int64,
	entity.InferenceBudgetPatch,
) (entity.InferenceBudgetStatus, error) {
	return entity.InferenceBudgetStatus{}, nil
}

func (r *memoryInferenceRepo) Usage(
	context.Context,
	time.Time,
	time.Time,
	string,
) ([]entity.InferenceUsageRow, error) {
	return nil, nil
}

func (r *memoryInferenceRepo) Registry(
	context.Context,
) ([]entity.InferenceRoute, error) {
	return r.routes, nil
}
