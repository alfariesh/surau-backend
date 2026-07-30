package persistent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferenceMigrationPinsAttributionBudgetAndSafeDown(t *testing.T) {
	t.Parallel()

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)

	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
	up, err := os.ReadFile(filepath.Join(
		root, "migrations", "20260728000001_add_u0_inference_layer.up.sql",
	))
	require.NoError(t, err)
	down, err := os.ReadFile(filepath.Join(
		root, "migrations", "20260728000001_add_u0_inference_layer.down.sql",
	))
	require.NoError(t, err)

	upSQL, downSQL := string(up), string(down)

	for _, table := range []string{
		"inference_tasks", "inference_providers", "inference_models",
		"inference_model_prices",
		"inference_prompt_versions", "inference_response_schemas",
		"inference_routes", "inference_calls", "inference_attempts",
		"inference_sessions", "inference_budget_policies",
		"inference_budget_reservations", "inference_cache",
	} {
		assert.Contains(t, upSQL, "CREATE TABLE "+table)
		assert.Contains(t, downSQL, "DROP TABLE IF EXISTS "+table)
	}

	assert.Contains(t, upSQL, "'inference:invoke'")
	assert.Contains(t, upSQL, "'Asia/Jakarta'")
	assert.Contains(t, upSQL, "DEFAULT 30")
	assert.Contains(t, upSQL, "DEFAULT 80.00")
	assert.Contains(t, upSQL, "inference_attempt_generation_guard")
	assert.Contains(t, upSQL, "generation.model_id <> NEW.provider_model_id")
	assert.Contains(t, upSQL, "generation.prompt_version <> NEW.prompt_version")
	assert.Contains(t, upSQL, "generation.provider IS DISTINCT FROM NEW.provider_key")
	assert.Contains(t, upSQL, "prompt_sha256 ~ '^[0-9a-f]{64}$'")
	assert.Contains(t, upSQL, "response_schema_sha256 ~ '^[0-9a-f]{64}$'")
	assert.Contains(t, upSQL, "registered_price_version IS DISTINCT FROM NEW.price_version")
	assert.Contains(t, upSQL, "policy_sha256")
	assert.Contains(t, upSQL, "inference_immutable_registry_guard")
	assert.Contains(t, upSQL, "NOT VALID")
	assert.Contains(t, upSQL, "VALIDATE CONSTRAINT")
	assert.Contains(t, downSQL, "refusing U-0 down migration")

	// No secret-bearing column may be introduced. Only the environment
	// variable name is registry metadata; values stay in process memory.
	for _, forbidden := range []string{"api_key TEXT", "api_secret", "secret_value", "prompt_text", "output_text"} {
		assert.NotContains(t, strings.ToLower(upSQL), forbidden)
	}
}
