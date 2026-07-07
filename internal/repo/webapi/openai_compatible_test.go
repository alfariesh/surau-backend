package webapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAICompatibleClientComplete(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"jawaban"}}]}`)
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(OpenAICompatibleOptions{
		BaseURL:     server.URL,
		APIKey:      "test-key",
		Model:       "glm-5.1",
		Timeout:     time.Second,
		MaxTokens:   100,
		Temperature: 0.1,
	})

	got, err := client.Complete(context.Background(), []entity.RAGChatMessage{{Role: "user", Content: "Q"}})

	require.NoError(t, err)
	assert.Equal(t, "jawaban", got)
}

func TestOpenAICompatibleClientCompleteFallsBackToReasoningContent(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"content":"","reasoning_content":"fallback"}}]}`)
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(OpenAICompatibleOptions{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "glm-5.1",
		Timeout: time.Second,
	})

	got, err := client.Complete(context.Background(), []entity.RAGChatMessage{{Role: "user", Content: "Q"}})

	require.NoError(t, err)
	assert.Equal(t, "fallback", got)
}

func TestOpenAICompatibleClientStream(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ja\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"waban\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(OpenAICompatibleOptions{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "glm-5.1",
		Timeout: time.Second,
	})

	var builder strings.Builder
	err := client.Stream(context.Background(), []entity.RAGChatMessage{{Role: "user", Content: "Q"}}, func(delta string) error {
		builder.WriteString(delta)

		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, "jawaban", builder.String())
}

func TestOpenAICompatibleClientStatusErrorRedactsAPIKey(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad key test-key", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewOpenAICompatibleClient(OpenAICompatibleOptions{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "glm-5.1",
		Timeout: time.Second,
	})

	_, err := client.Complete(context.Background(), []entity.RAGChatMessage{{Role: "user", Content: "Q"}})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "test-key")
	assert.Contains(t, err.Error(), "[redacted]")
}

func TestOpenAICompatibleClientMissingConfig(t *testing.T) {
	t.Parallel()

	client := NewOpenAICompatibleClient(OpenAICompatibleOptions{BaseURL: "http://example.test", Model: "glm-5.1"})

	_, err := client.Complete(context.Background(), []entity.RAGChatMessage{{Role: "user", Content: "Q"}})

	require.ErrorIs(t, err, entity.ErrRAGNotConfigured)
}
