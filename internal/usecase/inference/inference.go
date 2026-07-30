package inference

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/requestmeta"
	"github.com/alfariesh/surau-backend/pkg/cryptobox"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	maxProviderAttempts            = 2
	nanoPerUSD                     = int64(1_000_000_000)
	priceTokenUnit                 = int64(1_000_000)
	nanoPerUSDPerMillionTokens     = int64(1_000_000_000_000_000)
	inferenceDriverDeterministic   = "deterministic-rollout"
	inferenceProviderLabel         = "provider"
	inferenceTaskLabel             = "task"
	inferenceCatalogTimeout        = 15 * time.Second
	inferenceCatalogMaxBytes       = 4 << 20
	inferenceCatalogErrorBodyBytes = 4096
	deterministicProviderTimeoutMS = 1000
	operatorPriceTupleFields       = 4
	deepSeekPriority               = 2
	deepSeekInputNanoPerMillion    = 140_000_000
	deepSeekCachedNanoPerMillion   = 2_800_000
	deepSeekOutputNanoPerMillion   = 280_000_000
	fallbackTraceIDHexLength       = 32
)

var (
	errInvalidPriceDecimal   = errors.New("invalid nonnegative price decimal")
	errPriceDecimalOverflow  = errors.New("price decimal exceeds nano-USD range")
	errInferenceAttemptTrace = errors.New("inference provider attempt failed")
)

//nolint:gochecknoglobals // bounded task/provider/outcome labels only
var (
	inferenceCalls = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_inference_calls_total",
		Help: "Shared inference logical calls by task, provider, outcome and cache status.",
	}, []string{inferenceTaskLabel, inferenceProviderLabel, "outcome", "cache"})
	inferenceTokens = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_inference_tokens_total",
		Help: "Shared inference tokens by task, provider, direction and source.",
	}, []string{inferenceTaskLabel, inferenceProviderLabel, "direction", "source"})
	inferenceCost = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_inference_cost_nano_usd_total",
		Help: "Shared inference cost in integer nano-USD by task, provider and source.",
	}, []string{inferenceTaskLabel, inferenceProviderLabel, "source"})
	inferenceCostByUsageSource = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_inference_cost_by_usage_source_nano_usd_total",
		Help: "Inference cost split by provider-reported versus estimated token usage.",
	}, []string{inferenceTaskLabel, inferenceProviderLabel, "usage_source"})
	inferenceBudgetRatio = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "surau_inference_budget_usage_ratio",
		Help: "Current inference budget usage ratio; alert at 0.8 in enforce mode.",
	}, []string{"window", "mode"})
	inferenceBudgetRejections = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_inference_budget_rejections_total",
		Help: "Inference calls rejected before provider access by budget window.",
	}, []string{"window"})
	inferenceBaselineProgress = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "surau_inference_budget_baseline_progress_ratio",
		Help: "Elapsed fraction of the 30-day baseline window, capped at one.",
	})
	inferenceBudgetConfigAlert = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "surau_inference_budget_config_alert",
		Help: "Budget configuration alert, including a zero-cost expired baseline.",
	}, []string{"kind"})
	inferenceCacheHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_inference_cache_total",
		Help: "Inference safe-cache lookups by task and result.",
	}, []string{inferenceTaskLabel, "result"})
	inferenceFailovers = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_inference_failovers_total",
		Help: "Inference provider failovers by task, from and to provider.",
	}, []string{inferenceTaskLabel, "from", "to"})
)

type Options struct {
	Driver                      string
	PrimaryBaseURL              string
	PrimaryModel                string
	PrimaryAPIKeyEnv            string
	PrimaryCatalogURL           string
	PrimaryPriceVersion         string
	PrimaryInputUSDPerMillion   string
	PrimaryCachedUSDPerMillion  string
	PrimaryOutputUSDPerMillion  string
	PrimaryInputNanoPerMillion  int64
	PrimaryCachedNanoPerMillion int64
	PrimaryOutputNanoPerMillion int64
	DisableSecondary            bool
	SecondaryBaseURL            string
	SecondaryModel              string
	SecondaryAPIKeyEnv          string
	CacheSeed                   string
	Timeout                     time.Duration
	MaxOutputTokens             int
	Temperature                 float64
	CatalogHTTPClient           *http.Client
}

// UseCase is the sole application path to external model providers.
type UseCase struct {
	repo            repo.InferenceRepo
	provider        repo.InferenceProvider
	cacheBox        *cryptobox.Box
	cacheHMAC       []byte
	options         Options
	ephemeralRoutes []entity.InferenceRoute
	now             func() time.Time
}

//nolint:gocritic // Options is an immutable constructor snapshot retained by the usecase.
func New(
	registry repo.InferenceRepo,
	provider repo.InferenceProvider,
	options Options,
) (*UseCase, error) {
	if registry == nil || provider == nil {
		return nil, entity.ErrInferenceRouteMissing
	}

	cacheBox, err := cryptobox.New(options.CacheSeed, "surau-inference-cache-v1")
	if err != nil {
		return nil, fmt.Errorf("inference cache key: %w", err)
	}

	cacheKey := sha256.Sum256([]byte("surau-inference-cache-hmac-v1\x00" + options.CacheSeed))

	return &UseCase{
		repo: registry, provider: provider, cacheBox: cacheBox,
		cacheHMAC: cacheKey[:], options: options, now: time.Now,
	}, nil
}

// Initialize verifies the binary-owned prompt/schema content and reconciles
// only non-secret provider routing. A content conflict fails application boot.
func (uc *UseCase) Initialize(ctx context.Context) error {
	if err := uc.syncPrimaryPrice(ctx); err != nil {
		return err
	}

	manifests, err := promptManifests()
	if err != nil {
		return err
	}

	if syncErr := uc.repo.SyncManifest(ctx, manifests); syncErr != nil {
		return syncErr
	}

	routes, err := uc.routes(manifests)
	if err != nil {
		return err
	}

	if uc.options.Driver == inferenceDriverDeterministic {
		// The rollout evaluator shares the DEV database with the serving API.
		// Keep its route process-local so it cannot disable or replace the
		// serving SumoPod routes. Provider/model/price rows remain durable
		// because attempts and generation runs must still be attributable.
		uc.ephemeralRoutes = routes

		return uc.repo.SyncEphemeralModels(ctx, routes)
	}

	return uc.repo.SyncRoutes(ctx, routes)
}

func (uc *UseCase) syncPrimaryPrice(ctx context.Context) error {
	if uc.options.Driver == inferenceDriverDeterministic {
		return nil
	}

	if configured, err := uc.syncPrimaryOperatorPrice(); configured || err != nil {
		return err
	}

	return uc.syncPrimaryCatalogPrice(ctx)
}

