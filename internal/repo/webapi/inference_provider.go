package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	nanoUSDPerUSD            = 1_000_000_000
	maxDiscardedResponseBody = 4096
)

var (
	errInferenceProviderHTTPStatus = errors.New("inference provider HTTP status")
	errInferenceProviderEmpty      = errors.New("inference provider returned empty output")
	errInferenceEmbeddingInput     = errors.New("inference embedding input is empty")
	errInferenceEmbeddingEmpty     = errors.New("inference provider returned no embeddings")
	errProviderCostInvalid         = errors.New("invalid provider USD cost")
	errProviderCostOverflow        = errors.New("provider USD cost overflows nano-USD")
)

var _ repo.InferenceProvider = (*InferenceProvider)(nil)

// InferenceProvider is the only production adapter allowed to reach an LLM
// provider. Provider URL/model/key selection comes exclusively from U-0.
type InferenceProvider struct {
	transport http.RoundTripper
	rollout   *DeterministicRolloutLLMClient
}

func NewInferenceProvider() *InferenceProvider {
	return &InferenceProvider{
		transport: otelhttp.NewTransport(http.DefaultTransport),
		rollout:   NewDeterministicRolloutLLMClient(),
	}
}

//nolint:gocognit,gocritic,gocyclo,cyclop,funlen // Atomic provider parsing and retry classification.
func (p *InferenceProvider) Chat(
	ctx context.Context,
	route entity.InferenceRoute,
	request entity.InferenceProviderRequest,
) (entity.InferenceProviderResponse, error) {
	if route.ProviderKey == "deterministic-rollout" {
		messages := make([]entity.RAGChatMessage, len(request.Messages))
		for i := range request.Messages {
			messages[i] = entity.RAGChatMessage{
				Role: request.Messages[i].Role, Content: request.Messages[i].Content,
			}
		}

		output, err := p.rollout.Complete(ctx, messages)

		return entity.InferenceProviderResponse{Output: output}, err
	}

	apiKey := strings.TrimSpace(os.Getenv(route.APIKeyEnv))
	if apiKey == "" || strings.TrimSpace(route.BaseURL) == "" {
		return entity.InferenceProviderResponse{}, entity.ErrRAGNotConfigured
	}

	payload := inferenceChatRequest{
		Model:       route.ProviderModelID,
		Messages:    request.Messages,
		MaxTokens:   request.MaxTokens,
		Temperature: request.Temperature,
	}
	if request.JSONOutput {
		payload.ResponseFormat = &inferenceResponseFormat{Type: "json_object"}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return entity.InferenceProviderResponse{}, fmt.Errorf("InferenceProvider.Chat marshal: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost, strings.TrimRight(route.BaseURL, "/")+"/chat/completions",
		bytes.NewReader(body),
	)
	if err != nil {
		return entity.InferenceProviderResponse{}, fmt.Errorf("InferenceProvider.Chat request: %w", err)
	}

	httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	timeout := time.Duration(route.TimeoutMS) * time.Millisecond
	client := &http.Client{Timeout: timeout, Transport: p.transport}

	response, err := client.Do(httpRequest)
	if err != nil {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: true,
			Cause:     fmt.Errorf("InferenceProvider.Chat do: %w", err),
		}
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		discardProviderBody(response.Body)

		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			StatusCode: response.StatusCode,
			Retryable: response.StatusCode == http.StatusRequestTimeout ||
				response.StatusCode == http.StatusTooManyRequests ||
				response.StatusCode >= http.StatusInternalServerError,
			Cause: fmt.Errorf(
				"%w: %d", errInferenceProviderHTTPStatus, response.StatusCode,
			),
		}
	}

	var parsed inferenceChatResponse
	if err = json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: true,
			Cause:     fmt.Errorf("InferenceProvider.Chat decode: %w", err),
		}
	}

	if len(parsed.Choices) == 0 {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: true,
			Cause:     errInferenceProviderEmpty,
		}
	}

	output := parsed.Choices[0].Message.Content
	if strings.TrimSpace(output) == "" {
		output = parsed.Choices[0].Message.ReasoningContent
	}

	if strings.TrimSpace(output) == "" {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: true,
			Cause:     errInferenceProviderEmpty,
		}
	}

	result := entity.InferenceProviderResponse{
		Output:            output,
		InputTokens:       parsed.Usage.PromptTokens,
		CachedInputTokens: parsed.Usage.PromptTokensDetails.CachedTokens,
		OutputTokens:      parsed.Usage.CompletionTokens,
	}
	if parsed.Usage.CostNanoUSD != nil {
		result.CostNanoUSD = parsed.Usage.CostNanoUSD
	} else if parsed.Usage.CostUSD != nil {
		if nano, parseErr := decimalUSDToNano(parsed.Usage.CostUSD.String()); parseErr == nil {
			result.CostNanoUSD = &nano
		}
	}

	return result, nil
}

