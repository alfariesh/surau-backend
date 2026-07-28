package entity

import (
	"errors"
	"time"
)

const (
	InferenceTaskClassRewrite = "rewrite"
	InferenceTaskClassRerank  = "rerank"
	InferenceTaskClassEmbed   = "embed"
	InferenceTaskClassAnswer  = "answer"
	InferenceTaskClassJudge   = "judge"

	InferenceUsageProvider  = "provider"
	InferenceUsageEstimated = "estimated"
	InferenceUsageCache     = "cache"

	InferenceCostProvider = "provider"
	InferenceCostRegistry = "registry"
	InferenceCostCache    = "cache"
)

var (
	ErrInferenceTaskNotFound     = errors.New("inference task not found")
	ErrInferenceRouteMissing     = errors.New("inference route is not configured")
	ErrInferenceProviderFailure  = errors.New("inference provider unavailable")
	ErrInferenceSchemaInvalid    = errors.New("inference response schema invalid")
	ErrInferenceSessionPinned    = errors.New("inference session provider failed after pin")
	ErrInferenceRegistryConflict = errors.New(
		"inference registry content conflicts with embedded manifest",
	)
)

// InferenceBudgetExceededError is safe to expose: it contains no prompt or
// provider secret, only when a caller may retry after the daily/monthly reset.
type InferenceBudgetExceededError struct {
	RetryAfter time.Duration
	ResetAt    time.Time
	Window     string
}

func (e *InferenceBudgetExceededError) Error() string {
	return "inference budget exceeded"
}

// InferenceMessage is the provider-neutral rendered prompt.
type InferenceMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// InferenceInvoke is the only caller-facing inference input. Variables are
// rendered through the immutable prompt version selected by the registry.
type InferenceInvoke struct {
	TaskKey       string         `json:"task_key" validate:"required"`
	Variables     map[string]any `json:"variables" validate:"required"`
	SessionID     string         `json:"session_id,omitempty"`
	CacheVary     map[string]any `json:"cache_vary,omitempty"`
	SourceVersion string         `json:"source_version,omitempty"`
	IndexVersion  string         `json:"index_version,omitempty"`
}

// InferenceUsage is both the API shape and the durable metering tuple.
type InferenceUsage struct {
	InputTokens       int64  `json:"input_tokens"`
	CachedInputTokens int64  `json:"cached_input_tokens"`
	OutputTokens      int64  `json:"output_tokens"`
	Source            string `json:"source"`
}

// InferenceCost stores exact integer nano-USD. USD is a display-only decimal.
type InferenceCost struct {
	NanoUSD      int64  `json:"nano_usd"`
	USD          string `json:"usd"`
	Source       string `json:"source"`
	PriceVersion string `json:"price_version,omitempty"`
}

// InferenceResult is returned by the shared layer and internal gateway.
type InferenceResult struct {
	CallID      string             `json:"call_id"`
	Output      string             `json:"output"`
	Generation  GenerationIdentity `json:"generation"`
	Provider    string             `json:"provider"`
	Model       string             `json:"model"`
	Prompt      string             `json:"prompt_version"`
	Schema      string             `json:"response_schema_version"`
	Usage       InferenceUsage     `json:"usage"`
	Cost        InferenceCost      `json:"cost"`
	CacheStatus string             `json:"cache_status"`
	Failover    bool               `json:"failover"`
}

// InferenceProviderRequest is private to the shared layer/provider adapters.
type InferenceProviderRequest struct {
	Model       string
	Messages    []InferenceMessage
	MaxTokens   int
	Temperature float64
	JSONOutput  bool
}

// InferenceProviderResponse is normalized across providers.
type InferenceProviderResponse struct {
	Output            string
	InputTokens       int64
	CachedInputTokens int64
	OutputTokens      int64
	CostNanoUSD       *int64
}

// InferenceProviderError preserves retry classification without response data.
type InferenceProviderError struct {
	StatusCode int
	Retryable  bool
	Cause      error
}

func (e *InferenceProviderError) Error() string {
	if e.Cause == nil {
		return "inference provider error"
	}

	return e.Cause.Error()
}

func (e *InferenceProviderError) Unwrap() error {
	return e.Cause
}

// InferenceTask is one frozen task key and cache policy.
type InferenceTask struct {
	TaskKey              string `json:"task_key"`
	TaskClass            string `json:"task_class"`
	OutputKind           string `json:"output_kind"`
	CacheTTLSeconds      int    `json:"cache_ttl_seconds"`
	PersistentEnrichment bool   `json:"persistent_enrichment"`
	Enabled              bool   `json:"enabled"`
}

// InferencePromptManifest is the binary-owned immutable registry material.
type InferencePromptManifest struct {
	TaskKey               string             `json:"task_key"`
	TaskClass             string             `json:"task_class"`
	OutputKind            string             `json:"output_kind"`
	CacheTTLSeconds       int                `json:"cache_ttl_seconds"`
	PersistentEnrichment  bool               `json:"persistent_enrichment"`
	PromptVersion         string             `json:"prompt_version"`
	PolicySHA256          string             `json:"policy_sha256,omitempty"`
	Messages              []InferenceMessage `json:"messages"`
	ResponseSchemaVersion string             `json:"response_schema_version"`
	ResponseSchema        RawJSON            `json:"response_schema"`
}