// syncPrimaryCatalogPrice imports the effective, discount-adjusted price from
// the same catalog consumed by the SumoPod account dashboard. The provider
// secret is deliberately not sent: this catalog is public and contains no
// account credential.
func (uc *UseCase) syncPrimaryCatalogPrice(ctx context.Context) error {
	catalogURL := strings.TrimSpace(uc.options.PrimaryCatalogURL)
	if catalogURL == "" {
		return fmt.Errorf("%w: SumoPod price catalog URL is empty", entity.ErrInferenceRouteMissing)
	}

	client := uc.options.CatalogHTTPClient
	if client == nil {
		client = &http.Client{Timeout: inferenceCatalogTimeout}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("inference SumoPod catalog request: %w", err)
	}

	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("inference SumoPod catalog: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		discardCatalogBody(response.Body)

		return fmt.Errorf(
			"%w: SumoPod price catalog returned HTTP %d",
			entity.ErrInferenceRouteMissing,
			response.StatusCode,
		)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, inferenceCatalogMaxBytes))
	if err != nil {
		return fmt.Errorf("inference SumoPod catalog read: %w", err)
	}

	price, err := parseCatalogPrice(body, uc.options.PrimaryModel)
	if err != nil {
		return err
	}

	uc.options.PrimaryPriceVersion = price.Version
	uc.options.PrimaryInputNanoPerMillion = price.InputNanoPerMillion
	// The catalog does not advertise a cached-input discount. Charging cached
	// input at the full input rate is the conservative safe default.
	uc.options.PrimaryCachedNanoPerMillion = price.InputNanoPerMillion
	uc.options.PrimaryOutputNanoPerMillion = price.OutputNanoPerMillion

	return nil
}

// syncPrimaryOperatorPrice accepts a complete, append-only account price
// snapshot when a provider no longer exposes a machine-readable price
// catalog. Partial tuples fail closed so a paid route can never silently use
// zero or stale prices.
func (uc *UseCase) syncPrimaryOperatorPrice() (bool, error) {
	version := strings.TrimSpace(uc.options.PrimaryPriceVersion)
	input := strings.TrimSpace(uc.options.PrimaryInputUSDPerMillion)
	cached := strings.TrimSpace(uc.options.PrimaryCachedUSDPerMillion)
	output := strings.TrimSpace(uc.options.PrimaryOutputUSDPerMillion)
	configured := 0

	for _, value := range []string{version, input, cached, output} {
		if value != "" {
			configured++
		}
	}

	if configured == 0 {
		return false, nil
	}

	if configured != operatorPriceTupleFields {
		return true, fmt.Errorf(
			"%w: SumoPod operator price override must include version, input, cached input and output",
			entity.ErrInferenceRouteMissing,
		)
	}

	inputNano, err := pricePerMillionToNano(input)
	if err != nil {
		return true, fmt.Errorf("SumoPod operator input price: %w", err)
	}

	cachedNano, err := pricePerMillionToNano(cached)
	if err != nil {
		return true, fmt.Errorf("SumoPod operator cached-input price: %w", err)
	}

	outputNano, err := pricePerMillionToNano(output)
	if err != nil {
		return true, fmt.Errorf("SumoPod operator output price: %w", err)
	}

	uc.options.PrimaryPriceVersion = version
	uc.options.PrimaryInputNanoPerMillion = inputNano
	uc.options.PrimaryCachedNanoPerMillion = cachedNano
	uc.options.PrimaryOutputNanoPerMillion = outputNano

	return true, nil
}

func discardCatalogBody(body io.Reader) {
	if _, err := io.Copy(
		io.Discard,
		io.LimitReader(body, inferenceCatalogErrorBodyBytes),
	); err != nil {
		return
	}
}

type catalogPrice struct {
	Version              string
	InputNanoPerMillion  int64
	OutputNanoPerMillion int64
}

func parseCatalogPrice(payload []byte, modelID string) (catalogPrice, error) {
	var rows []json.RawMessage

	if err := json.Unmarshal(payload, &rows); err != nil || len(rows) == 0 {
		return catalogPrice{}, fmt.Errorf(
			"%w: SumoPod price catalog is empty or invalid",
			entity.ErrInferenceRouteMissing,
		)
	}

	for i := range rows {
		var row struct {
			ModelName          string          `json:"model_name"`
			InputCostPerToken  json.RawMessage `json:"input_cost_per_token"`
			OutputCostPerToken json.RawMessage `json:"output_cost_per_token"`
		}
		if err := json.Unmarshal(rows[i], &row); err != nil || row.ModelName != modelID {
			continue
		}

		input, err := pricePerTokenToNanoPerMillion(row.InputCostPerToken)
		if err != nil {
			return catalogPrice{}, fmt.Errorf("SumoPod %s input price: %w", modelID, err)
		}

		output, err := pricePerTokenToNanoPerMillion(row.OutputCostPerToken)
		if err != nil {
			return catalogPrice{}, fmt.Errorf("SumoPod %s output price: %w", modelID, err)
		}

		hash := sha256.Sum256(rows[i])

		return catalogPrice{
			Version:              "sumopod-catalog-" + hex.EncodeToString(hash[:8]),
			InputNanoPerMillion:  input,
			OutputNanoPerMillion: output,
		}, nil
	}

	return catalogPrice{}, fmt.Errorf(
		"%w: model %q is absent from SumoPod price catalog",
		entity.ErrInferenceRouteMissing,
		modelID,
	)
}

func pricePerTokenToNanoPerMillion(raw json.RawMessage) (int64, error) {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)

	return roundedDecimal(value, nanoPerUSDPerMillionTokens)
}

func pricePerMillionToNano(value string) (int64, error) {
	return roundedDecimal(strings.TrimSpace(value), nanoPerUSD)
}

func roundedDecimal(value string, multiplier int64) (int64, error) {
	rational, ok := new(big.Rat).SetString(value)
	if !ok || rational.Sign() < 0 {
		return 0, fmt.Errorf("%w: %q", errInvalidPriceDecimal, value)
	}

	rational.Mul(rational, big.NewRat(multiplier, 1))

	quotient, remainder := new(big.Int).QuoRem(
		rational.Num(),
		rational.Denom(),
		new(big.Int),
	)
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}

	if !quotient.IsInt64() {
		return 0, fmt.Errorf("%w: %q", errPriceDecimalOverflow, value)
	}

	return quotient.Int64(), nil
}

