package webapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// OpenAICompatibleOptions configures a chat-completions-compatible LLM.
type OpenAICompatibleOptions struct {
	BaseURL     string
	APIKey      string
	Model       string
	Timeout     time.Duration
	MaxTokens   int
	Temperature float64
}

// OpenAICompatibleClient calls an OpenAI-compatible /chat/completions API.
type OpenAICompatibleClient struct {
	baseURL     string
	apiKey      string
	model       string
	maxTokens   int
	temperature float64
	httpClient  *http.Client
}

// NewOpenAICompatibleClient creates an OpenAI-compatible LLM client.
func NewOpenAICompatibleClient(opts OpenAICompatibleOptions) *OpenAICompatibleClient {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	return &OpenAICompatibleClient{
		baseURL:     strings.TrimRight(opts.BaseURL, "/"),
		apiKey:      opts.APIKey,
		model:       opts.Model,
		maxTokens:   opts.MaxTokens,
		temperature: opts.Temperature,
		httpClient:  &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
}

// Complete returns a full chat completion response.
func (c *OpenAICompatibleClient) Complete(ctx context.Context, messages []entity.RAGChatMessage) (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}

	body, err := c.newRequestBody(messages, false)
	if err != nil {
		return "", err
	}

	req, err := c.newHTTPRequest(ctx, body)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("OpenAICompatibleClient - Complete - Do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", c.statusError(resp)
	}

	var parsed chatCompletionResponse
	if err = json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", fmt.Errorf("OpenAICompatibleClient - Complete - Decode: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("OpenAICompatibleClient - Complete: empty choices")
	}
	content := parsed.Choices[0].Message.Content
	if content == "" {
		content = parsed.Choices[0].Message.ReasoningContent
	}

	return content, nil
}

// Stream emits text deltas from a chat completion stream.
func (c *OpenAICompatibleClient) Stream(
	ctx context.Context,
	messages []entity.RAGChatMessage,
	emit func(delta string) error,
) error {
	if err := c.validate(); err != nil {
		return err
	}

	body, err := c.newRequestBody(messages, true)
	if err != nil {
		return err
	}

	req, err := c.newHTTPRequest(ctx, body)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("OpenAICompatibleClient - Stream - Do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return c.statusError(resp)
	}

	return parseOpenAIStream(resp.Body, emit)
}

func (c *OpenAICompatibleClient) validate() error {
	if c.baseURL == "" || c.model == "" || c.apiKey == "" {
		return entity.ErrRAGNotConfigured
	}

	return nil
}

func (c *OpenAICompatibleClient) newRequestBody(messages []entity.RAGChatMessage, stream bool) ([]byte, error) {
	payload := chatCompletionRequest{
		Model:       c.model,
		Messages:    messages,
		MaxTokens:   c.maxTokens,
		Temperature: c.temperature,
		Stream:      stream,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("OpenAICompatibleClient - newRequestBody - Marshal: %w", err)
	}

	return body, nil
}

func (c *OpenAICompatibleClient) newHTTPRequest(ctx context.Context, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("OpenAICompatibleClient - newHTTPRequest: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return req, nil
}

func (c *OpenAICompatibleClient) statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(body))
	if c.apiKey != "" {
		message = strings.ReplaceAll(message, c.apiKey, "[redacted]")
	}
	if message == "" {
		message = resp.Status
	}

	return fmt.Errorf("OpenAICompatibleClient: status %d: %s", resp.StatusCode, message)
}

func parseOpenAIStream(body io.Reader, emit func(delta string) error) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			return nil
		}

		var chunk chatCompletionStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("OpenAICompatibleClient - parseOpenAIStream - Unmarshal: %w", err)
		}

		for _, choice := range chunk.Choices {
			if choice.Delta.Content == "" {
				continue
			}
			if err := emit(choice.Delta.Content); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("OpenAICompatibleClient - parseOpenAIStream - Scan: %w", err)
	}

	return nil
}

type chatCompletionRequest struct {
	Model       string                  `json:"model"`
	Messages    []entity.RAGChatMessage `json:"messages"`
	MaxTokens   int                     `json:"max_tokens,omitempty"`
	Temperature float64                 `json:"temperature"`
	Stream      bool                    `json:"stream,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
}

type chatCompletionStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}
