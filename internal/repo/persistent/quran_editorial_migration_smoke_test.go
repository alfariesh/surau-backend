package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Pins Q-1's migration-only guarantees. Live migration round-trip CI proves
// PostgreSQL execution; this fast test prevents an accidental weakening of the
// grandfather, public visibility, revision, or single-write-path contracts.
func TestQuranEditorialWorkflowMigrationContracts(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260711000006_quran_editorial_workflow.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260711000006_quran_editorial_workflow.down.sql")
	require.NoError(t, err)

	upSQL, downSQL := string(up), string(down)

	for _, token := range []string{
		"SET status = 'published',\n    published_at = updated_at",
		"ALTER COLUMN status SET DEFAULT 'draft'",
		"quran_surah_editorial_publish_timestamp_check",
		"quran_ayah_editorial_publish_timestamp_check",
		"quran_surah_editorial_updated_by_fk",
		"quran_ayah_editorial_updated_by_fk",
		"FOREIGN KEY (updated_by) REFERENCES users (id) ON DELETE SET NULL NOT VALID",
		"PRIMARY KEY (surah_id, lang, status)",
		"PRIMARY KEY (surah_id, ayah_number, lang, status)",
		"CREATE TABLE IF NOT EXISTS quran_editorial_revisions",
		"resource_type TEXT NOT NULL",
		"status TEXT NOT NULL",
		"CHECK (status IN ('draft', 'published'))",
		"CHECK (origin IN ('rest', 'import', 'restore'))",
		"COALESCE(ayah_number, 0)",
		"uq_quran_editorial_revisions_scope_version",
		"to_jsonb(editorial)",
		"is_migration_baseline",
		"CREATE OR REPLACE VIEW quran_surah_editorial_public",
		"CREATE OR REPLACE VIEW quran_ayah_editorial_public",
		"WHERE status = 'published' AND license_status = 'permitted'",
		"idx_quran_surah_editorial_published_permitted",
		"idx_quran_ayah_editorial_published_permitted",
		"current_setting('surau.quran_editorial_writer', true)",
		"IS DISTINCT FROM 'quran-editorial-service'",
		"pg_trigger_depth() > 1",
		"CONSTRAINT = 'quran_editorial_published_license_check'",
		"MESSAGE = 'license_not_permitted:",
		"to_jsonb(NEW) ->> 'is_migration_baseline' = 'true'",
		"CREATE TRIGGER trg_quran_surah_editorial_writer_guard",
		"CREATE TRIGGER trg_quran_ayah_editorial_writer_guard",
		"CREATE TRIGGER trg_quran_editorial_revisions_writer_guard",
		"BEFORE UPDATE OF slug, chronological_order, ruku_count ON quran_surahs",
		"BEFORE INSERT ON quran_surahs",
		"CREATE TRIGGER trg_quran_surah_editorial_truncate_guard",
		"CREATE TRIGGER trg_quran_ayah_editorial_truncate_guard",
		"CREATE TRIGGER trg_quran_editorial_revisions_truncate_guard",
		"CREATE TRIGGER trg_quran_surahs_editorial_truncate_guard",
		"CREATE TRIGGER trg_quran_ayahs_editorial_truncate_guard",
		"BEFORE TRUNCATE ON quran_surah_editorial",
		"BEFORE TRUNCATE ON quran_ayah_editorial",
		"BEFORE TRUNCATE ON quran_editorial_revisions",
	} {
		assert.Contains(t, upSQL, token)
	}

	assert.NotContains(t, upSQL, "AND NEW.is_migration_baseline",
		"the shared trigger also runs on rows without this revision-only field")

	uniqueVersionStart := strings.Index(upSQL, "CREATE UNIQUE INDEX IF NOT EXISTS uq_quran_editorial_revisions_scope_version")
	require.Greater(t, uniqueVersionStart, -1)
	uniqueVersionEnd := strings.Index(upSQL[uniqueVersionStart:], ";") + uniqueVersionStart
	require.Greater(t, uniqueVersionEnd, uniqueVersionStart)
	uniqueVersionSQL := upSQL[uniqueVersionStart:uniqueVersionEnd]
	assert.Contains(t, uniqueVersionSQL, "lang,\n        version")
	assert.NotContains(t, uniqueVersionSQL, "status",
		"revision version must be monotonic across draft and published states")

	// Grandfathering may only assign new workflow metadata; public content and
	// cache validators must not be rewritten by this UPDATE.
	grandfatherStart := strings.Index(upSQL, "UPDATE quran_surah_editorial")
	grandfatherEnd := strings.Index(upSQL[grandfatherStart:], ";") + grandfatherStart
	require.Greater(t, grandfatherStart, -1)
	require.Greater(t, grandfatherEnd, grandfatherStart)
	grandfatherSQL := upSQL[grandfatherStart:grandfatherEnd]
	assert.NotContains(t, grandfatherSQL, "checksum =")
	assert.NotContains(t, grandfatherSQL, "updated_at =")

	// The guard must only become active after both baseline INSERTs, otherwise
	// the migration would reject its own grandfather snapshots.
	lastBaseline := strings.LastIndex(upSQL, "INSERT INTO quran_editorial_revisions")
	firstGuard := strings.Index(upSQL, "CREATE TRIGGER trg_quran_surah_editorial_writer_guard")

	require.Greater(t, lastBaseline, -1)
	require.Greater(t, firstGuard, lastBaseline)

	for _, token := range []string{
		"cannot roll back Q-1: draft Quran editorial rows exist",
		"cannot roll back Q-1: post-baseline Quran editorial revisions exist",
		"WHERE NOT is_migration_baseline",
		"DROP VIEW IF EXISTS quran_ayah_editorial_public",
		"DROP VIEW IF EXISTS quran_surah_editorial_public",
		"DROP TABLE IF EXISTS quran_editorial_revisions",
		"PRIMARY KEY (surah_id, ayah_number, lang)",
		"PRIMARY KEY (surah_id, lang)",
		"DROP COLUMN IF EXISTS published_at",
		"DROP COLUMN IF EXISTS updated_by",
		"DROP COLUMN IF EXISTS status",
		"DROP FUNCTION IF EXISTS quran_editorial_writer_guard()",
		"DROP TRIGGER IF EXISTS trg_quran_surah_editorial_truncate_guard",
		"DROP TRIGGER IF EXISTS trg_quran_ayah_editorial_truncate_guard",
		"DROP TRIGGER IF EXISTS trg_quran_editorial_revisions_truncate_guard",
		"DROP TRIGGER IF EXISTS trg_quran_surahs_editorial_truncate_guard",
		"DROP TRIGGER IF EXISTS trg_quran_ayahs_editorial_truncate_guard",
	} {
		assert.Contains(t, downSQL, token)
	}

	preflight := strings.Index(downSQL, "cannot roll back Q-1")
	dropHistory := strings.Index(downSQL, "DROP TABLE IF EXISTS quran_editorial_revisions")

	require.Greater(t, preflight, -1)
	require.Greater(t, dropHistory, preflight)
	assert.NotContains(t, downSQL, "DELETE FROM quran_surah_editorial")
	assert.NotContains(t, downSQL, "DELETE FROM quran_ayah_editorial")
}