func (uc *UseCase) routes(
	manifests []entity.InferencePromptManifest,
) ([]entity.InferenceRoute, error) {
	routes := make([]entity.InferenceRoute, 0, len(manifests)*maxProviderAttempts)
	for i := range manifests {
		manifest := manifests[i]
		if manifest.OutputKind == "embedding" {
			continue
		}

		if uc.options.Driver == inferenceDriverDeterministic {
			route, err := routeFromManifest(manifest, entity.InferenceRoute{
				Priority: 1, ProviderKey: "deterministic-rollout",
				BaseURL:         "http://deterministic-rollout.invalid",
				APIKeyEnv:       "INFERENCE_DETERMINISTIC_NO_SECRET",
				ModelKey:        "deterministic-rollout-v1",
				ProviderModelID: "deterministic-rollout-v1",
				PriceVersion:    "dev-zero-v1", SupportsJSON: true,
				TimeoutMS:       deterministicProviderTimeoutMS,
				MaxOutputTokens: uc.options.MaxOutputTokens,
				Temperature:     0,
			})
			if err != nil {
				return nil, err
			}

			routes = append(routes, route)

			continue
		}

		if uc.primaryPriced() {
			route, err := routeFromManifest(manifest, entity.InferenceRoute{
				Priority: 1, ProviderKey: "sumopod", BaseURL: uc.options.PrimaryBaseURL,
				APIKeyEnv: uc.options.PrimaryAPIKeyEnv, ModelKey: uc.options.PrimaryModel,
				ProviderModelID:      uc.options.PrimaryModel,
				PriceVersion:         uc.options.PrimaryPriceVersion,
				InputNanoPerMillion:  uc.options.PrimaryInputNanoPerMillion,
				CachedNanoPerMillion: uc.options.PrimaryCachedNanoPerMillion,
				OutputNanoPerMillion: uc.options.PrimaryOutputNanoPerMillion,
				SupportsJSON:         true, TimeoutMS: int(uc.options.Timeout.Milliseconds()),
				MaxOutputTokens: uc.options.MaxOutputTokens,
				Temperature:     uc.options.Temperature,
			})
			if err != nil {
				return nil, err
			}

			routes = append(routes, route)
		}

		if !uc.options.DisableSecondary {
			route, err := routeFromManifest(manifest, entity.InferenceRoute{
				Priority: deepSeekPriority, ProviderKey: "deepseek",
				BaseURL:   uc.options.SecondaryBaseURL,
				APIKeyEnv: uc.options.SecondaryAPIKeyEnv, ModelKey: uc.options.SecondaryModel,
				ProviderModelID:      uc.options.SecondaryModel,
				PriceVersion:         "deepseek-official-2026-07-28",
				InputNanoPerMillion:  deepSeekInputNanoPerMillion,
				CachedNanoPerMillion: deepSeekCachedNanoPerMillion,
				OutputNanoPerMillion: deepSeekOutputNanoPerMillion,
				SupportsJSON:         true, TimeoutMS: int(uc.options.Timeout.Milliseconds()),
				MaxOutputTokens: uc.options.MaxOutputTokens,
				Temperature:     uc.options.Temperature,
			})
			if err != nil {
				return nil, err
			}

			routes = append(routes, route)
		}
	}

	return routes, nil
}

//nolint:gocritic // Registry values are immutable snapshots assembled at boot.
func routeFromManifest(
	manifest entity.InferencePromptManifest,
	route entity.InferenceRoute,
) (entity.InferenceRoute, error) {
	messages, err := json.Marshal(manifest.Messages)
	if err != nil {
		return entity.InferenceRoute{}, fmt.Errorf("inference route messages: %w", err)
	}

	var schemaObject map[string]any
	if err = json.Unmarshal(manifest.ResponseSchema, &schemaObject); err != nil || schemaObject == nil {
		return entity.InferenceRoute{}, entity.ErrInferenceSchemaInvalid
	}

	schema, err := json.Marshal(schemaObject)
	if err != nil {
		return entity.InferenceRoute{}, fmt.Errorf("inference route schema: %w", err)
	}

	promptHash := sha256.Sum256(messages)
	schemaHash := sha256.Sum256(schema)

	route.TaskKey = manifest.TaskKey
	route.TaskClass = manifest.TaskClass
	route.OutputKind = manifest.OutputKind
	route.CacheTTLSeconds = manifest.CacheTTLSeconds
	route.PersistentEnrichment = manifest.PersistentEnrichment
	route.PromptVersion = manifest.PromptVersion
	route.PromptSHA256 = hex.EncodeToString(promptHash[:])
	route.PolicySHA256 = manifest.PolicySHA256
	route.MessagesTemplate = messages
	route.ResponseSchemaVersion = manifest.ResponseSchemaVersion
	route.ResponseSchemaSHA256 = hex.EncodeToString(schemaHash[:])
	route.ResponseSchema = schema

	return route, nil
}

func (uc *UseCase) resolveRoutes(
	ctx context.Context,
	taskKey, sessionID string,
) ([]entity.InferenceRoute, error) {
	if uc.options.Driver != inferenceDriverDeterministic {
		return uc.repo.ResolveRoutes(ctx, taskKey, sessionID)
	}

	routes := make([]entity.InferenceRoute, 0, 1)

	for i := range uc.ephemeralRoutes {
		if uc.ephemeralRoutes[i].TaskKey == taskKey {
			routes = append(routes, uc.ephemeralRoutes[i])
		}
	}

	if len(routes) == 0 {
		return nil, entity.ErrInferenceRouteMissing
	}

	return routes, nil
}

func (uc *UseCase) primaryPriced() bool {
	return strings.TrimSpace(uc.options.PrimaryPriceVersion) != "" &&
		uc.options.PrimaryInputNanoPerMillion >= 0 &&
		uc.options.PrimaryCachedNanoPerMillion >= 0 &&
		uc.options.PrimaryOutputNanoPerMillion >= 0
}

