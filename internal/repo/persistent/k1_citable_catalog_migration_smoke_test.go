package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestK1CitableCatalogMigrationContracts(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260713000001_add_k1_citable_catalog.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260713000001_add_k1_citable_catalog.down.sql")
	require.NoError(t, err)

	upSQL := string(up)
	for _, token := range []string{
		"ADD COLUMN IF NOT EXISTS units_stale_at TIMESTAMPTZ",
		"ADD COLUMN IF NOT EXISTS units_derivation_profile_version INTEGER",
		"ADD COLUMN IF NOT EXISTS content_role TEXT",
		"ADD COLUMN IF NOT EXISTS review_status TEXT",
		"ADD COLUMN IF NOT EXISTS source_document_hash BYTEA",
		"ADD COLUMN IF NOT EXISTS source_char_start INTEGER",
		"ADD COLUMN IF NOT EXISTS source_char_end INTEGER",
		"PERFORM set_config('surau.registry_writer', 'unit-service', true)",
		"ALTER COLUMN content_role SET DEFAULT 'legacy_auto'",
		"CREATE OR REPLACE FUNCTION citable_unit_k1_content_role_compat()",
		"CASE WHEN NEW.corpus = 'kitab' THEN 'book_page' ELSE NULL END",
		"CREATE TRIGGER trg_citable_unit_k1_content_role_compat",
		"'book_page', 'section_translation', 'heading_summary'",
		"corpus = 'kitab' AND content_role IS NOT NULL",
		"'paragraph', 'heading', 'quran_quote', 'footnote', 'html', 'summary'",
		"ADD COLUMN IF NOT EXISTS interpretive_retrieval_eligible BOOLEAN",
		"GENERATED ALWAYS AS",
		"corpus <> 'quran'",
		"kind <> 'quran_quote'",
		"provenance_class IN ('editorial', 'machine')",
		"review_status = 'approved'",
		"CREATE OR REPLACE VIEW public_book_interpretive_citable_units",
		"JOIN public_book_publications publication",
		"u.license_status IS NULL OR u.license_status = 'permitted'",
		"b.units_derivation_profile_version = 2",
		"CREATE TABLE IF NOT EXISTS citable_unit_catalog_queue",
		"ADD COLUMN IF NOT EXISTS unit_id UUID",
		"ADD COLUMN IF NOT EXISTS unit_char_start INTEGER",
		"ADD COLUMN IF NOT EXISTS unit_char_end INTEGER",
		"ADD COLUMN IF NOT EXISTS unit_binding_status TEXT NOT NULL DEFAULT 'pending'",
		"ADD COLUMN IF NOT EXISTS unit_binding_version INTEGER NOT NULL DEFAULT 1",
		"ADD COLUMN IF NOT EXISTS unit_source_hash TEXT",
		"FOREIGN KEY (unit_id) REFERENCES citable_units(id) ON DELETE RESTRICT",
		"NOT VALID",
		"VALIDATE CONSTRAINT knowledge_mentions_unit_fk",
		"CREATE TRIGGER trg_book_pages_units_stale",
		"CREATE TRIGGER trg_books_units_stale",
		"CREATE OR REPLACE FUNCTION kitab_units_book_stale_trigger()",
		"CREATE TRIGGER trg_book_page_edits_units_stale",
		"CREATE TRIGGER trg_section_translations_units_stale",
		"CREATE TRIGGER trg_book_heading_summaries_units_stale",
		"CREATE OR REPLACE FUNCTION kitab_units_asset_stale_trigger()",
		"CREATE CONSTRAINT TRIGGER trg_knowledge_mentions_approved_unit_guard",
		"CREATE OR REPLACE FUNCTION knowledge_mention_approved_unit_guard()",
		"CREATE TRIGGER trg_book_production_projects_units_stale",
	} {
		assert.Contains(t, upSQL, token)
	}

	assert.NotContains(t, upSQL, "effective_license_status = 'permitted'",
		"B-4 grandfather visibility must come from public_book_publications")
	assert.NotContains(t, upSQL, "provenance_class = 'machine' OR",
		"machine units must never bypass review approval")
	assert.NotContains(t, upSQL, "DROP CONSTRAINT IF EXISTS citable_units_generation_identity_check",
		"K-1 must not open even a brief B-6 enforcement gap")

	downSQL := string(down)
	for _, token := range []string{
		"DROP VIEW IF EXISTS public_book_interpretive_citable_units",
		"DROP TABLE IF EXISTS citable_unit_catalog_queue",
		"DROP CONSTRAINT IF EXISTS knowledge_mentions_unit_fk",
		"DROP COLUMN IF EXISTS unit_id",
		"DROP COLUMN IF EXISTS interpretive_retrieval_eligible",
		"DROP COLUMN IF EXISTS content_role",
		"DROP COLUMN IF EXISTS units_derivation_profile_version",
		"DROP FUNCTION IF EXISTS kitab_units_source_stale_trigger()",
		"DROP FUNCTION IF EXISTS kitab_units_book_stale_trigger()",
		"DROP FUNCTION IF EXISTS citable_unit_k1_content_role_compat()",
	} {
		assert.Contains(t, downSQL, token)
	}
}

