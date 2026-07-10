package persistent

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrossReferenceMigrationContracts(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260710000001_add_cross_references.up.sql")
	if os.IsNotExist(err) {
		up, err = os.ReadFile("migrations/20260710000001_add_cross_references.up.sql")
	}

	require.NoError(t, err)

	down, err := os.ReadFile("../../../migrations/20260710000001_add_cross_references.down.sql")
	if os.IsNotExist(err) {
		down, err = os.ReadFile("migrations/20260710000001_add_cross_references.down.sql")
	}

	require.NoError(t, err)

	upSQL, downSQL := string(up), string(down)
	for _, token := range []string{
		"CREATE TABLE IF NOT EXISTS cross_references",
		"CREATE TABLE IF NOT EXISTS quran_cross_reference_bridge",
		"CREATE TABLE IF NOT EXISTS cross_reference_registry_state",
		"current_setting('surau.cross_reference_writer', true)",
		"FOR KEY SHARE",
		"cross_reference_anchor_visible",
		"idx_cross_references_source_lookup",
		"idx_cross_references_target_lookup",
		"idx_cross_references_target_quran_containment",
		"method = 'machine'",
		"method_detail ->> 'prompt_version'",
		"confidence IS NOT NULL OR origin = 'legacy_quran_reference'",
		"created_by UUID REFERENCES users (id) ON DELETE RESTRICT",
		"reviewed_by UUID REFERENCES users (id) ON DELETE RESTRICT",
	} {
		assert.Contains(t, upSQL, token)
	}

	for _, token := range []string{
		"DROP TRIGGER IF EXISTS trg_quran_book_references_cross_reference_guard",
		"DROP TABLE IF EXISTS cross_reference_registry_state",
		"DROP TABLE IF EXISTS quran_cross_reference_bridge",
		"DROP TABLE IF EXISTS cross_references",
		"DROP FUNCTION IF EXISTS cross_reference_anchor_visible(TEXT)",
		"DROP FUNCTION IF EXISTS cross_reference_registry_guard()",
	} {
		assert.Contains(t, downSQL, token)
	}
}