// Invoke resolves, meters, validates, caches and traces one logical call.
//
//nolint:funlen,gocognit,gocyclo,cyclop,gocritic // linear U-0 safety pipeline and immutable input
func (uc *UseCase) Invoke(
	ctx context.Context,
	input entity.InferenceInvoke,
) (entity.InferenceResult, error) {
	routes, err := uc.resolveRoutes(ctx, strings.TrimSpace(input.TaskKey), input.SessionID)
	if err != nil {
		return entity.InferenceResult{}, err
	}

	if len(routes) > maxProviderAttempts {
		routes = routes[:maxProviderAttempts]
	}

	for i := range routes {
		if routes[i].PolicySHA256 == "" {
			continue
		}

		provided, ok := input.Variables["policy_hash"].(string)
		if !ok {
			return entity.InferenceResult{}, entity.ErrInferenceRegistryConflict
		}

		if !hmac.Equal([]byte(routes[i].PolicySHA256), []byte(strings.TrimSpace(provided))) {
			return entity.InferenceResult{}, entity.ErrInferenceRegistryConflict
		}
	}

	callID := uuid.NewString()
	requestID := requestmeta.RequestID(ctx)

	if requestID == "" {
		requestID = callID
	}

	cacheKeys := make([]string, len(routes))

	for i := range routes {
		cacheKeys[i], err = uc.cacheKey(routes[i], input)
		if err != nil {
			return entity.InferenceResult{}, err
		}
	}

	cacheKey := cacheKeys[0]

	ctx, span := otel.Tracer("surau/inference").Start(ctx, "inference."+routes[0].TaskKey,
		trace.WithAttributes(
			attribute.String("inference.call_id", callID),
			attribute.String("inference.task_key", routes[0].TaskKey),
			attribute.String("inference.task_class", routes[0].TaskClass),
			attribute.String("surau.request_id", requestID),
		))
	defer span.End()

	traceID := span.SpanContext().TraceID().String()
	if !span.SpanContext().TraceID().IsValid() {
		traceID = strings.ReplaceAll(callID, "-", "")
	}

	span.SetAttributes(attribute.String("surau.trace_id", traceID))

	call := entity.InferenceCall{
		ID: callID, TaskKey: routes[0].TaskKey, TaskClass: routes[0].TaskClass,
		RequestID: &requestID, TraceID: optionalString(traceID),
		CacheKey: &cacheKey, CacheStatus: "miss", Status: "started",
	}
	if input.SessionID != "" {
		call.SessionID = &input.SessionID
	}

	if err = uc.repo.CreateCall(ctx, call); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "inference_ledger_failed")

		return entity.InferenceResult{}, err
	}

	for i := range routes {
		if routes[i].CacheTTLSeconds <= 0 || routes[i].PersistentEnrichment ||
			routes[i].TaskClass == entity.InferenceTaskClassJudge {
			continue
		}

		result, hit, cacheErr := uc.cachedResult(ctx, call, cacheKeys[i])
		if cacheErr != nil {
			inferenceCacheHits.WithLabelValues(routes[i].TaskKey, "invalid").Inc()

			continue
		}

		if hit {
			recordResultSpan(span, routes[i], result, "cache_hit", 0)
			inferenceCalls.WithLabelValues(
				routes[i].TaskKey, result.Provider, "cache_hit", "hit",
			).Inc()
			inferenceCacheHits.WithLabelValues(routes[i].TaskKey, "hit").Inc()

			return result, nil
		}
	}

	inferenceCacheHits.WithLabelValues(routes[0].TaskKey, "miss").Inc()

	var maximumCost int64

	rendered := make([][]entity.InferenceMessage, len(routes))

	for i := range routes {
		rendered[i], err = renderMessages(routes[i].MessagesTemplate, input.Variables)
		if err != nil {
			ledgerErr := uc.failCall(
				ctx, &call, "inference_prompt_render_failed", nil, 0,
				entity.InferenceUsage{Source: entity.InferenceUsageEstimated},
			)

			return entity.InferenceResult{}, errors.Join(err, ledgerErr)
		}

		maximumCost += maximumRouteCost(routes[i], rendered[i])
	}

	budget, err := uc.repo.ReserveBudget(ctx, callID, maximumCost)
	uc.recordBudgetMetrics(&budget)

	if err != nil {
		var exceeded *entity.InferenceBudgetExceededError
		if errors.As(err, &exceeded) {
			inferenceBudgetRejections.WithLabelValues(exceeded.Window).Inc()
			span.SetStatus(codes.Error, "inference_budget_exceeded")
			span.SetAttributes(
				attribute.String("inference.outcome", "budget_rejected"),
				attribute.String("inference.provider", routes[0].ProviderKey),
				attribute.String("inference.model", routes[0].ProviderModelID),
				attribute.String("inference.prompt_version", routes[0].PromptVersion),
				attribute.String("inference.prompt_sha256", routes[0].PromptSHA256),
				attribute.String("inference.policy_sha256", routes[0].PolicySHA256),
				attribute.String("inference.response_schema_version", routes[0].ResponseSchemaVersion),
				attribute.String("inference.response_schema_sha256", routes[0].ResponseSchemaSHA256),
				attribute.String("inference.price_version", routes[0].PriceVersion),
				attribute.String("inference.generation_run_id", ""),
				attribute.String("inference.generation_status", "not_created_budget_rejected"),
				attribute.Int64("inference.input_tokens", 0),
				attribute.Int64("inference.cached_input_tokens", 0),
				attribute.Int64("inference.output_tokens", 0),
				attribute.Int64("inference.cost_nano_usd", 0),
				attribute.String("inference.cost_usd", "0.000000000"),
				attribute.String("inference.usage_source", entity.InferenceUsageEstimated),
				attribute.String("inference.cost_source", entity.InferenceCostRegistry),
				attribute.String("inference.cache_status", "miss"),
				attribute.Bool("inference.failover", false),
				attribute.Int("inference.attempt_count", 0),
			)
			inferenceCalls.WithLabelValues(
				routes[0].TaskKey, routes[0].ProviderKey, "budget_rejected", "miss",
			).Inc()
		} else {
			ledgerErr := uc.failCall(
				ctx,
				&call,
				"inference_budget_check_failed",
				nil,
				0,
				entity.InferenceUsage{Source: entity.InferenceUsageEstimated},
			)

			err = errors.Join(err, ledgerErr)
		}

		return entity.InferenceResult{}, err
	}

	var (
		totalUsage       entity.InferenceUsage
		totalCost        int64
		totalCostSource  string
		lastErr          error
		lastRoute        = routes[0]
		lastGenerationID string
		attemptCount     int
	)
	for i := range routes {
		route := routes[i]
		attempt := entity.InferenceAttempt{
			ID: uuid.NewString(), CallID: callID, AttemptNo: i + 1,
			Generation: entity.GenerationRun{
				ID: uuid.NewString(), TaskName: route.TaskKey,
				ModelID: route.ProviderModelID, PromptVersion: route.PromptVersion,
				Provider: &route.ProviderKey,
			},
			Route: route, Status: "started", Failover: i > 0,
		}
		lastRoute = route
		lastGenerationID = attempt.Generation.ID
		attemptCount = i + 1

		if err = uc.repo.CreateAttempt(ctx, attempt); err != nil {
			ledgerErr := uc.failCall(
				ctx,
				&call,
				"inference_attempt_ledger_failed",
				nil,
				totalCost,
				totalUsage,
			)
			settleErr := uc.repo.SettleBudget(ctx, callID)

			return entity.InferenceResult{}, errors.Join(err, ledgerErr, settleErr)
		}

		attemptCtx, attemptSpan := otel.Tracer("surau/inference").Start(
			ctx, "inference.provider."+route.ProviderKey,
			trace.WithAttributes(attemptAttributes(attempt, requestID, traceID)...),
		)

		var (
			providerResponse entity.InferenceProviderResponse
			providerErr      error
		)

		providerRequest := entity.InferenceProviderRequest{
			Model: route.ProviderModelID, Messages: rendered[i],
			MaxTokens: route.MaxOutputTokens, Temperature: route.Temperature,
			JSONOutput: route.OutputKind == "structured",
		}

		if route.OutputKind == "embedding" {
			providerResponse, providerErr = uc.provider.Embed(attemptCtx, route, providerRequest)
		} else {
			providerResponse, providerErr = uc.provider.Chat(attemptCtx, route, providerRequest)
		}

		usage := normalizedUsage(rendered[i], providerResponse)
		if providerErr != nil && providerResponse.OutputTokens <= 0 {
			// A failed provider that omits usage must not become artificially
			// cheap. Reserve/settle the configured output ceiling.
			usage.OutputTokens = int64(route.MaxOutputTokens)
			usage.Source = entity.InferenceUsageEstimated
		}

		cost, costSource := routeCost(route, usage, providerResponse.CostNanoUSD)
		attempt.Usage, attempt.CostNanoUSD, attempt.CostSource = usage, cost, costSource
		totalUsage = addUsage(totalUsage, usage)
		totalCost += cost

		if totalCostSource == "" {
			totalCostSource = costSource
		} else if totalCostSource != costSource {
			totalCostSource = entity.InferenceCostRegistry
		}

		if providerErr == nil {
			providerResponse.Output = cleanJSONOutput(providerResponse.Output)

			providerErr = validateResponse(route.ResponseSchema, providerResponse.Output)
			if providerErr != nil {
				providerErr = &entity.InferenceProviderError{
					Retryable: true, Cause: providerErr,
				}
			}
		}

		if providerErr != nil {
			attempt.Status = "failed"
			code := providerErrorCode(providerErr)

			attempt.ErrorCode = &code
			if err = uc.repo.FinishAttempt(ctx, attempt); err != nil {
				attemptSpan.RecordError(err)
				attemptSpan.End()

				settleErr := uc.repo.SettleBudget(ctx, callID)

				return entity.InferenceResult{}, errors.Join(err, settleErr)
			}

			uc.recordUsageMetrics(route, usage, cost, costSource)
			attemptSpan.RecordError(fmt.Errorf("%w: %s", errInferenceAttemptTrace, code))
			attemptSpan.SetStatus(codes.Error, code)
			attemptSpan.SetAttributes(
				attribute.Int64("inference.input_tokens", usage.InputTokens),
				attribute.Int64("inference.cached_input_tokens", usage.CachedInputTokens),
				attribute.Int64("inference.output_tokens", usage.OutputTokens),
				attribute.Int64("inference.cost_nano_usd", cost),
				attribute.String("inference.cost_usd", nanoUSDString(cost)),
				attribute.String("inference.usage_source", usage.Source),
				attribute.String("inference.cost_source", costSource),
				attribute.String("inference.cache_status", "miss"),
				attribute.String("inference.outcome", "failed"),
			)
			attemptSpan.End()

			lastErr = providerErr

			if !retryableProviderError(providerErr) || i+1 >= len(routes) {
				break
			}

			inferenceFailovers.WithLabelValues(
				route.TaskKey, route.ProviderKey, routes[i+1].ProviderKey,
			).Inc()

			continue
		}

		attempt.Status = "succeeded"
		if err = uc.repo.FinishAttempt(ctx, attempt); err != nil {
			attemptSpan.RecordError(err)
			attemptSpan.End()

			settleErr := uc.repo.SettleBudget(ctx, callID)

			return entity.InferenceResult{}, errors.Join(err, settleErr)
		}

		attemptSpan.SetStatus(codes.Ok, "succeeded")
		attemptSpan.SetAttributes(
			attribute.Int64("inference.input_tokens", usage.InputTokens),
			attribute.Int64("inference.cached_input_tokens", usage.CachedInputTokens),
			attribute.Int64("inference.output_tokens", usage.OutputTokens),
			attribute.Int64("inference.cost_nano_usd", cost),
			attribute.String("inference.cost_usd", nanoUSDString(cost)),
			attribute.String("inference.usage_source", usage.Source),
			attribute.String("inference.cost_source", costSource),
			attribute.String("inference.cache_status", "miss"),
			attribute.String("inference.outcome", "succeeded"),
		)
		attemptSpan.End()
		uc.recordUsageMetrics(route, usage, cost, costSource)

		call.Status = "succeeded"
		call.GenerationID = &attempt.Generation.ID
		call.Usage = totalUsage
		call.CostNanoUSD = totalCost
		call.CostSource = totalCostSource
		call.CacheStatus = "miss"

		if input.SessionID != "" {
			if err = uc.repo.PinSession(
				ctx, input.SessionID, route.ProviderKey, route.ModelKey,
			); err != nil {
				sessionErr := uc.repo.FailSession(ctx, input.SessionID)
				ledgerErr := uc.failCall(
					ctx,
					&call,
					"inference_session_pin_failed",
					&attempt.Generation.ID,
					totalCost,
					totalUsage,
				)
				settleErr := uc.repo.SettleBudget(ctx, callID)

				return entity.InferenceResult{}, errors.Join(
					entity.ErrInferenceSessionPinned, err, sessionErr, ledgerErr, settleErr,
				)
			}
		}

		if err = uc.repo.FinishCall(ctx, call); err != nil {
			settleErr := uc.repo.SettleBudget(ctx, callID)

			return entity.InferenceResult{}, errors.Join(err, settleErr)
		}

		result := entity.InferenceResult{
			CallID: callID, Output: providerResponse.Output,
			Generation: attempt.Generation.Identity(), Provider: route.ProviderKey,
			Model: route.ProviderModelID, Prompt: route.PromptVersion,
			Schema: route.ResponseSchemaVersion, Usage: totalUsage,
			Cost: entity.InferenceCost{
				NanoUSD: totalCost, USD: nanoUSDString(totalCost),
				Source: totalCostSource, PriceVersion: route.PriceVersion,
			},
			CacheStatus: "miss", Failover: i > 0,
		}
		if route.CacheTTLSeconds > 0 && !route.PersistentEnrichment &&
			route.TaskClass != entity.InferenceTaskClassJudge {
			if cacheErr := uc.putCache(
				ctx, cacheKeys[i], route, result, attempt.Generation,
			); cacheErr != nil {
				span.RecordError(cacheErr)
				inferenceCacheHits.WithLabelValues(route.TaskKey, "write_failed").Inc()
			}
		}

		if err = uc.repo.SettleBudget(ctx, callID); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "inference_budget_settlement_failed")

			return entity.InferenceResult{}, err
		}

		uc.refreshBudgetMetrics(ctx)
		recordResultSpan(span, route, result, "succeeded", i+1)
		inferenceCalls.WithLabelValues(
			route.TaskKey, route.ProviderKey, "succeeded", "miss",
		).Inc()

		return result, nil
	}

	code := "inference_provider_unavailable"
	call.Status = "failed"
	call.Usage = totalUsage
	call.CostNanoUSD = totalCost
	call.CostSource = totalCostSource

	call.ErrorCode = &code
	if err = uc.repo.FinishCall(ctx, call); err != nil {
		settleErr := uc.repo.SettleBudget(ctx, callID)

		return entity.InferenceResult{}, errors.Join(err, settleErr)
	}

	if err = uc.repo.SettleBudget(ctx, callID); err != nil {
		return entity.InferenceResult{}, err
	}

	span.SetStatus(codes.Error, code)
	span.SetAttributes(
		attribute.String("inference.outcome", "failed"),
		attribute.String("inference.provider", lastRoute.ProviderKey),
		attribute.String("inference.model", lastRoute.ProviderModelID),
		attribute.String("inference.prompt_version", lastRoute.PromptVersion),
		attribute.String("inference.prompt_sha256", lastRoute.PromptSHA256),
		attribute.String("inference.policy_sha256", lastRoute.PolicySHA256),
		attribute.String("inference.response_schema_version", lastRoute.ResponseSchemaVersion),
		attribute.String("inference.response_schema_sha256", lastRoute.ResponseSchemaSHA256),
		attribute.String("inference.price_version", lastRoute.PriceVersion),
		attribute.String("inference.generation_run_id", lastGenerationID),
		attribute.Int64("inference.input_tokens", totalUsage.InputTokens),
		attribute.Int64("inference.cached_input_tokens", totalUsage.CachedInputTokens),
		attribute.Int64("inference.output_tokens", totalUsage.OutputTokens),
		attribute.Int64("inference.cost_nano_usd", totalCost),
		attribute.String("inference.cost_usd", nanoUSDString(totalCost)),
		attribute.String("inference.usage_source", totalUsage.Source),
		attribute.String("inference.cost_source", totalCostSource),
		attribute.String("inference.cache_status", "miss"),
		attribute.Bool("inference.failover", attemptCount > 1),
		attribute.Int("inference.attempt_count", attemptCount),
	)
	inferenceCalls.WithLabelValues(
		routes[0].TaskKey, routes[0].ProviderKey, "failed", "miss",
	).Inc()

	if input.SessionID != "" && len(routes) == 1 {
		sessionErr := uc.repo.FailSession(ctx, input.SessionID)

		return entity.InferenceResult{}, errors.Join(
			entity.ErrInferenceSessionPinned, lastErr, sessionErr,
		)
	}

	return entity.InferenceResult{}, errors.Join(entity.ErrInferenceProviderFailure, lastErr)
}

