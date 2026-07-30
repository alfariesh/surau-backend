package persistent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveInferenceAttributionGuard proves the database itself rejects an
// attempt whose task/model/prompt/schema/price tuple disagrees with B-6 or the
// immutable U-0 registry.
//
//nolint:paralleltest // serial transaction intentionally shadows current policy fixtures
func TestLiveInferenceAttributionGuard(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	tx, err := pg.Pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	var violations int

	fixture := seedLiveInferenceAttribution(t, tx)

	require.NoError(t, tx.QueryRow(t.Context(), `
SELECT count(*)
FROM inference_attempts attempt
JOIN inference_calls call ON call.id=attempt.call_id
JOIN generation_runs generation ON generation.id=attempt.generation_run_id
JOIN inference_prompt_versions prompt
  ON prompt.task_key=attempt.task_key AND prompt.version=attempt.prompt_version
JOIN inference_response_schemas schema
  ON schema.task_key=attempt.task_key AND schema.version=attempt.response_schema_version
JOIN inference_model_prices price
  ON price.provider_key=attempt.provider_key
 AND price.model_key=attempt.model_key
 AND price.price_version=attempt.price_version
WHERE attempt.call_id=$1
  AND (
      attempt.task_key<>call.task_key
      OR attempt.task_key<>generation.task_name
      OR attempt.provider_key IS DISTINCT FROM generation.provider
      OR attempt.provider_model_id<>generation.model_id
      OR attempt.prompt_version<>generation.prompt_version
      OR attempt.prompt_sha256<>prompt.content_sha256
      OR attempt.response_schema_sha256<>schema.content_sha256
  )`, fixture.callID).Scan(&violations))
	assert.Zero(t, violations)

	assertRejectedAttributionMutation(t, tx, fixture.attemptID, "prompt_sha256", strings.Repeat("0", 64))
	assertRejectedAttributionMutation(t, tx, fixture.attemptID, "response_schema_sha256", strings.Repeat("1", 64))
	assertRejectedAttributionMutation(t, tx, fixture.attemptID, "provider_model_id", "wrong-model")
	assertRejectedAttributionMutation(t, tx, fixture.attemptID, "price_version", "wrong-price")
}

// TestLiveInferenceBaselineBoundary proves day 30 still measures, day 31
// activates 2x caps, and an empty baseline never creates a zero cap.
//
//nolint:paralleltest // serial policy revisions are rolled back together
func TestLiveInferenceBaselineBoundary(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	tx, err := pg.Pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	taskKey := "u0-live-baseline-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	_, err = tx.Exec(t.Context(), `
INSERT INTO inference_tasks (task_key, task_class, output_kind)
	VALUES ($1,'answer','structured')`, taskKey)
	require.NoError(t, err)

	started := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	_, err = tx.Exec(t.Context(), `
INSERT INTO inference_budget_policies (
    mode, timezone, baseline_started_at, baseline_days, alert_percent, reason
) VALUES ('baseline','Asia/Jakarta',$1,30,80,'live day-30 boundary')`, started)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
INSERT INTO inference_calls (
    id, task_key, task_class, status, cache_status, input_tokens,
    output_tokens, cost_nano_usd, usage_source, cost_source, created_at, completed_at
) VALUES (
    $1,$2,'answer','succeeded','miss',10,5,50,'provider','registry',$3,$3
)`, uuid.NewString(), taskKey, started.Add(12*time.Hour))
	require.NoError(t, err)

	var before, atDay30 int

	require.NoError(t, tx.QueryRow(t.Context(),
		`SELECT count(*) FROM inference_budget_policies`).Scan(&before))
	require.NoError(t, maybeActivateBudget(t.Context(), tx, started.Add(30*24*time.Hour-time.Nanosecond)))
	require.NoError(t, tx.QueryRow(t.Context(),
		`SELECT count(*) FROM inference_budget_policies`).Scan(&atDay30))
	assert.Equal(t, before, atDay30)

	require.NoError(t, maybeActivateBudget(t.Context(), tx, started.Add(30*24*time.Hour)))
	status, err := budgetStatus(t.Context(), tx, started.Add(30*24*time.Hour), false)
	require.NoError(t, err)
	require.NotNil(t, status.DailyCapNanoUSD)
	require.NotNil(t, status.MonthlyCapNanoUSD)
	assert.Equal(t, "enforce", status.Mode)
	assert.EqualValues(t, 100, *status.DailyCapNanoUSD)
	assert.EqualValues(t, 100, *status.MonthlyCapNanoUSD)

	zeroStarted := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	_, err = tx.Exec(t.Context(), `
INSERT INTO inference_budget_policies (
    mode, timezone, baseline_started_at, baseline_days, alert_percent, reason
) VALUES ('baseline','Asia/Jakarta',$1,30,80,'live zero baseline')`, zeroStarted)
	require.NoError(t, err)
	require.NoError(t, maybeActivateBudget(t.Context(), tx, zeroStarted.Add(31*24*time.Hour)))
	zero, err := budgetStatus(t.Context(), tx, zeroStarted.Add(31*24*time.Hour), false)
	require.NoError(t, err)
	assert.Equal(t, "baseline", zero.Mode)
	assert.Nil(t, zero.DailyCapNanoUSD)
	assert.Nil(t, zero.MonthlyCapNanoUSD)
	assert.Contains(t, zero.ConfigAlert, "baseline is zero")
	assert.True(t, zeroStarted.Add(31*24*time.Hour).Equal(zero.BaselineStartedAt))
	assert.True(t, zero.BaselineStartedAt.Add(30*24*time.Hour).Equal(zero.BaselineEndsAt))
}