// InferenceRoute is a fully resolved provider attempt. API keys never appear.
type InferenceRoute struct {
	TaskKey               string  `json:"task_key"`
	TaskClass             string  `json:"task_class"`
	OutputKind            string  `json:"output_kind"`
	CacheTTLSeconds       int     `json:"cache_ttl_seconds"`
	PersistentEnrichment  bool    `json:"persistent_enrichment"`
	Priority              int     `json:"priority"`
	ProviderKey           string  `json:"provider"`
	BaseURL               string  `json:"-"`
	APIKeyEnv             string  `json:"-"`
	ModelKey              string  `json:"model_key"`
	ProviderModelID       string  `json:"model"`
	PriceVersion          string  `json:"price_version"`
	InputNanoPerMillion   int64   `json:"input_nano_usd_per_million"`
	CachedNanoPerMillion  int64   `json:"cached_input_nano_usd_per_million"`
	OutputNanoPerMillion  int64   `json:"output_nano_usd_per_million"`
	SupportsJSON          bool    `json:"supports_json"`
	SupportsEmbeddings    bool    `json:"supports_embeddings"`
	TimeoutMS             int     `json:"timeout_ms"`
	MaxOutputTokens       int     `json:"max_output_tokens"`
	PromptVersion         string  `json:"prompt_version"`
	PromptSHA256          string  `json:"prompt_sha256"`
	PolicySHA256          string  `json:"policy_sha256,omitempty"`
	MessagesTemplate      RawJSON `json:"-"`
	ResponseSchemaVersion string  `json:"response_schema_version"`
	ResponseSchemaSHA256  string  `json:"response_schema_sha256"`
	ResponseSchema        RawJSON `json:"-"`
	Temperature           float64 `json:"temperature"`
}

// InferenceCall is the durable logical request.
type InferenceCall struct {
	ID           string
	TaskKey      string
	TaskClass    string
	SessionID    *string
	RequestID    *string
	TraceID      *string
	CacheKey     *string
	CacheStatus  string
	Status       string
	GenerationID *string
	Usage        InferenceUsage
	CostNanoUSD  int64
	CostSource   string
	ErrorCode    *string
}

// InferenceAttempt is one actual provider request and B-6 descriptor.
type InferenceAttempt struct {
	ID          string
	CallID      string
	AttemptNo   int
	Generation  GenerationRun
	Route       InferenceRoute
	Status      string
	Usage       InferenceUsage
	CostNanoUSD int64
	CostSource  string
	Failover    bool
	ErrorCode   *string
}

// InferenceCacheEntry contains encrypted content and original attribution.
type InferenceCacheEntry struct {
	CacheKey      string
	TaskKey       string
	GenerationRun GenerationRun
	Ciphertext    string
	ExpiresAt     time.Time
}

// InferenceBudgetStatus is safe for operator/admin display.
type InferenceBudgetStatus struct {
	Revision            int64      `json:"revision"`
	Mode                string     `json:"mode"`
	Timezone            string     `json:"timezone"`
	BaselineStartedAt   time.Time  `json:"baseline_started_at"`
	BaselineDays        int        `json:"baseline_days"`
	BaselineEndsAt      time.Time  `json:"baseline_ends_at"`
	BaselineCostNanoUSD int64      `json:"baseline_cost_nano_usd"`
	DailyCapNanoUSD     *int64     `json:"daily_cap_nano_usd,omitempty"`
	MonthlyCapNanoUSD   *int64     `json:"monthly_cap_nano_usd,omitempty"`
	DailyUsedNanoUSD    int64      `json:"daily_used_nano_usd"`
	MonthlyUsedNanoUSD  int64      `json:"monthly_used_nano_usd"`
	ReservedNanoUSD     int64      `json:"reserved_nano_usd"`
	AlertPercent        float64    `json:"alert_percent"`
	NextResetAt         *time.Time `json:"next_reset_at,omitempty"`
	DailyResetAt        *time.Time `json:"daily_reset_at,omitempty"`
	MonthlyResetAt      *time.Time `json:"monthly_reset_at,omitempty"`
	ConfigAlert         string     `json:"config_alert,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// InferenceBudgetPatch creates an append-only policy revision.
type InferenceBudgetPatch struct {
	Mode              string `json:"mode" validate:"required,oneof=baseline enforce disabled"`
	DailyCapNanoUSD   *int64 `json:"daily_cap_nano_usd,omitempty"`
	MonthlyCapNanoUSD *int64 `json:"monthly_cap_nano_usd,omitempty"`
	Reason            string `json:"reason" validate:"required,min=3,max=500"`
}

// InferenceUsageRow is a bounded grouping for the admin daily ledger.
type InferenceUsageRow struct {
	Day          string `json:"day"`
	TaskKey      string `json:"task_key,omitempty"`
	TaskClass    string `json:"task_class,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	CachedTokens int64  `json:"cached_input_tokens"`
	CostNanoUSD  int64  `json:"cost_nano_usd"`
	Calls        int64  `json:"calls"`
}

type InferenceUsageList struct {
	Items []InferenceUsageRow `json:"items"`
	Total int                 `json:"total"`
}

// InferenceSession pins batch enrichment to one provider/model after success.
type InferenceSession struct {
	ID                string    `json:"id"`
	TaskKey           string    `json:"task_key"`
	Status            string    `json:"status"`
	PinnedProviderKey *string   `json:"pinned_provider,omitempty"`
	PinnedModelKey    *string   `json:"pinned_model,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}