func (uc *UseCase) CreateSession(
	ctx context.Context,
	taskKey string,
) (entity.InferenceSession, error) {
	if _, err := uc.resolveRoutes(ctx, taskKey, ""); err != nil {
		return entity.InferenceSession{}, err
	}

	return uc.repo.CreateSession(ctx, taskKey)
}

func (uc *UseCase) Budget(
	ctx context.Context,
) (entity.InferenceBudgetStatus, error) {
	status, err := uc.repo.Budget(ctx, uc.now())
	if err == nil {
		uc.recordBudgetMetrics(&status)
	}

	return status, err
}

func (uc *UseCase) UpdateBudget(
	ctx context.Context,
	actorID string,
	expectedRevision int64,
	patch entity.InferenceBudgetPatch,
) (entity.InferenceBudgetStatus, error) {
	return uc.repo.UpdateBudget(ctx, actorID, expectedRevision, patch)
}

func (uc *UseCase) Usage(
	ctx context.Context,
	from, to time.Time,
	groupBy string,
) ([]entity.InferenceUsageRow, error) {
	return uc.repo.Usage(ctx, from, to, groupBy)
}

func (uc *UseCase) Registry(ctx context.Context) ([]entity.InferenceRoute, error) {
	return uc.repo.Registry(ctx)
}

