package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBookProductionWorkflowMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260530000005_book_production_workflow.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS book_production_projects")
	assert.Contains(t, upSQL, "CREATE UNIQUE INDEX IF NOT EXISTS idx_book_production_projects_active_book_lang")
	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS section_translation_edits")
	assert.Contains(t, upSQL, "ADD COLUMN IF NOT EXISTS is_deleted BOOLEAN NOT NULL DEFAULT FALSE")
	assert.Contains(t, upSQL, "Backfilled from existing published translation assets")

	down, err := os.ReadFile("../../../migrations/20260530000005_book_production_workflow.down.sql")
	require.NoError(t, err)
	downSQL := string(down)

	for _, table := range []string{
		"section_audio_edits",
		"heading_summary_edits",
		"section_translation_edits",
		"book_production_projects",
	} {
		assert.True(t, strings.Contains(downSQL, "DROP TABLE IF EXISTS "+table), table)
	}
	assert.Contains(t, downSQL, "DROP COLUMN IF EXISTS is_deleted")
}

func TestBookProductionEventsMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260531000006_book_production_events.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS book_production_events")
	assert.Contains(t, upSQL, "project_id UUID NOT NULL REFERENCES book_production_projects(id) ON DELETE CASCADE")
	assert.Contains(t, upSQL, "CREATE INDEX IF NOT EXISTS idx_book_production_events_project_created")
	assert.Contains(t, upSQL, "CREATE INDEX IF NOT EXISTS idx_book_production_events_actor_created")
	assert.Contains(t, upSQL, "CREATE INDEX IF NOT EXISTS idx_book_production_events_type_created")

	down, err := os.ReadFile("../../../migrations/20260531000006_book_production_events.down.sql")
	require.NoError(t, err)
	downSQL := string(down)

	assert.Contains(t, downSQL, "DROP TABLE IF EXISTS book_production_events")
}

func TestBookProductionDraftRevisionsMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260531000008_book_production_draft_revisions.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS book_production_draft_revisions")
	assert.Contains(t, upSQL, "project_id UUID NOT NULL REFERENCES book_production_projects(id) ON DELETE CASCADE")
	assert.Contains(t, upSQL, "snapshot JSONB NOT NULL")
	assert.Contains(t, upSQL, "idx_book_production_draft_revisions_project_asset")
	assert.Contains(t, upSQL, "idx_book_production_draft_revisions_unique_version")

	down, err := os.ReadFile("../../../migrations/20260531000008_book_production_draft_revisions.down.sql")
	require.NoError(t, err)
	downSQL := string(down)

	assert.Contains(t, downSQL, "DROP TABLE IF EXISTS book_production_draft_revisions")
}
