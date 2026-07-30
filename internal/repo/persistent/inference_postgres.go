package persistent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Scratch production images need the Jakarta budget boundary data.

	"github.com/alfariesh/surau-backend/internal/entity"
	domainrepo "github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	maxInferenceRoutes              = 2
	baselineCapMultiplier           = 2
	zeroBaselineExtensionReason     = "automatic zero-cost baseline extension"
	zeroBaselineConfigurationNotice = "baseline is zero; enforcement postponed until metered usage exists"
)

var _ domainrepo.InferenceRepo = (*InferenceRepo)(nil)

// InferenceRepo persists U-0 registry, metering, budget and encrypted cache.
type InferenceRepo struct {
	*postgres.Postgres
	now func() time.Time
}

func NewInferenceRepo(pg *postgres.Postgres) *InferenceRepo {
	return &InferenceRepo{Postgres: pg, now: time.Now}
}

//nolint:funlen // Transaction verifies every immutable prompt/schema row before commit.
func (r *InferenceRepo) SyncManifest(
	ctx context.Context,
	manifests []entity.InferencePromptManifest,
) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("InferenceRepo.SyncManifest begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for i := range manifests {
		manifest := manifests[i]

		messages, marshalErr := json.Marshal(manifest.Messages)
		if marshalErr != nil {
			return fmt.Errorf("InferenceRepo.SyncManifest messages: %w", marshalErr)
		}

		schema, marshalErr := canonicalJSONObject(manifest.ResponseSchema)
		if marshalErr != nil {
			return fmt.Errorf("InferenceRepo.SyncManifest schema: %w", marshalErr)
		}

		promptHash := sha256Hex(messages)
		schemaHash := sha256Hex(schema)

		tag, execErr := tx.Exec(
			ctx, `
INSERT INTO inference_tasks (
    task_key, task_class, output_kind, cache_ttl_seconds, persistent_enrichment
) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (task_key) DO UPDATE
SET task_key = EXCLUDED.task_key
WHERE inference_tasks.task_class = EXCLUDED.task_class
  AND inference_tasks.output_kind = EXCLUDED.output_kind
  AND inference_tasks.cache_ttl_seconds = EXCLUDED.cache_ttl_seconds
  AND inference_tasks.persistent_enrichment = EXCLUDED.persistent_enrichment`,
			manifest.TaskKey, manifest.TaskClass, manifest.OutputKind,
			manifest.CacheTTLSeconds, manifest.PersistentEnrichment,
		)
		if execErr != nil {
			return fmt.Errorf("InferenceRepo.SyncManifest task %s: %w", manifest.TaskKey, execErr)
		}

		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: task %s", entity.ErrInferenceRegistryConflict, manifest.TaskKey)
		}

		if insertErr := insertImmutableRegistryRow(
			ctx, tx, "inference_prompt_versions", manifest.TaskKey,
			manifest.PromptVersion, messages, promptHash, manifest.PolicySHA256,
		); insertErr != nil {
			return insertErr
		}

		if insertErr := insertImmutableRegistryRow(
			ctx, tx, "inference_response_schemas", manifest.TaskKey,
			manifest.ResponseSchemaVersion, schema, schemaHash, "",
		); insertErr != nil {
			return insertErr
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("InferenceRepo.SyncManifest commit: %w", err)
	}

	return nil
}

//nolint:funlen // Prompt/schema verification remains one atomic registry operation.
func insertImmutableRegistryRow(
	ctx context.Context,
	tx pgx.Tx,
	table, taskKey, version string,
	body []byte,
	hash string,
	policySHA256 string,
) error {
	columns := "task_key, version, messages_template, content_sha256, policy_sha256"
	values := "$1, $2, $3::jsonb, $4, NULLIF($5, '')"

	if table == "inference_response_schemas" {
		columns = "task_key, version, schema_body, content_sha256"
		values = "$1, $2, $3::jsonb, $4"
	}

	query := fmt.Sprintf(`
INSERT INTO %s (%s)
	VALUES (%s)
	ON CONFLICT (task_key, version) DO NOTHING`, table, columns, values)

	args := []any{taskKey, version, body, hash}
	if table == "inference_prompt_versions" {
		args = append(args, policySHA256)
	}

	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("InferenceRepo.SyncManifest insert %s %s/%s: %w", table, taskKey, version, err)
	}

	var (
		storedHash   string
		storedPolicy *string
	)
	if table == "inference_prompt_versions" {
		if err := tx.QueryRow(
			ctx,
			`SELECT content_sha256, policy_sha256
FROM inference_prompt_versions WHERE task_key = $1 AND version = $2`,
			taskKey,
			version,
		).Scan(&storedHash, &storedPolicy); err != nil {
			return fmt.Errorf(
				"InferenceRepo.SyncManifest verify %s %s/%s: %w",
				table,
				taskKey,
				version,
				err,
			)
		}

		if stringValue(storedPolicy) != policySHA256 {
			return fmt.Errorf("%w: %s %s/%s policy", entity.ErrInferenceRegistryConflict, table, taskKey, version)
		}

		if storedHash != hash {
			return fmt.Errorf("%w: %s %s/%s", entity.ErrInferenceRegistryConflict, table, taskKey, version)
		}

		return nil
	}

	selectQuery := fmt.Sprintf(
		"SELECT content_sha256 FROM %s WHERE task_key = $1 AND version = $2", table,
	)
	if err := tx.QueryRow(ctx, selectQuery, taskKey, version).Scan(&storedHash); err != nil {
		return fmt.Errorf("InferenceRepo.SyncManifest verify %s %s/%s: %w", table, taskKey, version, err)
	}

	if storedHash != hash {
		return fmt.Errorf("%w: %s %s/%s", entity.ErrInferenceRegistryConflict, table, taskKey, version)
	}

	return nil
}