//nolint:gocritic // Manifest is an immutable API/registry value.
func (uc *UseCase) RegisterPrompt(
	ctx context.Context,
	manifest entity.InferencePromptManifest,
) error {
	if strings.TrimSpace(manifest.TaskKey) == "" ||
		!validTaskClass(manifest.TaskClass) ||
		strings.TrimSpace(manifest.PromptVersion) == "" ||
		strings.TrimSpace(manifest.ResponseSchemaVersion) == "" ||
		len(manifest.Messages) == 0 ||
		len(manifest.ResponseSchema) == 0 {
		return entity.ErrInferenceRegistryConflict
	}

	return uc.repo.SyncManifest(ctx, []entity.InferencePromptManifest{manifest})
}

func validTaskClass(taskClass string) bool {
	switch strings.TrimSpace(taskClass) {
	case entity.InferenceTaskClassRewrite,
		entity.InferenceTaskClassRerank,
		entity.InferenceTaskClassEmbed,
		entity.InferenceTaskClassAnswer,
		entity.InferenceTaskClassJudge:
		return true
	default:
		return false
	}
}

func renderMessages(
	raw entity.RawJSON,
	variables map[string]any,
) ([]entity.InferenceMessage, error) {
	var templates []entity.InferenceMessage
	if err := json.Unmarshal(raw, &templates); err != nil {
		return nil, fmt.Errorf("inference prompt decode: %w", err)
	}

	rendered := make([]entity.InferenceMessage, len(templates))

	for i := range templates {
		var output bytes.Buffer

		tmpl, err := template.New("prompt").Option("missingkey=error").Parse(templates[i].Content)
		if err != nil {
			return nil, fmt.Errorf("inference prompt compile: %w", err)
		}

		if err = tmpl.Execute(&output, variables); err != nil {
			return nil, fmt.Errorf("inference prompt render: %w", err)
		}

		rendered[i] = entity.InferenceMessage{
			Role: templates[i].Role, Content: output.String(),
		}
	}

	return rendered, nil
}

func validateResponse(schemaRaw entity.RawJSON, output string) error {
	var (
		schemaDocument any
		value          any
	)
	if err := json.Unmarshal(schemaRaw, &schemaDocument); err != nil {
		return fmt.Errorf("%w: schema compile input", entity.ErrInferenceSchemaInvalid)
	}

	if err := json.Unmarshal([]byte(output), &value); err != nil {
		return fmt.Errorf("%w: response is not JSON", entity.ErrInferenceSchemaInvalid)
	}

	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("https://surau.org/inference/schema.json", schemaDocument); err != nil {
		return errors.Join(entity.ErrInferenceSchemaInvalid, err)
	}

	schema, err := compiler.Compile("https://surau.org/inference/schema.json")
	if err != nil {
		return errors.Join(entity.ErrInferenceSchemaInvalid, err)
	}

	if err = schema.Validate(value); err != nil {
		return errors.Join(entity.ErrInferenceSchemaInvalid, err)
	}

	return nil
}

func cleanJSONOutput(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "```") {
		value = strings.TrimPrefix(value, "```json")
		value = strings.TrimPrefix(value, "```JSON")
		value = strings.TrimPrefix(value, "```")
		value = strings.TrimSuffix(value, "```")
	}

	return strings.TrimSpace(value)
}

func normalizedUsage(
	messages []entity.InferenceMessage,
	response entity.InferenceProviderResponse,
) entity.InferenceUsage {
	usage := entity.InferenceUsage{
		InputTokens: response.InputTokens, CachedInputTokens: response.CachedInputTokens,
		OutputTokens: response.OutputTokens, Source: entity.InferenceUsageProvider,
	}
	estimated := false

	if usage.InputTokens <= 0 {
		usage.InputTokens = estimateMessageTokens(messages)
		estimated = true
	}

	if usage.OutputTokens <= 0 && response.Output != "" {
		usage.OutputTokens = estimateTokens(response.Output)
		estimated = true
	}

	if estimated {
		usage.Source = entity.InferenceUsageEstimated
	}

	return usage
}

func estimateMessageTokens(messages []entity.InferenceMessage) int64 {
	var total int64
	for i := range messages {
		total += estimateTokens(messages[i].Role)
		total += estimateTokens(messages[i].Content)
		total += 4
	}

	return max(total, 1)
}

func estimateTokens(value string) int64 {
	// A byte-level upper estimate is intentionally expensive but safe for
	// Arabic and Indonesian: it prevents missing provider usage from making a
	// call artificially cheap or weakening a concurrent budget reservation.
	return max(int64(len([]byte(value))), 1)
}

//nolint:gocritic // Route is an immutable registry snapshot used only for arithmetic.
func maximumRouteCost(
	route entity.InferenceRoute,
	messages []entity.InferenceMessage,
) int64 {
	usage := entity.InferenceUsage{
		InputTokens:  estimateMessageTokens(messages),
		OutputTokens: int64(route.MaxOutputTokens),
	}
	cost, _ := routeCost(route, usage, nil)

	return cost
}

