package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuranSitemapSlugRegistryMigrationContracts(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260715000001_quran_sitemap_slug_registry.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260715000001_quran_sitemap_slug_registry.down.sql")
	require.NoError(t, err)

	upSQL, downSQL := string(up), string(down)
	for _, token := range []string{
		"CREATE TABLE IF NOT EXISTS quran_surah_slug_registry",
		"slug TEXT PRIMARY KEY",
		"REFERENCES quran_surahs(surah_id) ON DELETE RESTRICT",
		"^[a-z0-9]+(-[a-z0-9]+)*$",
		"INSERT INTO quran_surah_slug_registry",
		"quran_surah_slug_registry_sync",
		"AFTER UPDATE OF slug ON quran_surahs",
		"quran_surah_slug_registry_immutable_guard",
		"BEFORE UPDATE OR DELETE ON quran_surah_slug_registry",
		"BEFORE TRUNCATE ON quran_surah_slug_registry",
		"quran_editorial_public_slug_guard",
		"NEW.status = 'published' AND NEW.license_status = 'permitted'",
		"VALIDATE CONSTRAINT quran_surahs_slug_format_check",
	} {
		assert.Contains(t, upSQL, token)
	}

	assert.Less(
		t,
		strings.Index(upSQL, "ADD CONSTRAINT quran_surahs_slug_format_check"),
		strings.Index(upSQL, "VALIDATE CONSTRAINT quran_surahs_slug_format_check"),
	)

	for _, token := range []string{
		"cannot roll back Q-4: historical Quran surah slugs exist",
		"ERRCODE = '55000'",
		"DROP TABLE IF EXISTS quran_surah_slug_registry",
		"DROP CONSTRAINT IF EXISTS quran_surahs_slug_format_check",
	} {
		assert.Contains(t, downSQL, token)
	}

	preflight := strings.Index(downSQL, "cannot roll back Q-4")
	dropRegistry := strings.Index(downSQL, "DROP TABLE IF EXISTS quran_surah_slug_registry")

	require.Greater(t, preflight, -1)
	require.Greater(t, dropRegistry, preflight)
	assert.NotContains(t, downSQL, "DELETE FROM quran_surah_slug_registry")
}
