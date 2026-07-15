package dbcredential

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestImporterURLRequiresExplicitLegacyOverlap(t *testing.T) {
	t.Setenv("IMPORTER_PG_URL", "")
	t.Setenv("PG_URL", "postgres://owner/db")
	t.Setenv("ALLOW_LEGACY_DB_CREDENTIALS", "")
	assert.Empty(t, ImporterURL())

	t.Setenv("ALLOW_LEGACY_DB_CREDENTIALS", "true")
	assert.Equal(t, "postgres://owner/db", ImporterURL())

	t.Setenv("IMPORTER_PG_URL", "postgres://importer/db")
	assert.Equal(t, "postgres://importer/db", ImporterURL())
}