//nolint:gocritic // Route is an immutable registry snapshot used only for arithmetic.
func routeCost(
	route entity.InferenceRoute,
	usage entity.InferenceUsage,
	providerCost *int64,
) (cost int64, source string) {
	if providerCost != nil && *providerCost >= 0 {
		return *providerCost, entity.InferenceCostProvider
	}

	uncached := max(usage.InputTokens-usage.CachedInputTokens, 0)

	numerator := uncached*route.InputNanoPerMillion +
		usage.CachedInputTokens*route.CachedNanoPerMillion +
		usage.OutputTokens*route.OutputNanoPerMillion
	if numerator == 0 {
		return 0, entity.InferenceCostRegistry
	}

	return (numerator + priceTokenUnit - 1) / priceTokenUnit, entity.InferenceCostRegistry
}

//nolint:gocritic // Input is an immutable key snapshot and is not retained.
func (uc *UseCase) cacheKey(
	route entity.InferenceRoute,
	input entity.InferenceInvoke,
) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"task": route.TaskKey, "prompt_version": route.PromptVersion,
		"prompt_hash": route.PromptSHA256, "schema_version": route.ResponseSchemaVersion,
		"schema_hash": route.ResponseSchemaSHA256, "provider": route.ProviderKey,
		"model": route.ProviderModelID, "variables": input.Variables,
		"vary": input.CacheVary, "source_version": input.SourceVersion,
		"index_version": input.IndexVersion, "temperature": route.Temperature,
		"max_output_tokens": route.MaxOutputTokens,
	})
	if err != nil {
		return "", fmt.Errorf("inference cache key: %w", err)
	}

	mac := hmac.New(sha256.New, uc.cacheHMAC)
	_, _ = mac.Write(payload)

	return hex.EncodeToString(mac.Sum(nil)), nil
}

type cachedPayload struct {
	Output       string                    `json:"output"`
	Generation   entity.GenerationIdentity `json:"generation"`
	Provider     string                    `json:"provider"`
	Model        string                    `json:"model"`
	Prompt       string                    `json:"prompt_version"`
	Schema       string                    `json:"response_schema_version"`
	PriceVersion string                    `json:"price_version"`
}

//nolint:gocritic // Call is updated once for an immutable cache-hit ledger record.
func (uc *UseCase) cachedResult(
	ctx context.Context,
	call entity.InferenceCall,
	key string,
) (entity.InferenceResult, bool, error) {
	var payload cachedPayload

	entry, found, err := uc.repo.GetCache(ctx, key)
	if err != nil || !found {
		return entity.InferenceResult{}, false, err
	}

	plaintext, err := uc.cacheBox.OpenWithAAD(entry.Ciphertext, []byte(key))
	if err != nil {
		return entity.InferenceResult{}, false, nil
	}

	if err = json.Unmarshal(plaintext, &payload); err != nil {
		return entity.InferenceResult{}, false, nil
	}

	call.Status = "cache_hit"
	call.GenerationID = &payload.Generation.RunID
	call.CacheStatus = "hit"
	call.Usage = entity.InferenceUsage{Source: entity.InferenceUsageCache}

	call.CostSource = entity.InferenceCostCache
	if err = uc.repo.FinishCall(ctx, call); err != nil {
		return entity.InferenceResult{}, false, err
	}

	return entity.InferenceResult{
		CallID: call.ID, Output: payload.Output, Generation: payload.Generation,
		Provider: payload.Provider, Model: payload.Model, Prompt: payload.Prompt,
		Schema: payload.Schema, Usage: call.Usage,
		Cost: entity.InferenceCost{
			NanoUSD: 0, USD: nanoUSDString(0), Source: entity.InferenceCostCache,
			PriceVersion: payload.PriceVersion,
		},
		CacheStatus: "hit",
	}, true, nil
}

//nolint:gocritic // Cache material is copied once across the persistence boundary.
func (uc *UseCase) putCache(
	ctx context.Context,
	key string,
	route entity.InferenceRoute,
	result entity.InferenceResult,
	generation entity.GenerationRun,
) error {
	plaintext, err := json.Marshal(cachedPayload{
		Output: result.Output, Generation: result.Generation,
		Provider: result.Provider, Model: result.Model, Prompt: result.Prompt,
		Schema: result.Schema, PriceVersion: result.Cost.PriceVersion,
	})
	if err != nil {
		return err
	}

	ciphertext, err := uc.cacheBox.SealWithAAD(plaintext, []byte(key))
	if err != nil {
		return err
	}

	return uc.repo.PutCache(ctx, entity.InferenceCacheEntry{
		CacheKey: key, TaskKey: route.TaskKey, GenerationRun: generation,
		Ciphertext: ciphertext,
		ExpiresAt:  uc.now().UTC().Add(time.Duration(route.CacheTTLSeconds) * time.Second),
	})
}

func retryableProviderError(err error) bool {
	var providerErr *entity.InferenceProviderError

	return errors.As(err, &providerErr) && providerErr.Retryable
}

func providerErrorCode(err error) string {
	var providerErr *entity.InferenceProviderError

	if errors.Is(err, entity.ErrInferenceSchemaInvalid) {
		return "inference_response_schema_invalid"
	}

	if errors.As(err, &providerErr) {
		if errors.Is(providerErr.Cause, entity.ErrInferenceSchemaInvalid) {
			return "inference_response_schema_invalid"
		}

		if providerErr.StatusCode != 0 {
			return "inference_provider_http_" + strconv.Itoa(providerErr.StatusCode)
		}
	}

	return "inference_provider_error"
}

func addUsage(left, right entity.InferenceUsage) entity.InferenceUsage {
	source := left.Source
	if source == "" {
		source = right.Source
	}

	if source != right.Source {
		source = entity.InferenceUsageEstimated
	}

	return entity.InferenceUsage{
		InputTokens:       left.InputTokens + right.InputTokens,
		CachedInputTokens: left.CachedInputTokens + right.CachedInputTokens,
		OutputTokens:      left.OutputTokens + right.OutputTokens,
		Source:            source,
	}
}

//nolint:gocritic // Attempt is flattened immediately into bounded trace attributes.
func attemptAttributes(
	attempt entity.InferenceAttempt,
	requestID string,
	traceID string,
) []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String("inference.call_id", attempt.CallID),
		attribute.String("surau.request_id", requestID),
		attribute.String("surau.trace_id", traceID),
		attribute.Int("inference.attempt_no", attempt.AttemptNo),
		attribute.String("inference.task_key", attempt.Route.TaskKey),
		attribute.String("inference.task_class", attempt.Route.TaskClass),
		attribute.String("inference.provider", attempt.Route.ProviderKey),
		attribute.String("inference.model", attempt.Route.ProviderModelID),
		attribute.String("inference.prompt_version", attempt.Route.PromptVersion),
		attribute.String("inference.prompt_sha256", attempt.Route.PromptSHA256),
		attribute.String("inference.policy_sha256", attempt.Route.PolicySHA256),
		attribute.String("inference.response_schema_version", attempt.Route.ResponseSchemaVersion),
		attribute.String("inference.response_schema_sha256", attempt.Route.ResponseSchemaSHA256),
		attribute.String("inference.generation_run_id", attempt.Generation.ID),
		attribute.String("inference.price_version", attempt.Route.PriceVersion),
		attribute.String("inference.cache_status", "miss"),
		attribute.Bool("inference.failover", attempt.Failover),
	}
}