func TestK1CrossReferenceAuditUsesResolvableAnchorProjection(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("citable_unit_postgres.go")
	require.NoError(t, err)

	auditSQL := string(source)
	assert.Contains(t, auditSQL, "WITH RECURSIVE kitab_points AS")
	assert.Contains(t, auditSQL, "unit_walk(")
	assert.Contains(t, auditSQL, "crossed_boundary")
	assert.Contains(t, auditSQL, "WHERE NOT root_exists OR crossed_boundary OR NOT has_active")
	assert.Contains(t, auditSQL, "AND NOT cross_reference_anchor_point_visible(point)")
	assert.NotContains(t, auditSQL,
		"AND EXISTS (SELECT 1 FROM citable_units unit WHERE unit.anchor = point)",
		"a tombstoned row without an active lineage endpoint is unresolved")
}

func TestK1MentionBypassIsLimitedToInitialProfileUpgrade(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("citable_unit_catalog_tx.go")
	require.NoError(t, err)

	txSource := string(source)
	assert.Contains(t, txSource,
		"units_derivation_profile_version IS NULL")
	assert.Contains(t, txSource,
		"if legacyMentionBindingBypass {")
	assert.Contains(t, txSource,
		"SET CONSTRAINTS trg_knowledge_mentions_approved_unit_guard IMMEDIATE")
}

func TestK1RegistryChecksumHashesRowsBeforeAggregation(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("citable_unit_catalog_tx.go")
	require.NoError(t, err)

	text := string(source)
	registryStart := strings.Index(text, "func catalogRegistryChecksum(")
	require.NotEqual(t, -1, registryStart)
	registryEnd := strings.Index(text[registryStart:], "// CatalogEvidenceChecksums")
	require.NotEqual(t, -1, registryEnd)
	registrySQL := text[registryStart : registryStart+registryEnd]

	assert.Contains(t, registrySQL, "WITH unit_rows AS MATERIALIZED")
	assert.Contains(t, registrySQL, "sha256(convert_to(jsonb_build_array(")
	assert.Contains(t, registrySQL, "string_agg(encode(row_digest, 'hex')")
	assert.Contains(t, registrySQL, "ORDER BY predecessor_id, successor_id, reason")
	assert.NotContains(t, registrySQL, "jsonb_agg(",
		"large catalogs must never materialize one PostgreSQL JSONB array")
}

func TestK1CatalogChecksumVersionMigrationContracts(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260713000014_add_k1_catalog_checksum_version.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260713000014_add_k1_catalog_checksum_version.down.sql")
	require.NoError(t, err)

	upSQL := string(up)
	assert.Contains(t, upSQL, "ADD COLUMN IF NOT EXISTS checksum_version INTEGER NOT NULL DEFAULT 1")
	assert.Contains(t, upSQL, "CHECK (checksum_version >= 1) NOT VALID")
	assert.Contains(t, upSQL, "VALIDATE CONSTRAINT citable_unit_catalog_queue_checksum_version_check")
	assert.Contains(t, string(down), "DROP COLUMN IF EXISTS checksum_version")
}

func TestK1CatalogProfileThreeMigrationContracts(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260713000015_harden_k1_catalog_profile.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260713000015_harden_k1_catalog_profile.down.sql")
	require.NoError(t, err)

	upSQL := string(up)
	assert.Contains(t, upSQL, "units_derivation_profile_version IS DISTINCT FROM 3")
	assert.Contains(t, upSQL, "book.units_derivation_profile_version = 3")
	assert.Contains(t, upSQL, "PERFORM set_config('surau.registry_writer', 'unit-service', true)")
	assert.Contains(t, upSQL, "HAVING COUNT(DISTINCT walk.unit_id) = 1")
	assert.Contains(t, upSQL, "'\"unlinked\"'::jsonb")
	assert.Contains(t, string(down), "book.units_derivation_profile_version = 2")
	assert.Contains(t, string(down), "units_stale_at = COALESCE")
}