// Embed is present for the frozen U-0 task class, but no embedding route is
// enabled until U-1 selects an Arabic↔Indonesian-safe model.
//
//nolint:gocritic,gocyclo,cyclop,funlen // Protocol parsing preserves retry classification.
func (p *InferenceProvider) Embed(
	ctx context.Context,
	route entity.InferenceRoute,
	request entity.InferenceProviderRequest,
) (entity.InferenceProviderResponse, error) {
	if !route.SupportsEmbeddings {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: false,
			Cause:     entity.ErrInferenceRouteMissing,
		}
	}

	apiKey := strings.TrimSpace(os.Getenv(route.APIKeyEnv))
	if apiKey == "" || strings.TrimSpace(route.BaseURL) == "" {
		return entity.InferenceProviderResponse{}, entity.ErrRAGNotConfigured
	}

	inputs := make([]string, 0, len(request.Messages))
	for i := range request.Messages {
		if request.Messages[i].Role == "user" {
			inputs = append(inputs, request.Messages[i].Content)
		}
	}

	if len(inputs) == 0 {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: false,
			Cause:     errInferenceEmbeddingInput,
		}
	}

	body, err := json.Marshal(inferenceEmbeddingRequest{
		Model: route.ProviderModelID,
		Input: inputs,
	})
	if err != nil {
		return entity.InferenceProviderResponse{}, fmt.Errorf("InferenceProvider.Embed marshal: %w", err)
	}

	httpRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPost, strings.TrimRight(route.BaseURL, "/")+"/embeddings",
		bytes.NewReader(body),
	)
	if err != nil {
		return entity.InferenceProviderResponse{}, fmt.Errorf("InferenceProvider.Embed request: %w", err)
	}

	httpRequest.Header.Set("Authorization", "Bearer "+apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := (&http.Client{
		Timeout:   time.Duration(route.TimeoutMS) * time.Millisecond,
		Transport: p.transport,
	}).Do(httpRequest)
	if err != nil {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: true,
			Cause:     fmt.Errorf("InferenceProvider.Embed do: %w", err),
		}
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return entity.InferenceProviderResponse{}, inferenceHTTPError(response)
	}

	var parsed inferenceEmbeddingResponse

	if err = json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: true,
			Cause:     fmt.Errorf("InferenceProvider.Embed decode: %w", err),
		}
	}

	if len(parsed.Data) == 0 {
		return entity.InferenceProviderResponse{}, &entity.InferenceProviderError{
			Retryable: true,
			Cause:     errInferenceEmbeddingEmpty,
		}
	}

	output, err := json.Marshal(map[string]any{"embedding": parsed.Data[0].Embedding})
	if err != nil {
		return entity.InferenceProviderResponse{}, fmt.Errorf("InferenceProvider.Embed output: %w", err)
	}

	return entity.InferenceProviderResponse{
		Output:      string(output),
		InputTokens: parsed.Usage.PromptTokens,
	}, nil
}

func inferenceHTTPError(response *http.Response) error {
	discardProviderBody(response.Body)

	return &entity.InferenceProviderError{
		StatusCode: response.StatusCode,
		Retryable: response.StatusCode == http.StatusRequestTimeout ||
			response.StatusCode == http.StatusTooManyRequests ||
			response.StatusCode >= http.StatusInternalServerError,
		Cause: fmt.Errorf("%w: %d", errInferenceProviderHTTPStatus, response.StatusCode),
	}
}

func discardProviderBody(body io.Reader) {
	if _, err := io.Copy(
		io.Discard,
		io.LimitReader(body, maxDiscardedResponseBody),
	); err != nil {
		return
	}
}

type inferenceChatRequest struct {
	Model          string                    `json:"model"`
	Messages       []entity.InferenceMessage `json:"messages"`
	MaxTokens      int                       `json:"max_tokens,omitempty"`
	Temperature    float64                   `json:"temperature"`
	ResponseFormat *inferenceResponseFormat  `json:"response_format,omitempty"`
}

type inferenceResponseFormat struct {
	Type string `json:"type"`
}

type inferenceChatResponse struct {
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		CompletionTokens    int64 `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CostNanoUSD *int64       `json:"cost_nano_usd"`
		CostUSD     *json.Number `json:"cost_usd"`
	} `json:"usage"`
}

type inferenceEmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type inferenceEmbeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int64 `json:"prompt_tokens"`
	} `json:"usage"`
}

func decimalUSDToNano(value string) (int64, error) {
	ratio, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok || ratio.Sign() < 0 {
		return 0, errProviderCostInvalid
	}

	ratio.Mul(ratio, big.NewRat(nanoUSDPerUSD, 1))

	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(ratio.Num(), ratio.Denom(), remainder)

	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}

	if !quotient.IsInt64() {
		return 0, errProviderCostOverflow
	}

	return quotient.Int64(), nil
}