//nolint:gocritic // Result is flattened immediately and never retained.
func recordResultSpan(
	span trace.Span,
	route entity.InferenceRoute,
	result entity.InferenceResult,
	outcome string,
	attempts int,
) {
	span.SetStatus(codes.Ok, outcome)
	span.SetAttributes(
		attribute.String("inference.outcome", outcome),
		attribute.String("inference.provider", result.Provider),
		attribute.String("inference.model", result.Model),
		attribute.String("inference.prompt_version", result.Prompt),
		attribute.String("inference.prompt_sha256", route.PromptSHA256),
		attribute.String("inference.policy_sha256", route.PolicySHA256),
		attribute.String("inference.response_schema_version", result.Schema),
		attribute.String("inference.response_schema_sha256", route.ResponseSchemaSHA256),
		attribute.String("inference.price_version", result.Cost.PriceVersion),
		attribute.String("inference.generation_run_id", result.Generation.RunID),
		attribute.Int64("inference.input_tokens", result.Usage.InputTokens),
		attribute.Int64("inference.cached_input_tokens", result.Usage.CachedInputTokens),
		attribute.Int64("inference.output_tokens", result.Usage.OutputTokens),
		attribute.Int64("inference.cost_nano_usd", result.Cost.NanoUSD),
		attribute.String("inference.cost_usd", result.Cost.USD),
		attribute.String("inference.usage_source", result.Usage.Source),
		attribute.String("inference.cost_source", result.Cost.Source),
		attribute.String("inference.cache_status", result.CacheStatus),
		attribute.Bool("inference.failover", result.Failover),
		attribute.Int("inference.attempt_count", attempts),
	)
}

//nolint:gocritic // Route is flattened immediately into bounded metric labels.
func (uc *UseCase) recordUsageMetrics(
	route entity.InferenceRoute,
	usage entity.InferenceUsage,
	cost int64,
	costSource string,
) {
	inferenceTokens.WithLabelValues(
		route.TaskKey, route.ProviderKey, "input", usage.Source,
	).Add(float64(usage.InputTokens))
	inferenceTokens.WithLabelValues(
		route.TaskKey, route.ProviderKey, "cached_input", usage.Source,
	).Add(float64(usage.CachedInputTokens))
	inferenceTokens.WithLabelValues(
		route.TaskKey, route.ProviderKey, "output", usage.Source,
	).Add(float64(usage.OutputTokens))
	inferenceCost.WithLabelValues(
		route.TaskKey, route.ProviderKey, costSource,
	).Add(float64(cost))
	inferenceCostByUsageSource.WithLabelValues(
		route.TaskKey, route.ProviderKey, usage.Source,
	).Add(float64(cost))
}

func (uc *UseCase) refreshBudgetMetrics(ctx context.Context) {
	status, err := uc.repo.Budget(ctx, uc.now())
	if err != nil {
		return
	}

	uc.recordBudgetMetrics(&status)
}

func (uc *UseCase) recordBudgetMetrics(status *entity.InferenceBudgetStatus) {
	for _, mode := range []string{"baseline", "enforce", "disabled"} {
		inferenceBudgetRatio.WithLabelValues("daily", mode).Set(0)
		inferenceBudgetRatio.WithLabelValues("monthly", mode).Set(0)
	}

	daily, monthly := float64(0), float64(0)
	if status.DailyCapNanoUSD != nil && *status.DailyCapNanoUSD > 0 {
		daily = float64(status.DailyUsedNanoUSD+status.ReservedNanoUSD) /
			float64(*status.DailyCapNanoUSD)
	}

	if status.MonthlyCapNanoUSD != nil && *status.MonthlyCapNanoUSD > 0 {
		monthly = float64(status.MonthlyUsedNanoUSD+status.ReservedNanoUSD) /
			float64(*status.MonthlyCapNanoUSD)
	}

	inferenceBudgetRatio.WithLabelValues("daily", status.Mode).Set(daily)
	inferenceBudgetRatio.WithLabelValues("monthly", status.Mode).Set(monthly)

	progress := float64(0)

	window := status.BaselineEndsAt.Sub(status.BaselineStartedAt)
	if window > 0 {
		progress = min(max(uc.now().Sub(status.BaselineStartedAt).Seconds()/window.Seconds(), 0), 1)
	}

	inferenceBaselineProgress.Set(progress)

	configAlert := float64(0)
	if status.ConfigAlert != "" {
		configAlert = 1
	}

	inferenceBudgetConfigAlert.WithLabelValues("zero_baseline").Set(configAlert)
}

func (uc *UseCase) failCall(
	ctx context.Context,
	call *entity.InferenceCall,
	code string,
	generationID *string,
	cost int64,
	usage entity.InferenceUsage,
) error {
	call.Status = "failed"
	call.GenerationID = generationID
	call.CostNanoUSD = cost
	call.CostSource = entity.InferenceCostRegistry
	call.Usage = usage
	call.ErrorCode = &code

	return uc.repo.FinishCall(ctx, *call)
}

func nanoUSDString(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}

	return fmt.Sprintf("%s%d.%09d", sign, value/nanoPerUSD, value%nanoPerUSD)
}

func optionalString(value string) *string {
	if value == "" || value == strings.Repeat("0", fallbackTraceIDHexLength) {
		return nil
	}

	return &value
}

// ProviderCredentialReadiness reports only missing variable names, never
// credential values. It is used by dev verification and readiness tooling.
func (uc *UseCase) ProviderCredentialReadiness(
	ctx context.Context,
) ([]string, error) {
	if uc.options.Driver == inferenceDriverDeterministic {
		return nil, nil
	}

	routes, err := uc.repo.Registry(ctx)
	if err != nil {
		return nil, err
	}

	missing := make([]string, 0)
	seen := make(map[string]struct{})
	providers := make(map[string]struct{})

	for i := range routes {
		providers[routes[i].ProviderKey] = struct{}{}
		if routes[i].ProviderKey == inferenceDriverDeterministic {
			continue
		}

		if strings.TrimSpace(os.Getenv(routes[i].APIKeyEnv)) != "" {
			continue
		}

		if _, ok := seen[routes[i].APIKeyEnv]; ok {
			continue
		}

		missing = append(missing, routes[i].APIKeyEnv)
		seen[routes[i].APIKeyEnv] = struct{}{}
	}

	missing = append(missing, uc.missingProviderRoutes(providers)...)

	return missing, nil
}

func (uc *UseCase) missingProviderRoutes(providers map[string]struct{}) []string {
	if uc.options.Driver == inferenceDriverDeterministic {
		return nil
	}

	missing := make([]string, 0, maxProviderAttempts)

	if _, ok := providers["sumopod"]; !ok {
		missing = append(missing, "INFERENCE_SUMOPOD_CATALOG_ROUTE")
	}

	if uc.options.DisableSecondary {
		return missing
	}

	if _, ok := providers["deepseek"]; !ok {
		missing = append(missing, "INFERENCE_DEEPSEEK_ROUTE")
	}

	return missing
}
