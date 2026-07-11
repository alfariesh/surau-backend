package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerationRunMigrationContracts(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260711000002_add_generation_runs.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260711000002_add_generation_runs.down.sql")
	require.NoError(t, err)

	upSQL := string(up)
	for _, token := range []string{
		"CREATE TABLE IF NOT EXISTS generation_runs",
		"CREATE OR REPLACE FUNCTION generation_run_safe_uuid",
		"CREATE TRIGGER trg_generation_runs_immutable",
		"FOREIGN KEY (id) REFERENCES generation_runs (id)",
		"ADD COLUMN IF NOT EXISTS generation_run_id UUID",
		"cross_references_generation_run_fk",
		"citable_units_generation_run_fk",
		"constraint_name := table_name || '_generation_run_fk'",
		"WHERE conname = constraint_name",
		"LEFT JOIN generation_runs gr ON gr.id = child.generation_run_id",
		"citable_units_generation_identity_check",
		"cross_references_generation_identity_check",
		"generation_run_safe_uuid(method_detail ->> 'run_id') = generation_run_id",
		"CREATE OR REPLACE FUNCTION generated_asset_provenance_guard()",
		"OLD.provenance_class = 'legacy_unknown'",
		"rewritten legacy asset must declare provenance_class",
		"machine generated asset requires generation_run_id",
		"citable unit generation_run_id is immutable",
		"cross-reference generation_run_id is immutable",
		"knowledge_extraction_generation_identity_guard",
		"knowledge extraction generation tuple conflicts with registry",
		"surau.production_publish",
		"publish cannot relabel generated asset without new text",
	} {
		assert.Contains(t, upSQL, token)
	}

	assert.Contains(t, upSQL, ") NOT VALID", "untouched legacy machine units remain readable")
	assert.NotContains(t, upSQL, "[1-5][0-9a-fA-F]{3}",
		"UUID validation must not reject valid UUID versions such as v7")
	assert.NotContains(t, upSQL, "method_detail ->> 'run_id' = generation_run_id::TEXT",
		"legacy UUID comparison must use PostgreSQL's canonical UUID type")
	assert.Contains(t, upSQL, "ALTER COLUMN provenance_class DROP DEFAULT",
		"fast legacy backfill must not leave a default available to new writers")
	assert.NotContains(t, upSQL, "idx_cross_references_generation_run")
	assert.NotContains(t, upSQL, "idx_citable_units_generation_run")

	for _, table := range []string{
		"book_metadata_translations",
		"author_translations",
		"category_translations",
		"section_translations",
		"book_heading_summaries",
		"book_metadata_translation_edits",
		"author_translation_edits",
		"category_translation_edits",
		"section_translation_edits",
		"heading_summary_edits",
	} {
		assert.Contains(t, upSQL, "ALTER TABLE "+table)
		assert.Contains(t, upSQL, "trg_"+table+"_provenance")
		assert.NotContains(t, upSQL, "idx_"+table+"_generation_run",
			"unused child index would take a blocking build lock during deploy")
		assert.Contains(t, string(down), "DROP TRIGGER IF EXISTS trg_"+table+"_provenance")
	}

	downSQL := string(down)
	for _, token := range []string{
		"DROP FUNCTION IF EXISTS generated_asset_provenance_guard()",
		"DROP FUNCTION IF EXISTS cross_reference_generation_identity_guard()",
		"DROP FUNCTION IF EXISTS citable_unit_generation_identity_guard()",
		"DROP FUNCTION IF EXISTS knowledge_extraction_generation_identity_guard()",
		"DROP CONSTRAINT IF EXISTS knowledge_extraction_runs_generation_run_fk",
		"DROP TABLE IF EXISTS generation_runs",
		"DROP FUNCTION IF EXISTS generation_runs_immutable_guard()",
		"DROP FUNCTION IF EXISTS generation_run_safe_uuid(TEXT)",
	} {
		assert.Contains(t, downSQL, token)
	}

	assert.Less(t,
		strings.Index(downSQL, "DROP CONSTRAINT IF EXISTS knowledge_extraction_runs_generation_run_fk"),
		strings.Index(downSQL, "DROP TABLE IF EXISTS generation_runs"),
		"dependent foreign keys must be removed before the registry")
}