func TestK1CitableUnitParentInvariantMigrationContracts(t *testing.T) {
	t.Parallel()

	expand, err := os.ReadFile("../../../migrations/20260714000001_enforce_citable_unit_parent_invariant.up.sql")
	require.NoError(t, err)
	validate, err := os.ReadFile("../../../migrations/20260714000002_validate_citable_unit_parent_invariant.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260714000001_enforce_citable_unit_parent_invariant.down.sql")
	require.NoError(t, err)

	expandSQL := string(expand)
	assert.Contains(t, expandSQL, "PERFORM set_config('surau.registry_writer', 'unit-service', true)")
	assert.Contains(t, expandSQL, "HAVING COUNT(DISTINCT walk.unit_id) = 1")
	assert.Contains(t, expandSQL, "ADD CONSTRAINT citable_units_parent_shape_check")
	assert.Contains(t, expandSQL, "NOT VALID")
	assert.Contains(t, expandSQL, "CREATE CONSTRAINT TRIGGER trg_citable_unit_parent_invariant")
	assert.Contains(t, expandSQL, "DEFERRABLE INITIALLY DEFERRED")
	assert.Contains(t, expandSQL, "parent.lifecycle = 'active'")
	assert.Contains(t, string(validate), "VALIDATE CONSTRAINT citable_units_parent_shape_check")
	assert.Contains(t, string(down), "DROP TRIGGER IF EXISTS trg_citable_unit_parent_invariant")
	assert.Contains(t, string(down), "DROP CONSTRAINT IF EXISTS citable_units_parent_shape_check")
}

func TestK1EditorialReconcileBindsMentionsBeforeCommit(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("citable_unit_postgres.go")
	require.NoError(t, err)

	text := string(source)
	apply := strings.Index(text, "func (r *CitableUnitRepo) ApplyReconcile")
	require.NotEqual(t, -1, apply)

	bind := strings.Index(text[apply:], ").BindKnowledgeMentions(ctx, plan.BookID)")
	commit := strings.Index(text[apply:], "tx.Commit(ctx)")

	require.NotEqual(t, -1, bind)
	require.NotEqual(t, -1, commit)
	assert.Less(t, bind, commit, "approved mention guard must run inside the registry transaction")
}

//nolint:wsl_v5 // migration file matrix stays adjacent to each assertion
func TestK1CitableCatalogOnlineIndexesAreOnePerMigration(t *testing.T) {
	t.Parallel()

	files := []string{
		"20260713000002_add_k1_citable_catalog_indexes.up.sql",
		"20260713000003_add_k1_scope_v2_index.up.sql",
		"20260713000004_add_k1_content_v1_index.up.sql",
		"20260713000005_add_k1_content_v2_index.up.sql",
		"20260713000006_add_k1_interpretive_index.up.sql",
		"20260713000007_add_k1_mention_binding_index.up.sql",
		"20260713000008_swap_k1_citable_catalog_indexes.up.sql",
		"20260713000009_add_k1_unit_text_trgm_index.up.sql",
		"20260713000010_add_k1_unit_text_unmarked_trgm_index.up.sql",
		"20260713000016_add_k1_unit_fts_index.up.sql",
	}

	for _, name := range files {
		contents, err := os.ReadFile("../../../migrations/" + name)
		require.NoError(t, err, name)
		upSQL := string(contents)
		createCount := strings.Count(upSQL, "CREATE INDEX CONCURRENTLY") +
			strings.Count(upSQL, "CREATE UNIQUE INDEX CONCURRENTLY")
		assert.Equal(t, 1, createCount, name)
	}

	dropFiles := []string{
		"20260713000011_drop_k1_legacy_scope_index.up.sql",
		"20260713000012_drop_k1_legacy_content_index.up.sql",
	}
	for _, name := range dropFiles {
		contents, err := os.ReadFile("../../../migrations/" + name)
		require.NoError(t, err, name)
		assert.Equal(t, 1, strings.Count(string(contents), "DROP INDEX CONCURRENTLY"), name)
		assert.NotContains(t, string(contents), "CREATE INDEX CONCURRENTLY", name)
	}

	guard, err := os.ReadFile("../../../migrations/20260713000013_k1_enrichment_down_guard.down.sql")
	require.NoError(t, err)
	assert.Contains(t, string(guard), "DELETE FROM citable_units")
	assert.NotContains(t, string(guard), "INDEX CONCURRENTLY")
}