// TestLiveInferenceBudgetReservationSerializesParallelCalls proves row locking
// admits at most one of two 60-unit reservations behind a 100-unit remainder.
//
//nolint:paralleltest // mutates the latest append-only budget policy briefly
func TestLiveInferenceBudgetReservationSerializesParallelCalls(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL, postgres.MaxPoolSize(4))
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	ctx := t.Context()

	taskKey := "u0-live-budget-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	callIDs := []string{uuid.NewString(), uuid.NewString()}

	var existingDaily, existingMonthly, existingReserved int64

	location, err := time.LoadLocation("Asia/Jakarta")
	require.NoError(t, err)

	now := time.Now()
	localNow := now.In(location)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	monthStart := time.Date(localNow.Year(), localNow.Month(), 1, 0, 0, 0, 0, location)
	require.NoError(t, pg.Pool.QueryRow(
		ctx, `
SELECT
    COALESCE(sum(cost_nano_usd) FILTER (WHERE created_at >= $1),0)::bigint,
    COALESCE(sum(cost_nano_usd) FILTER (WHERE created_at >= $2),0)::bigint
FROM inference_calls WHERE status IN ('succeeded','failed')`,
		dayStart.UTC(),
		monthStart.UTC(),
	).Scan(&existingDaily, &existingMonthly))
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT COALESCE(sum(reserved_nano_usd),0)::bigint
FROM inference_budget_reservations WHERE status='active'`).Scan(&existingReserved))

	var revision int64
	require.NoError(t, pg.Pool.QueryRow(
		ctx, `