//nolint:funlen,gocognit,gocyclo,cyclop // Transaction verifies every immutable route component.
func (r *InferenceRepo) SyncRoutes(ctx context.Context, routes []entity.InferenceRoute) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("InferenceRepo.SyncRoutes begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Binary/config reconciliation is authoritative for active routes. This
	// prevents a previously priced provider from remaining reachable after
	// its price catalog configuration is removed.
	if _, err = tx.Exec(ctx, `UPDATE inference_routes SET enabled=false, updated_at=now()`); err != nil {
		return fmt.Errorf("InferenceRepo.SyncRoutes disable stale: %w", err)
	}

	for i := range routes {
		route := routes[i]
		if route.PriceVersion == "" {
			return fmt.Errorf("%w: paid route %s/%s has no price version",
				entity.ErrInferenceRouteMissing, route.ProviderKey, route.ModelKey)
		}

		if _, err = tx.Exec(
			ctx, `
INSERT INTO inference_providers (
    provider_key, display_name, base_url, api_key_env
) VALUES ($1, $2, $3, $4)
ON CONFLICT (provider_key) DO UPDATE
SET display_name = EXCLUDED.display_name,
    base_url = EXCLUDED.base_url,
    api_key_env = EXCLUDED.api_key_env,
    enabled = true,
    updated_at = now()`,
			route.ProviderKey, route.ProviderKey, route.BaseURL, route.APIKeyEnv,
		); err != nil {
			return fmt.Errorf("InferenceRepo.SyncRoutes provider: %w", err)
		}

		if _, err = tx.Exec(
			ctx, `
INSERT INTO inference_models (
    provider_key, model_key, provider_model_id, price_version,
    input_nano_usd_per_million, cached_input_nano_usd_per_million,
    output_nano_usd_per_million, supports_json, supports_embeddings,
    timeout_ms, max_output_tokens
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (provider_key, model_key) DO UPDATE
SET provider_model_id = EXCLUDED.provider_model_id,
    price_version = EXCLUDED.price_version,
    input_nano_usd_per_million = EXCLUDED.input_nano_usd_per_million,
    cached_input_nano_usd_per_million = EXCLUDED.cached_input_nano_usd_per_million,
    output_nano_usd_per_million = EXCLUDED.output_nano_usd_per_million,
    supports_json = EXCLUDED.supports_json,
    supports_embeddings = EXCLUDED.supports_embeddings,
    timeout_ms = EXCLUDED.timeout_ms,
    max_output_tokens = EXCLUDED.max_output_tokens,
    enabled = true`,
			route.ProviderKey, route.ModelKey, route.ProviderModelID, route.PriceVersion,
			route.InputNanoPerMillion, route.CachedNanoPerMillion, route.OutputNanoPerMillion,
			route.SupportsJSON, route.SupportsEmbeddings, route.TimeoutMS, route.MaxOutputTokens,
		); err != nil {
			return fmt.Errorf("InferenceRepo.SyncRoutes model: %w", err)
		}

		if _, err = tx.Exec(
			ctx, `
INSERT INTO inference_model_prices (
    provider_key, model_key, price_version,
    input_nano_usd_per_million, cached_input_nano_usd_per_million,
    output_nano_usd_per_million
) VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (provider_key, model_key, price_version) DO NOTHING`,
			route.ProviderKey, route.ModelKey, route.PriceVersion,
			route.InputNanoPerMillion, route.CachedNanoPerMillion,
			route.OutputNanoPerMillion,
		); err != nil {
			return fmt.Errorf("InferenceRepo.SyncRoutes price: %w", err)
		}

		var inputPrice, cachedPrice, outputPrice int64
		if err = tx.QueryRow(
			ctx, `
SELECT input_nano_usd_per_million, cached_input_nano_usd_per_million,
       output_nano_usd_per_million
FROM inference_model_prices
WHERE provider_key=$1 AND model_key=$2 AND price_version=$3`,
			route.ProviderKey,
			route.ModelKey,
			route.PriceVersion,
		).Scan(&inputPrice, &cachedPrice, &outputPrice); err != nil {
			return fmt.Errorf("InferenceRepo.SyncRoutes verify price: %w", err)
		}

		if inputPrice != route.InputNanoPerMillion ||
			cachedPrice != route.CachedNanoPerMillion ||
			outputPrice != route.OutputNanoPerMillion {
			return fmt.Errorf(
				"%w: price %s/%s/%s",
				entity.ErrInferenceRegistryConflict,
				route.ProviderKey,
				route.ModelKey,
				route.PriceVersion,
			)
		}

		if _, err = tx.Exec(
			ctx, `
INSERT INTO inference_routes (
    task_key, priority, provider_key, model_key, prompt_version,
    response_schema_version, temperature
) VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (task_key, priority) DO UPDATE
SET provider_key = EXCLUDED.provider_key,
    model_key = EXCLUDED.model_key,
    prompt_version = EXCLUDED.prompt_version,
    response_schema_version = EXCLUDED.response_schema_version,
    temperature = EXCLUDED.temperature,
    enabled = true,
    updated_at = now()`,
			route.TaskKey, route.Priority, route.ProviderKey, route.ModelKey,
			route.PromptVersion, route.ResponseSchemaVersion, route.Temperature,
		); err != nil {
			return fmt.Errorf("InferenceRepo.SyncRoutes route %s/%d: %w",
				route.TaskKey, route.Priority, err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("InferenceRepo.SyncRoutes commit: %w", err)
	}

	return nil
}

const inferenceRouteSelect = `
t.task_key, t.task_class, t.output_kind, t.cache_ttl_seconds,
t.persistent_enrichment, route.priority, provider.provider_key,
provider.base_url, provider.api_key_env, model.model_key,
model.provider_model_id, model.price_version,
model.input_nano_usd_per_million, model.cached_input_nano_usd_per_million,
model.output_nano_usd_per_million, model.supports_json,
model.supports_embeddings, model.timeout_ms, model.max_output_tokens,
route.prompt_version, prompt.content_sha256, prompt.policy_sha256, prompt.messages_template,
route.response_schema_version, schema.content_sha256, schema.schema_body,
route.temperature`

func (r *InferenceRepo) ResolveRoutes(
	ctx context.Context,
	taskKey, sessionID string,
) ([]entity.InferenceRoute, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT `+inferenceRouteSelect+`
FROM inference_routes route
JOIN inference_tasks t ON t.task_key = route.task_key
JOIN inference_providers provider ON provider.provider_key = route.provider_key
JOIN inference_models model
  ON model.provider_key = route.provider_key AND model.model_key = route.model_key
JOIN inference_prompt_versions prompt
  ON prompt.task_key = route.task_key AND prompt.version = route.prompt_version
JOIN inference_response_schemas schema
  ON schema.task_key = route.task_key AND schema.version = route.response_schema_version
LEFT JOIN inference_sessions session ON session.id = NULLIF($2, '')::uuid
WHERE route.task_key = $1
  AND route.enabled AND t.enabled AND provider.enabled AND model.enabled
  AND model.price_version IS NOT NULL
  AND (
      NULLIF($2, '') IS NULL
      OR (session.id IS NOT NULL AND session.task_key = route.task_key)
  )
  AND (
      session.id IS NULL
      OR session.pinned_provider_key IS NULL
      OR (
          session.pinned_provider_key = route.provider_key
          AND session.pinned_model_key = route.model_key
      )
  )
ORDER BY route.priority`, taskKey, sessionID)
	if err != nil {
		return nil, fmt.Errorf("InferenceRepo.ResolveRoutes query: %w", err)
	}
	defer rows.Close()

	routes := make([]entity.InferenceRoute, 0, maxInferenceRoutes)

	for rows.Next() {
		route, scanErr := scanInferenceRoute(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("InferenceRepo.ResolveRoutes scan: %w", scanErr)
		}

		routes = append(routes, route)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("InferenceRepo.ResolveRoutes rows: %w", err)
	}

	if len(routes) == 0 {
		return nil, entity.ErrInferenceRouteMissing
	}

	return routes, nil
}

type inferenceRouteScanner interface {
	Scan(dest ...any) error
}

func scanInferenceRoute(row inferenceRouteScanner) (entity.InferenceRoute, error) {
	var (
		route        entity.InferenceRoute
		policySHA256 *string
		messages     []byte
		schema       []byte
	)

	err := row.Scan(
		&route.TaskKey, &route.TaskClass, &route.OutputKind,
		&route.CacheTTLSeconds, &route.PersistentEnrichment, &route.Priority,
		&route.ProviderKey, &route.BaseURL, &route.APIKeyEnv, &route.ModelKey,
		&route.ProviderModelID, &route.PriceVersion, &route.InputNanoPerMillion,
		&route.CachedNanoPerMillion, &route.OutputNanoPerMillion,
		&route.SupportsJSON, &route.SupportsEmbeddings, &route.TimeoutMS,
		&route.MaxOutputTokens, &route.PromptVersion, &route.PromptSHA256,
		&policySHA256, &messages, &route.ResponseSchemaVersion, &route.ResponseSchemaSHA256,
		&schema, &route.Temperature,
	)
	route.PolicySHA256 = stringValue(policySHA256)
	route.MessagesTemplate = entity.RawJSON(messages)
	route.ResponseSchema = entity.RawJSON(schema)

	return route, err
}

//nolint:gocritic // Repository interface accepts an immutable ledger value.
func (r *InferenceRepo) CreateCall(ctx context.Context, call entity.InferenceCall) error {
	_, err := r.Pool.Exec(
		ctx, `
INSERT INTO inference_calls (
    id, task_key, task_class, session_id, request_id, trace_id,
    cache_key, cache_status, status
) VALUES ($1,$2,$3,NULLIF($4,'')::uuid,$5,$6,$7,$8,$9)`,
		call.ID, call.TaskKey, call.TaskClass, stringValue(call.SessionID),
		call.RequestID, call.TraceID, call.CacheKey, call.CacheStatus, call.Status,
	)
	if err != nil {
		return fmt.Errorf("InferenceRepo.CreateCall: %w", err)
	}

	return nil
}

//nolint:gocritic // Repository interface accepts an immutable attempt snapshot.
func (r *InferenceRepo) CreateAttempt(ctx context.Context, attempt entity.InferenceAttempt) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("InferenceRepo.CreateAttempt begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	metadata, err := json.Marshal(map[string]any{
		"inference_call_id": attempt.CallID,
		"attempt_no":        attempt.AttemptNo,
		"price_version":     attempt.Route.PriceVersion,
	})
	if err != nil {
		return fmt.Errorf("InferenceRepo.CreateAttempt metadata: %w", err)
	}

	if _, err = tx.Exec(
		ctx, `
INSERT INTO generation_runs (
    id, task_name, model_id, prompt_version, provider, metadata
) VALUES ($1,$2,$3,$4,$5,$6::jsonb)`,
		attempt.Generation.ID, attempt.Route.TaskKey, attempt.Route.ProviderModelID,
		attempt.Route.PromptVersion, attempt.Route.ProviderKey, metadata,
	); err != nil {
		return fmt.Errorf("InferenceRepo.CreateAttempt generation: %w", err)
	}

	if _, err = tx.Exec(
		ctx, `
INSERT INTO inference_attempts (
    id, call_id, attempt_no, generation_run_id, task_key, task_class,
    provider_key, model_key, provider_model_id, prompt_version, prompt_sha256,
    response_schema_version, response_schema_sha256, price_version, status,
    failover
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		attempt.ID, attempt.CallID, attempt.AttemptNo, attempt.Generation.ID,
		attempt.Route.TaskKey, attempt.Route.TaskClass, attempt.Route.ProviderKey,
		attempt.Route.ModelKey, attempt.Route.ProviderModelID,
		attempt.Route.PromptVersion, attempt.Route.PromptSHA256,
		attempt.Route.ResponseSchemaVersion, attempt.Route.ResponseSchemaSHA256,
		attempt.Route.PriceVersion, attempt.Status, attempt.Failover,
	); err != nil {
		return fmt.Errorf("InferenceRepo.CreateAttempt attempt: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("InferenceRepo.CreateAttempt commit: %w", err)
	}

	return nil
}

//nolint:gocritic // Repository interface accepts an immutable attempt snapshot.
func (r *InferenceRepo) FinishAttempt(ctx context.Context, attempt entity.InferenceAttempt) error {
	_, err := r.Pool.Exec(
		ctx, `
UPDATE inference_attempts
SET status=$2, input_tokens=$3, cached_input_tokens=$4, output_tokens=$5,
    cost_nano_usd=$6, usage_source=$7, cost_source=$8, error_code=$9,
    completed_at=now()
WHERE id=$1`,
		attempt.ID, attempt.Status, attempt.Usage.InputTokens,
		attempt.Usage.CachedInputTokens, attempt.Usage.OutputTokens,
		attempt.CostNanoUSD, attempt.Usage.Source, attempt.CostSource,
		attempt.ErrorCode,
	)
	if err != nil {
		return fmt.Errorf("InferenceRepo.FinishAttempt: %w", err)
	}

	return nil
}

//nolint:gocritic // Repository interface accepts an immutable ledger value.
func (r *InferenceRepo) FinishCall(ctx context.Context, call entity.InferenceCall) error {
	_, err := r.Pool.Exec(
		ctx, `
UPDATE inference_calls
SET status=$2, final_generation_run_id=$3, cache_status=$4,
    input_tokens=$5, cached_input_tokens=$6, output_tokens=$7,
    cost_nano_usd=$8, usage_source=$9, cost_source=$10,
    error_code=$11, completed_at=now()
WHERE id=$1`,
		call.ID, call.Status, call.GenerationID, call.CacheStatus,
		call.Usage.InputTokens, call.Usage.CachedInputTokens,
		call.Usage.OutputTokens, call.CostNanoUSD, call.Usage.Source,
		call.CostSource, call.ErrorCode,
	)
	if err != nil {
		return fmt.Errorf("InferenceRepo.FinishCall: %w", err)
	}

	return nil
}

func (r *InferenceRepo) ReserveBudget(
	ctx context.Context,
	callID string,
	maximumNanoUSD int64,
) (entity.InferenceBudgetStatus, error) {
	// FOR UPDATE on the current policy row is the serialization point. Using
	// READ COMMITTED lets waiters observe the preceding reservation after the
	// lock is released, yielding a stable budget rejection instead of a 40001.
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.ReserveBudget begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if activateErr := maybeActivateBudget(ctx, tx, r.now()); activateErr != nil {
		return entity.InferenceBudgetStatus{}, activateErr
	}

	status, err := budgetStatus(ctx, tx, r.now(), true)
	if err != nil {
		return entity.InferenceBudgetStatus{}, err
	}

	if exceeded, window, resetAt := budgetWouldExceed(&status, maximumNanoUSD); exceeded {
		code := "inference_budget_exceeded"
		if _, updateErr := tx.Exec(ctx, `
UPDATE inference_calls
SET status='budget_rejected', error_code=$2, completed_at=now()
WHERE id=$1`, callID, code); updateErr != nil {
			return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.ReserveBudget reject: %w", updateErr)
		}

		if err = tx.Commit(ctx); err != nil {
			return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.ReserveBudget reject commit: %w", err)
		}

		return status, &entity.InferenceBudgetExceededError{
			RetryAfter: resetAt.Sub(r.now()),
			ResetAt:    resetAt,
			Window:     window,
		}
	}

	if _, err = tx.Exec(ctx, `
INSERT INTO inference_budget_reservations (call_id, reserved_nano_usd)
	VALUES ($1, $2)`, callID, maximumNanoUSD); err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.ReserveBudget insert: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.ReserveBudget commit: %w", err)
	}

	return status, nil
}

func (r *InferenceRepo) SettleBudget(ctx context.Context, callID string) error {
	_, err := r.Pool.Exec(ctx, `
UPDATE inference_budget_reservations
SET status='settled', settled_at=now()
WHERE call_id=$1 AND status='active'`, callID)
	if err != nil {
		return fmt.Errorf("InferenceRepo.SettleBudget: %w", err)
	}

	return nil
}

func (r *InferenceRepo) GetCache(
	ctx context.Context,
	cacheKey string,
) (entity.InferenceCacheEntry, bool, error) {
	var (
		entry    entity.InferenceCacheEntry
		provider *string
		metadata []byte
	)

	err := r.Pool.QueryRow(ctx, `
UPDATE inference_cache cache
SET last_hit_at=now(), hit_count=hit_count+1
FROM generation_runs generation
WHERE cache.cache_key=$1 AND cache.expires_at > now()
  AND generation.id=cache.generation_run_id
RETURNING cache.cache_key, cache.task_key, cache.ciphertext, cache.expires_at,
          generation.id, generation.task_name, generation.model_id,
          generation.prompt_version, generation.provider,
          generation.metadata, generation.created_at`, cacheKey).Scan(
		&entry.CacheKey, &entry.TaskKey, &entry.Ciphertext, &entry.ExpiresAt,
		&entry.GenerationRun.ID, &entry.GenerationRun.TaskName,
		&entry.GenerationRun.ModelID, &entry.GenerationRun.PromptVersion,
		&provider, &metadata, &entry.GenerationRun.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.InferenceCacheEntry{}, false, nil
	}

	if err != nil {
		return entity.InferenceCacheEntry{}, false, fmt.Errorf("InferenceRepo.GetCache: %w", err)
	}

	entry.GenerationRun.Provider = provider
	entry.GenerationRun.Metadata = entity.RawJSON(metadata)

	return entry, true, nil
}

//nolint:gocritic // Repository interface accepts immutable encrypted cache material.
func (r *InferenceRepo) PutCache(ctx context.Context, entry entity.InferenceCacheEntry) error {
	_, err := r.Pool.Exec(
		ctx, `
INSERT INTO inference_cache (
    cache_key, task_key, generation_run_id, ciphertext, expires_at
) VALUES ($1,$2,$3,$4,$5)
ON CONFLICT (cache_key) DO UPDATE
SET generation_run_id=EXCLUDED.generation_run_id,
    ciphertext=EXCLUDED.ciphertext,
    expires_at=EXCLUDED.expires_at,
    created_at=now(), last_hit_at=NULL, hit_count=0`,
		entry.CacheKey, entry.TaskKey, entry.GenerationRun.ID,
		entry.Ciphertext, entry.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("InferenceRepo.PutCache: %w", err)
	}

	return nil
}

func (r *InferenceRepo) CreateSession(
	ctx context.Context,
	taskKey string,
) (entity.InferenceSession, error) {
	session := entity.InferenceSession{
		ID:        uuid.NewString(),
		TaskKey:   taskKey,
		Status:    "open",
		CreatedAt: r.now().UTC(),
		UpdatedAt: r.now().UTC(),
	}

	err := r.Pool.QueryRow(ctx, `
INSERT INTO inference_sessions (id, task_key, created_at, updated_at)
VALUES ($1,$2,$3,$3)
RETURNING created_at, updated_at`, session.ID, session.TaskKey, session.CreatedAt).Scan(
		&session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return entity.InferenceSession{}, fmt.Errorf("InferenceRepo.CreateSession: %w", err)
	}

	return session, nil
}

func (r *InferenceRepo) PinSession(
	ctx context.Context,
	sessionID, providerKey, modelKey string,
) error {
	tag, err := r.Pool.Exec(ctx, `
UPDATE inference_sessions
SET pinned_provider_key=$2, pinned_model_key=$3, status='pinned', updated_at=now()
WHERE id=$1
  AND (
      pinned_provider_key IS NULL
      OR (pinned_provider_key=$2 AND pinned_model_key=$3)
  )`, sessionID, providerKey, modelKey)
	if err != nil {
		return fmt.Errorf("InferenceRepo.PinSession: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrInferenceSessionPinned
	}

	return nil
}

func (r *InferenceRepo) FailSession(ctx context.Context, sessionID string) error {
	tag, err := r.Pool.Exec(ctx, `
UPDATE inference_sessions
SET status='failed', updated_at=now()
WHERE id=$1 AND status IN ('open','pinned')`, sessionID)
	if err != nil {
		return fmt.Errorf("InferenceRepo.FailSession: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrInferenceSessionPinned
	}

	return nil
}

func (r *InferenceRepo) Budget(
	ctx context.Context,
	now time.Time,
) (entity.InferenceBudgetStatus, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.Budget begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if activateErr := maybeActivateBudget(ctx, tx, now); activateErr != nil {
		return entity.InferenceBudgetStatus{}, activateErr
	}

	status, err := budgetStatus(ctx, tx, now, false)
	if err != nil {
		return entity.InferenceBudgetStatus{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.Budget commit: %w", err)
	}

	return status, nil
}

//nolint:gocyclo,cyclop // Append-only validation keeps every unsafe budget state explicit.
func (r *InferenceRepo) UpdateBudget(
	ctx context.Context,
	actorID string,
	expectedRevision int64,
	patch entity.InferenceBudgetPatch,
) (entity.InferenceBudgetStatus, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.UpdateBudget begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		currentRevision int64
		timezone        string
		started         time.Time
		days            int
		alert           float64
	)

	err = tx.QueryRow(ctx, `
SELECT revision, timezone, baseline_started_at, baseline_days, alert_percent
FROM inference_budget_policies
ORDER BY revision DESC LIMIT 1
FOR UPDATE`).Scan(&currentRevision, &timezone, &started, &days, &alert)
	if err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.UpdateBudget current: %w", err)
	}

	if currentRevision != expectedRevision {
		return entity.InferenceBudgetStatus{}, entity.ErrPreconditionFailed
	}

	if patch.Mode == "enforce" &&
		(patch.DailyCapNanoUSD == nil || patch.MonthlyCapNanoUSD == nil ||
			*patch.DailyCapNanoUSD <= 0 || *patch.MonthlyCapNanoUSD <= 0) {
		return entity.InferenceBudgetStatus{}, entity.ErrInvalidStatus
	}

	_, err = tx.Exec(
		ctx, `
INSERT INTO inference_budget_policies (
    mode, timezone, baseline_started_at, baseline_days,
    monthly_cap_nano_usd, daily_cap_nano_usd, alert_percent,
    reason, actor_user_id
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		patch.Mode, timezone, started, days, patch.MonthlyCapNanoUSD,
		patch.DailyCapNanoUSD, alert, patch.Reason, nullableUUID(actorID),
	)
	if err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.UpdateBudget insert: %w", err)
	}

	status, err := budgetStatus(ctx, tx, r.now(), false)
	if err != nil {
		return entity.InferenceBudgetStatus{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.InferenceBudgetStatus{}, fmt.Errorf("InferenceRepo.UpdateBudget commit: %w", err)
	}

	return status, nil
}

func (r *InferenceRepo) Usage(
	ctx context.Context,
	from, to time.Time,
	groupBy string,
) ([]entity.InferenceUsageRow, error) {
	taskExpr, classExpr, providerExpr, modelExpr := "''", "''", "''", "''"

	switch groupBy {
	case "task":
		taskExpr, classExpr = "call.task_key", "call.task_class"
	case "provider":
		providerExpr = "COALESCE(attempt.provider_key, '')"
	case "model":
		providerExpr = "COALESCE(attempt.provider_key, '')"
		modelExpr = "COALESCE(attempt.provider_model_id, '')"
	case "day", "":
	default:
		return nil, entity.ErrInvalidStatus
	}

	query := fmt.Sprintf(`
SELECT to_char((call.created_at AT TIME ZONE 'Asia/Jakarta')::date, 'YYYY-MM-DD'),
       %s AS task_key, %s AS task_class, %s AS provider, %s AS model,
       COALESCE(sum(call.input_tokens),0), COALESCE(sum(call.output_tokens),0),
       COALESCE(sum(call.cached_input_tokens),0), COALESCE(sum(call.cost_nano_usd),0),
       count(*)
FROM inference_calls call
LEFT JOIN inference_attempts attempt
  ON attempt.generation_run_id=call.final_generation_run_id
WHERE call.created_at >= $1 AND call.created_at < $2
  AND call.status IN ('succeeded','failed','cache_hit')
	GROUP BY 1,2,3,4,5
	ORDER BY 1 DESC, 2, 4, 5`, taskExpr, classExpr, providerExpr, modelExpr)

	rows, err := r.Pool.Query(ctx, query, from, to)
	if err != nil {
		return nil, fmt.Errorf("InferenceRepo.Usage query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.InferenceUsageRow, 0)

	for rows.Next() {
		var item entity.InferenceUsageRow
		if err = rows.Scan(
			&item.Day, &item.TaskKey, &item.TaskClass, &item.Provider, &item.Model,
			&item.InputTokens, &item.OutputTokens, &item.CachedTokens,
			&item.CostNanoUSD, &item.Calls,
		); err != nil {
			return nil, fmt.Errorf("InferenceRepo.Usage scan: %w", err)
		}

		items = append(items, item)
	}

	return items, rows.Err()
}

func (r *InferenceRepo) Registry(ctx context.Context) ([]entity.InferenceRoute, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT `+inferenceRouteSelect+`
FROM inference_routes route
JOIN inference_tasks t ON t.task_key = route.task_key
JOIN inference_providers provider ON provider.provider_key = route.provider_key
JOIN inference_models model
  ON model.provider_key = route.provider_key AND model.model_key = route.model_key
JOIN inference_prompt_versions prompt
  ON prompt.task_key = route.task_key AND prompt.version = route.prompt_version
JOIN inference_response_schemas schema
  ON schema.task_key = route.task_key AND schema.version = route.response_schema_version
ORDER BY t.task_key, route.priority`)
	if err != nil {
		return nil, fmt.Errorf("InferenceRepo.Registry query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.InferenceRoute, 0)

	for rows.Next() {
		item, scanErr := scanInferenceRoute(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("InferenceRepo.Registry scan: %w", scanErr)
		}

		items = append(items, item)
	}

	return items, rows.Err()
}

func maybeActivateBudget(ctx context.Context, tx pgx.Tx, now time.Time) error {
	var (
		revision int64
		mode     string
		timezone string
		started  time.Time
		days     int
		alert    float64
	)

	err := tx.QueryRow(ctx, `
SELECT revision, mode, timezone, baseline_started_at, baseline_days, alert_percent
FROM inference_budget_policies
ORDER BY revision DESC LIMIT 1
FOR UPDATE`).Scan(&revision, &mode, &timezone, &started, &days, &alert)
	if err != nil {
		return fmt.Errorf("InferenceRepo budget policy: %w", err)
	}

	if mode != "baseline" || now.Before(started.AddDate(0, 0, days)) {
		return nil
	}

	var (
		total    int64
		maxDaily int64
	)

	err = tx.QueryRow(ctx, `
WITH days AS (
    SELECT (created_at AT TIME ZONE $3)::date AS day,
           sum(cost_nano_usd)::bigint AS cost
    FROM inference_calls
    WHERE created_at >= $1 AND created_at < $2
      AND status IN ('succeeded','failed')
    GROUP BY 1
)
SELECT COALESCE(sum(cost),0)::bigint, COALESCE(max(cost),0)::bigint
FROM days`, started, started.AddDate(0, 0, days), timezone).Scan(&total, &maxDaily)
	if err != nil {
		return fmt.Errorf("InferenceRepo baseline totals: %w", err)
	}

	if total == 0 || maxDaily == 0 {
		return extendZeroBaseline(ctx, tx, now, timezone, days, alert, revision)
	}

	_, err = tx.Exec(
		ctx, `
INSERT INTO inference_budget_policies (
    mode, timezone, baseline_started_at, baseline_days,
    monthly_cap_nano_usd, daily_cap_nano_usd, alert_percent, reason
) VALUES ('enforce',$1,$2,$3,$4,$5,$6,$7)`,
		timezone, started, days, total*baselineCapMultiplier,
		maxDaily*baselineCapMultiplier, alert,
		fmt.Sprintf("automatic 2x activation from baseline revision %d", revision),
	)
	if err != nil {
		return fmt.Errorf("InferenceRepo activate baseline: %w", err)
	}

	return nil
}

func extendZeroBaseline(
	ctx context.Context,
	tx pgx.Tx,
	now time.Time,
	timezone string,
	days int,
	alert float64,
	revision int64,
) error {
	_, err := tx.Exec(
		ctx, `
INSERT INTO inference_budget_policies (
    mode, timezone, baseline_started_at, baseline_days, alert_percent, reason
) VALUES ('baseline',$1,$2,$3,$4,$5)`,
		timezone,
		now,
		days,
		alert,
		fmt.Sprintf("%s from revision %d", zeroBaselineExtensionReason, revision),
	)
	if err != nil {
		return fmt.Errorf("InferenceRepo extend zero baseline: %w", err)
	}

	return nil
}

//nolint:funlen // Status calculation intentionally returns one atomic daily/monthly snapshot.
func budgetStatus(
	ctx context.Context,
	tx pgx.Tx,
	now time.Time,
	lock bool,
) (entity.InferenceBudgetStatus, error) {
	lockSQL := ""
	if lock {
		lockSQL = " FOR UPDATE"
	}

	var (
		status entity.InferenceBudgetStatus
		reason string
	)

	err := tx.QueryRow(ctx, `
SELECT revision, mode, timezone, baseline_started_at, baseline_days,
       daily_cap_nano_usd, monthly_cap_nano_usd, alert_percent, reason, created_at
FROM inference_budget_policies
ORDER BY revision DESC LIMIT 1`+lockSQL).Scan(
		&status.Revision, &status.Mode, &status.Timezone,
		&status.BaselineStartedAt, &status.BaselineDays,
		&status.DailyCapNanoUSD, &status.MonthlyCapNanoUSD,
		&status.AlertPercent, &reason, &status.UpdatedAt,
	)
	if err != nil {
		return status, fmt.Errorf("InferenceRepo budget status policy: %w", err)
	}

	status.BaselineEndsAt = status.BaselineStartedAt.AddDate(0, 0, status.BaselineDays)

	location, err := time.LoadLocation(status.Timezone)
	if err != nil {
		return status, fmt.Errorf("InferenceRepo budget timezone: %w", err)
	}

	localNow := now.In(location)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	monthStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, location)
	nextDay := dayStart.AddDate(0, 0, 1)
	nextMonth := monthStart.AddDate(0, 1, 0)
	status.DailyResetAt = &nextDay
	status.MonthlyResetAt = &nextMonth
	nextReset := nextDay
	status.NextResetAt = &nextReset

	err = tx.QueryRow(
		ctx, `
SELECT
    COALESCE(sum(cost_nano_usd) FILTER (WHERE created_at >= $1),0)::bigint,
    COALESCE(sum(cost_nano_usd) FILTER (WHERE created_at >= $2),0)::bigint,
    COALESCE(sum(cost_nano_usd) FILTER (
        WHERE created_at >= $3 AND created_at < $4
    ),0)::bigint
FROM inference_calls
WHERE status IN ('succeeded','failed')`,
		dayStart.UTC(), monthStart.UTC(), status.BaselineStartedAt,
		status.BaselineEndsAt,
	).Scan(
		&status.DailyUsedNanoUSD, &status.MonthlyUsedNanoUSD,
		&status.BaselineCostNanoUSD,
	)
	if err != nil {
		return status, fmt.Errorf("InferenceRepo budget status usage: %w", err)
	}

	err = tx.QueryRow(ctx, `
SELECT COALESCE(sum(reserved_nano_usd),0)::bigint
FROM inference_budget_reservations
WHERE status='active'`).Scan(&status.ReservedNanoUSD)
	if err != nil {
		return status, fmt.Errorf("InferenceRepo budget reservations: %w", err)
	}

	if status.Mode == "baseline" && status.BaselineCostNanoUSD == 0 &&
		(!now.Before(status.BaselineEndsAt) ||
			strings.HasPrefix(reason, zeroBaselineExtensionReason)) {
		status.ConfigAlert = zeroBaselineConfigurationNotice
	}

	return status, nil
}

func budgetWouldExceed(
	status *entity.InferenceBudgetStatus,
	maximumNanoUSD int64,
) (exceeded bool, window string, resetAt time.Time) {
	if status.Mode != "enforce" {
		return false, "", time.Time{}
	}

	reserved := status.ReservedNanoUSD + maximumNanoUSD
	if status.DailyCapNanoUSD != nil &&
		status.DailyUsedNanoUSD+reserved > *status.DailyCapNanoUSD {
		return true, "daily", *status.DailyResetAt
	}

	if status.MonthlyCapNanoUSD != nil &&
		status.MonthlyUsedNanoUSD+reserved > *status.MonthlyCapNanoUSD {
		return true, "monthly", *status.MonthlyResetAt
	}

	return false, "", time.Time{}
}

func canonicalJSONObject(raw entity.RawJSON) ([]byte, error) {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, entity.ErrInferenceSchemaInvalid
	}

	return json.Marshal(object)
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)

	return hex.EncodeToString(sum[:])
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}

	return strings.TrimSpace(*value)
}

func nullableUUID(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	return value
}
