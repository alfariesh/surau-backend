package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferenceProviderChatNormalizesUsageAndProviderCost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var payload map[string]any

		assert.Equal(t, "/chat/completions", request.URL.Path)
		assert.Equal(t, "Bearer secret", request.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		assert.Equal(t, "model-one", payload["model"])
		assert.Equal(t, map[string]any{"type": "json_object"}, payload["response_format"])

		if _, writeErr := w.Write([]byte(`{
				"choices":[{"message":{"content":"{\"ok\":true}"}}],
				"usage":{
				"prompt_tokens":12,
				"completion_tokens":4,
				"prompt_tokens_details":{"cached_tokens":3},
					"cost_nano_usd":777
				}
			}`)); writeErr != nil {
			t.Error(writeErr)
		}
	}))
	defer server.Close()

	t.Setenv("U0_PROVIDER_TEST_CREDENTIAL", "secret")

	provider := NewInferenceProvider()

	result, err := provider.Chat(t.Context(), entity.InferenceRoute{
		BaseURL: server.URL, APIKeyEnv: "U0_PROVIDER_TEST_CREDENTIAL",
		ProviderModelID: "model-one", TimeoutMS: 500,
	}, entity.InferenceProviderRequest{
		Messages:   []entity.InferenceMessage{{Role: "user", Content: "hello"}},
		JSONOutput: true,
	})
	require.NoError(t, err)
	assert.Equal(t, `{"ok":true}`, result.Output)
	assert.EqualValues(t, 12, result.InputTokens)
	assert.EqualValues(t, 3, result.CachedInputTokens)
	assert.EqualValues(t, 4, result.OutputTokens)
	require.NotNil(t, result.CostNanoUSD)
	assert.EqualValues(t, 777, *result.CostNanoUSD)
}

//nolint:gosec // Test-only environment-variable names contain no credential value.
func TestInferenceProviderEmbeddingsStayBehindRegistryCapability(t *testing.T) {
	var providerErr *entity.InferenceProviderError

	provider := NewInferenceProvider()
	_, err := provider.Embed(t.Context(), entity.InferenceRoute{}, entity.InferenceProviderRequest{})

	require.ErrorAs(t, err, &providerErr)
	assert.False(t, providerErr.Retryable)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "/embeddings", request.URL.Path)

		if _, writeErr := w.Write([]byte(`{
				"data":[{"embedding":[0.25,-0.5],"index":0}],
				"usage":{"prompt_tokens":8}
			}`)); writeErr != nil {
			t.Error(writeErr)
		}
	}))
	defer server.Close()

	t.Setenv("U0_EMBED_TEST_CREDENTIAL", "secret")
	result, err := provider.Embed(t.Context(), entity.InferenceRoute{
		BaseURL: server.URL, APIKeyEnv: "U0_EMBED_TEST_CREDENTIAL",
		ProviderModelID: "embedding-model", TimeoutMS: 500,
		SupportsEmbeddings: true,
	}, entity.InferenceProviderRequest{
		Messages: []entity.InferenceMessage{{Role: "user", Content: "النص"}},
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"embedding":[0.25,-0.5]}`, result.Output)
	assert.EqualValues(t, 8, result.InputTokens)
}

//nolint:gosec // Test-only environment-variable names contain no credential value.
func TestInferenceProviderRedactsSecretAndClassifiesHTTP(t *testing.T) {
	var providerErr *entity.InferenceProviderError

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "credential secret-value rejected", http.StatusTooManyRequests)
	}))
	defer server.Close()

	t.Setenv("U0_REDACTION_TEST_CREDENTIAL", "secret-value")
	_, err := NewInferenceProvider().Chat(t.Context(), entity.InferenceRoute{
		BaseURL: server.URL, APIKeyEnv: "U0_REDACTION_TEST_CREDENTIAL",
		ProviderModelID: "model", TimeoutMS: 500,
	}, entity.InferenceProviderRequest{})

	require.ErrorAs(t, err, &providerErr)
	assert.True(t, providerErr.Retryable)
	assert.Equal(t, http.StatusTooManyRequests, providerErr.StatusCode)
	assert.NotContains(t, err.Error(), "secret-value")
	assert.Equal(t, "inference provider HTTP status: 429", err.Error())
}

func TestDecimalProviderCostUsesExactConservativeNanoUSD(t *testing.T) {
	t.Parallel()

	nano, err := decimalUSDToNano("0.0000000011")
	require.NoError(t, err)
	assert.EqualValues(t, 2, nano)
	nano, err = decimalUSDToNano("12.345678901")
	require.NoError(t, err)
	assert.EqualValues(t, 12_345_678_901, nano)

	_, err = decimalUSDToNano("-1")
	require.Error(t, err)
}
