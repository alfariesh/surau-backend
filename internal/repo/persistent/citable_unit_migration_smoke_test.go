package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Cheap contract lock on the B-1 registry migration text: the round-trip CI
// job proves up/down are symmetric against a live DB, this pins the specific
// objects and the write-guard so an accidental edit is caught in unit tests.
func TestCitableUnitsMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260709065724_add_citable_units.up.sql")
	require.NoError(t, err)

	upSQL := string(up)

	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS citable_units")
	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS citable_unit_lineage")
	assert.Contains(t, upSQL, "CREATE UNIQUE INDEX IF NOT EXISTS uq_citable_units_anchor")
	assert.Contains(t, upSQL, "CREATE UNIQUE INDEX IF NOT EXISTS uq_citable_units_scope_ordinal")
	assert.Contains(t, upSQL, "CREATE UNIQUE INDEX IF NOT EXISTS uq_citable_units_active_content")
	assert.Contains(t, upSQL, "NULLS NOT DISTINCT")
	// The single-write-path guard (C2) and its cascade escape hatch.
	assert.Contains(t, upSQL, "CREATE OR REPLACE FUNCTION citable_registry_guard()")
	assert.Contains(t, upSQL, "current_setting('surau.registry_writer', true) IS NOT DISTINCT FROM 'unit-service'")
	assert.Contains(t, upSQL, "pg_trigger_depth() > 1")
	assert.Contains(t, upSQL, "CREATE TRIGGER trg_citable_units_guard")
	assert.Contains(t, upSQL, "CREATE TRIGGER trg_citable_unit_lineage_guard")
	assert.Contains(t, upSQL, "ADD COLUMN IF NOT EXISTS units_derived_at TIMESTAMPTZ")

	down, err := os.ReadFile("../../../migrations/20260709065724_add_citable_units.down.sql")
	require.NoError(t, err)

	downSQL := string(down)

	for _, object := range []string{
		"DROP TABLE IF EXISTS citable_unit_lineage",
		"DROP TABLE IF EXISTS citable_units",
		// The function must be dropped explicitly: the round-trip CI job
		// asserts no app-owned functions survive `down -all`.
		"DROP FUNCTION IF EXISTS citable_registry_guard()",
		"DROP COLUMN IF EXISTS units_derived_at",
	} {
		assert.True(t, strings.Contains(downSQL, object), object)
	}

	// Lineage must be dropped before units (FK order).
	assert.Less(t,
		strings.Index(downSQL, "DROP TABLE IF EXISTS citable_unit_lineage"),
		strings.Index(downSQL, "DROP TABLE IF EXISTS citable_units"),
		"lineage table drops before units table")
}