INSERT INTO inference_budget_policies (
    mode, timezone, baseline_started_at, baseline_days,
    daily_cap_nano_usd, monthly_cap_nano_usd, alert_percent, reason
) VALUES ('enforce','Asia/Jakarta',now(),30,$1,$2,80,$3)
RETURNING revision`,
		existingDaily+existingReserved+100,
		existingMonthly+existingReserved+100,
		"live parallel reservation "+taskKey,
	).Scan(&revision))
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO inference_tasks (task_key, task_class, output_kind)
	VALUES ($1,'answer','structured')`, taskKey)
	require.NoError(t, err)

	for _, callID := range callIDs {
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO inference_calls (id, task_key, task_class, status, cache_status)
VALUES ($1,$2,'answer','started','miss')`, callID, taskKey)
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cleanup := func(query string, args ...any) {
			_, cleanupErr := pg.Pool.Exec(cleanupCtx, query, args...)
			assert.NoError(t, cleanupErr)
		}
		cleanup(
			`DELETE FROM inference_budget_reservations WHERE call_id=ANY($1::uuid[])`,
			callIDs,
		)
		cleanup(`DELETE FROM inference_calls WHERE id=ANY($1::uuid[])`, callIDs)
		cleanup(`DELETE FROM inference_tasks WHERE task_key=$1`, taskKey)
		cleanup(`DELETE FROM inference_budget_policies WHERE revision=$1`, revision)
	})

	var wait sync.WaitGroup

	repository := NewInferenceRepo(pg)
	start := make(chan struct{})
	results := make(chan error, len(callIDs))

	for _, callID := range callIDs {
		wait.Add(1)

		go func(id string) {
			defer wait.Done()

			<-start

			_, reserveErr := repository.ReserveBudget(context.Background(), id, 60)
			results <- reserveErr
		}(callID)
	}

	close(start)
	wait.Wait()
	close(results)

	var admitted, rejected int

	for reserveErr := range results {
		if reserveErr == nil {
			admitted++

			continue
		}

		var exceeded *entity.InferenceBudgetExceededError
		require.True(t, errors.As(reserveErr, &exceeded), reserveErr)
		assert.Positive(t, exceeded.RetryAfter)

		rejected++
	}

	assert.Equal(t, 1, admitted)
	assert.Equal(t, 1, rejected)

	var fixtureReserved int64
	require.NoError(t, pg.Pool.QueryRow(
		ctx, `
SELECT COALESCE(sum(reserved_nano_usd),0)::bigint
FROM inference_budget_reservations WHERE call_id=ANY($1::uuid[]) AND status='active'`,
		callIDs,
	).Scan(&fixtureReserved))
	assert.EqualValues(t, 60, fixtureReserved)
}

// TestLiveInferenceRepositoryLifecycle exercises the complete persistent
// boundary against PostgreSQL: immutable registry, routes, ledger, cache,
// pinned session, budget, usage, and operator registry views.
//
//nolint:paralleltest // One serial lifecycle owns and cleans a connected SQL fixture.
func TestLiveInferenceRepositoryLifecycle(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := t.Context()
	repository := NewInferenceRepo(pg)
	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	taskKey := "u0-live-lifecycle-" + suffix
	providerKeys := []string{
		"u0-live-primary-" + suffix,
		"u0-live-secondary-" + suffix,
		"u0-live-ephemeral-" + suffix,
	}
	modelKeys := []string{
		"model-primary-" + suffix,
		"model-secondary-" + suffix,
		"model-ephemeral-" + suffix,
	}

	var policyRevision int64

	type routeState struct {
		taskKey  string
		priority int
		enabled  bool
	}

	var originalRoutes []routeState

	rows, err := pg.Pool.Query(ctx, `SELECT task_key, priority, enabled FROM inference_routes`)
	require.NoError(t, err)

	for rows.Next() {
		var state routeState
		require.NoError(t, rows.Scan(&state.taskKey, &state.priority, &state.enabled))
		originalRoutes = append(originalRoutes, state)
	}

	require.NoError(t, rows.Err())
	rows.Close()

	restoreRouteStates := func(cleanupCtx context.Context) {
		for _, state := range originalRoutes {
			_, restoreErr := pg.Pool.Exec(
				cleanupCtx,
				`UPDATE inference_routes SET enabled=$3 WHERE task_key=$1 AND priority=$2`,
				state.taskKey,
				state.priority,
				state.enabled,
			)
			assert.NoError(t, restoreErr)
		}
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		connection, acquireErr := pg.Pool.Acquire(cleanupCtx)
		if !assert.NoError(t, acquireErr) {
			return
		}
		defer connection.Release()

		_, replicationErr := connection.Exec(
			cleanupCtx,
			`SET session_replication_role = replica`,
		)
		if !assert.NoError(t, replicationErr) {
			return
		}
		defer func() {
			_, resetErr := connection.Exec(
				context.Background(),
				`SET session_replication_role = origin`,
			)
			assert.NoError(t, resetErr)
		}()

		cleanup := func(query string, args ...any) {
			_, cleanupErr := connection.Exec(cleanupCtx, query, args...)
			assert.NoError(t, cleanupErr)
		}

		cleanup(`DELETE FROM inference_cache WHERE task_key=$1`, taskKey)
		cleanup(`DELETE FROM inference_calls WHERE task_key=$1`, taskKey)
		cleanup(`DELETE FROM generation_runs WHERE task_name=$1`, taskKey)
		cleanup(`DELETE FROM inference_sessions WHERE task_key=$1`, taskKey)
		cleanup(`DELETE FROM inference_routes WHERE task_key=$1`, taskKey)
		cleanup(`DELETE FROM inference_response_schemas WHERE task_key=$1`, taskKey)
		cleanup(`DELETE FROM inference_prompt_versions WHERE task_key=$1`, taskKey)
		cleanup(`DELETE FROM inference_model_prices WHERE provider_key=ANY($1)`, providerKeys)
		cleanup(`DELETE FROM inference_models WHERE provider_key=ANY($1)`, providerKeys)
		cleanup(`DELETE FROM inference_providers WHERE provider_key=ANY($1)`, providerKeys)
		cleanup(`DELETE FROM inference_tasks WHERE task_key=$1`, taskKey)

		if policyRevision != 0 {
			cleanup(`DELETE FROM inference_budget_policies WHERE revision=$1`, policyRevision)
		}

		for _, state := range originalRoutes {
			_, restoreErr := connection.Exec(
				cleanupCtx,
				`UPDATE inference_routes SET enabled=$3 WHERE task_key=$1 AND priority=$2`,
				state.taskKey,
				state.priority,
				state.enabled,
			)
			assert.NoError(t, restoreErr)
		}
	})

	manifest := entity.InferencePromptManifest{
		TaskKey: taskKey, TaskClass: entity.InferenceTaskClassAnswer,
		OutputKind: "structured", CacheTTLSeconds: 600,
		PromptVersion: "prompt-v1",
		Messages: []entity.InferenceMessage{
			{Role: "system", Content: "Return JSON."},
			{Role: "user", Content: "{{.user}}"},
		},
		ResponseSchemaVersion: "schema-v1",
		ResponseSchema: entity.RawJSON(
			`{"required":["answer"],"type":"object","properties":{"answer":{"type":"string"}}}`,
		),
	}
	require.NoError(t, repository.SyncManifest(ctx, []entity.InferencePromptManifest{manifest}))
	require.NoError(t, repository.SyncManifest(ctx, []entity.InferencePromptManifest{manifest}))

	conflicting := manifest
	conflicting.Messages = []entity.InferenceMessage{{Role: "user", Content: "changed"}}
	require.ErrorIs(
		t,
		repository.SyncManifest(ctx, []entity.InferencePromptManifest{conflicting}),
		entity.ErrInferenceRegistryConflict,
	)

	routes := make([]entity.InferenceRoute, 2)
	for index := range routes {
		//nolint:gosec // APIKeyEnv is an environment-variable name, never a credential.
		routes[index] = entity.InferenceRoute{
			TaskKey: taskKey, Priority: index + 1,
			ProviderKey: providerKeys[index], BaseURL: "https://provider.invalid/v1",
			APIKeyEnv:       "U0_LIVE_TEST_KEY",
			ModelKey:        modelKeys[index],
			ProviderModelID: modelKeys[index], PriceVersion: "price-v1",
			InputNanoPerMillion: 100, CachedNanoPerMillion: 10,
			OutputNanoPerMillion: 200, SupportsJSON: true,
			TimeoutMS: 500, MaxOutputTokens: 50,
			PromptVersion: "prompt-v1", ResponseSchemaVersion: "schema-v1",
		}
	}

	missingPrice := routes[0]
	missingPrice.PriceVersion = ""
	require.ErrorIs(
		t,
		repository.SyncRoutes(ctx, []entity.InferenceRoute{missingPrice}),
		entity.ErrInferenceRouteMissing,
	)
	require.NoError(t, repository.SyncRoutes(ctx, routes))
	restoreRouteStates(ctx)

	resolved, err := repository.ResolveRoutes(ctx, taskKey, "")
	require.NoError(t, err)
	require.Len(t, resolved, 2)
	assert.Equal(t, providerKeys[0], resolved[0].ProviderKey)
	assert.NotEmpty(t, resolved[0].PromptSHA256)
	assert.NotEmpty(t, resolved[0].ResponseSchemaSHA256)

	ephemeral := routes[0]
	ephemeral.ProviderKey = providerKeys[2]
	ephemeral.ModelKey = modelKeys[2]
	ephemeral.ProviderModelID = modelKeys[2]
	require.NoError(t, repository.SyncEphemeralModels(
		ctx,
		[]entity.InferenceRoute{ephemeral},
	))

	resolvedAfterEphemeral, err := repository.ResolveRoutes(ctx, taskKey, "")
	require.NoError(t, err)
	require.Len(t, resolvedAfterEphemeral, 2)
	assert.Equal(t, providerKeys[0], resolvedAfterEphemeral[0].ProviderKey)
	assert.Equal(t, providerKeys[1], resolvedAfterEphemeral[1].ProviderKey)

	var ephemeralModelCount int
	require.NoError(t, pg.Pool.QueryRow(
		ctx,
		`SELECT count(*) FROM inference_models WHERE provider_key=$1 AND model_key=$2`,
		providerKeys[2],
		modelKeys[2],
	).Scan(&ephemeralModelCount))
	assert.Equal(t, 1, ephemeralModelCount)

	_, err = repository.ResolveRoutes(ctx, taskKey+"-missing", "")
	require.ErrorIs(t, err, entity.ErrInferenceRouteMissing)

	session, err := repository.CreateSession(ctx, taskKey)
	require.NoError(t, err)
	require.NoError(t, repository.PinSession(ctx, session.ID, providerKeys[0], modelKeys[0]))
	require.ErrorIs(
		t,
		repository.PinSession(ctx, session.ID, providerKeys[1], modelKeys[1]),
		entity.ErrInferenceSessionPinned,
	)

	pinned, err := repository.ResolveRoutes(ctx, taskKey, session.ID)
	require.NoError(t, err)
	require.Len(t, pinned, 1)
	assert.Equal(t, providerKeys[0], pinned[0].ProviderKey)

	callID := uuid.NewString()
	requestID := "request-u0-live-" + suffix
	traceID := strings.Repeat("1", 32)
	cacheKey := strings.Repeat("a", 64)
	sessionID := session.ID
	call := entity.InferenceCall{
		ID: callID, TaskKey: taskKey, TaskClass: entity.InferenceTaskClassAnswer,
		SessionID: &sessionID, RequestID: &requestID, TraceID: &traceID,
		CacheKey: &cacheKey, CacheStatus: "miss", Status: "started",
	}
	require.NoError(t, repository.CreateCall(ctx, call))

	status, err := repository.ReserveBudget(ctx, callID, 1_000)
	require.NoError(t, err)
	assert.Contains(t, []string{"baseline", "disabled", "enforce"}, status.Mode)
	require.NoError(t, repository.SettleBudget(ctx, callID))

	provider := resolved[0].ProviderKey
	generation := entity.GenerationRun{
		ID: uuid.NewString(), TaskName: taskKey,
		ModelID: resolved[0].ProviderModelID, PromptVersion: resolved[0].PromptVersion,
		Provider: &provider,
	}
	attempt := entity.InferenceAttempt{
		ID: uuid.NewString(), CallID: callID, AttemptNo: 1,
		Generation: generation, Route: resolved[0], Status: "started",
	}
	require.NoError(t, repository.CreateAttempt(ctx, attempt))

	attempt.Status = "succeeded"
	attempt.Usage = entity.InferenceUsage{
		InputTokens: 12, CachedInputTokens: 2, OutputTokens: 4,
		Source: entity.InferenceUsageProvider,
	}
	attempt.CostNanoUSD = 321
	attempt.CostSource = entity.InferenceCostRegistry
	require.NoError(t, repository.FinishAttempt(ctx, attempt))

	call.Status = "succeeded"
	call.GenerationID = &generation.ID
	call.Usage = attempt.Usage
	call.CostNanoUSD = attempt.CostNanoUSD
	call.CostSource = attempt.CostSource
	require.NoError(t, repository.FinishCall(ctx, call))

	_, found, err := repository.GetCache(ctx, strings.Repeat("f", 64))
	require.NoError(t, err)
	assert.False(t, found)

	cacheEntry := entity.InferenceCacheEntry{
		CacheKey: cacheKey, TaskKey: taskKey, GenerationRun: generation,
		Ciphertext: "encrypted-u0-live-fixture",
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	require.NoError(t, repository.PutCache(ctx, cacheEntry))
	cached, found, err := repository.GetCache(ctx, cacheKey)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, generation.ID, cached.GenerationRun.ID)
	assert.Equal(t, cacheEntry.Ciphertext, cached.Ciphertext)

	usage, err := repository.Usage(
		ctx,
		time.Now().Add(-time.Hour),
		time.Now().Add(time.Hour),
		"model",
	)
	require.NoError(t, err)
	require.NotEmpty(t, usage)
	assert.True(t, usageContainsTaskCost(usage, providerKeys[0], modelKeys[0], 321))

	_, err = repository.Usage(ctx, time.Now().Add(-time.Hour), time.Now(), "unsafe")
	require.ErrorIs(t, err, entity.ErrInvalidStatus)

	registry, err := repository.Registry(ctx)
	require.NoError(t, err)
	assert.True(t, registryContainsTask(registry, taskKey))

	budget, err := repository.Budget(ctx, time.Now())
	require.NoError(t, err)
	_, err = repository.UpdateBudget(ctx, "", budget.Revision+1, entity.InferenceBudgetPatch{
		Mode: "disabled", Reason: "wrong revision",
	})
	require.ErrorIs(t, err, entity.ErrPreconditionFailed)
	_, err = repository.UpdateBudget(ctx, "", budget.Revision, entity.InferenceBudgetPatch{
		Mode: "enforce", Reason: "missing caps",
	})
	require.ErrorIs(t, err, entity.ErrInvalidStatus)

	updated, err := repository.UpdateBudget(ctx, "", budget.Revision, entity.InferenceBudgetPatch{
		Mode: "disabled", Reason: "live repository lifecycle",
	})
	require.NoError(t, err)

	policyRevision = updated.Revision
	assert.Equal(t, "disabled", updated.Mode)

	require.NoError(t, repository.FailSession(ctx, session.ID))
	require.ErrorIs(t, repository.FailSession(ctx, session.ID), entity.ErrInferenceSessionPinned)
}

func usageContainsTaskCost(
	items []entity.InferenceUsageRow,
	provider string,
	model string,
	cost int64,
) bool {
	for i := range items {
		if items[i].Provider == provider &&
			items[i].Model == model &&
			items[i].CostNanoUSD == cost {
			return true
		}
	}

	return false
}

func registryContainsTask(items []entity.InferenceRoute, taskKey string) bool {
	for i := range items {
		if items[i].TaskKey == taskKey {
			return true
		}
	}

	return false
}

type liveInferenceAttributionFixture struct {
	callID    string
	attemptID string
}

func seedLiveInferenceAttribution(
	t *testing.T,
	tx pgx.Tx,
) liveInferenceAttributionFixture {
	t.Helper()

	suffix := strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	taskKey := "u0-live-attribution-" + suffix
	providerKey := "u0-live-" + suffix
	modelKey := "model-" + suffix
	promptHash := strings.Repeat("a", 64)
	schemaHash := strings.Repeat("b", 64)
	callID := uuid.NewString()
	runID := uuid.NewString()
	attemptID := uuid.NewString()

	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO inference_tasks (task_key,task_class,output_kind)
VALUES ($1,'answer','structured')`, []any{taskKey}},
		{`INSERT INTO inference_providers (provider_key,display_name,base_url,api_key_env)
VALUES ($1,$1,'https://provider.invalid','U0_LIVE_KEY')`, []any{providerKey}},
		{`INSERT INTO inference_models (
provider_key,model_key,provider_model_id,price_version,
input_nano_usd_per_million,cached_input_nano_usd_per_million,
output_nano_usd_per_million
) VALUES ($1,$2,$2,'price-v1',100,100,200)`, []any{providerKey, modelKey}},
		{`INSERT INTO inference_model_prices (
provider_key,model_key,price_version,input_nano_usd_per_million,
cached_input_nano_usd_per_million,output_nano_usd_per_million
) VALUES ($1,$2,'price-v1',100,100,200)`, []any{providerKey, modelKey}},
		{`INSERT INTO inference_prompt_versions (
task_key,version,messages_template,content_sha256
) VALUES ($1,'prompt-v1','[]',$2)`, []any{taskKey, promptHash}},
		{`INSERT INTO inference_response_schemas (
task_key,version,schema_body,content_sha256
) VALUES ($1,'schema-v1','{}',$2)`, []any{taskKey, schemaHash}},
		{`INSERT INTO inference_calls (id,task_key,task_class,status,cache_status)
VALUES ($1,$2,'answer','started','miss')`, []any{callID, taskKey}},
		{`INSERT INTO generation_runs (
id,task_name,model_id,prompt_version,provider
) VALUES ($1,$2,$3,'prompt-v1',$4)`, []any{runID, taskKey, modelKey, providerKey}},
		{`INSERT INTO inference_attempts (
id,call_id,attempt_no,generation_run_id,task_key,task_class,
provider_key,model_key,provider_model_id,prompt_version,prompt_sha256,
response_schema_version,response_schema_sha256,price_version
) VALUES (
$1,$2,1,$3,$4,'answer',$5,$6,$6,'prompt-v1',$7,'schema-v1',$8,'price-v1'
)`, []any{attemptID, callID, runID, taskKey, providerKey, modelKey, promptHash, schemaHash}},
	}
	for _, statement := range statements {
		_, err := tx.Exec(t.Context(), statement.sql, statement.args...)
		require.NoError(t, err)
	}

	return liveInferenceAttributionFixture{callID: callID, attemptID: attemptID}
}

func assertRejectedAttributionMutation(
	t *testing.T,
	tx pgx.Tx,
	attemptID string,
	column string,
	value string,
) {
	t.Helper()

	savepoint := "u0_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	_, err := tx.Exec(t.Context(), "SAVEPOINT "+pgx.Identifier{savepoint}.Sanitize())
	require.NoError(t, err)
	_, err = tx.Exec(
		t.Context(),
		fmt.Sprintf(
			"UPDATE inference_attempts SET %s=$2 WHERE id=$1",
			pgx.Identifier{column}.Sanitize(),
		),
		attemptID,
		value,
	)
	require.Error(t, err)
	_, rollbackErr := tx.Exec(
		t.Context(),
		"ROLLBACK TO SAVEPOINT "+pgx.Identifier{savepoint}.Sanitize(),
	)
	require.NoError(t, rollbackErr)
}
