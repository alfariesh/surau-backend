package backfill

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuranDevRolloutOrdersNavigationBeforeCitableReconcile(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile("../../.github/workflows/deploy-dev.yml")
	require.NoError(t, err)

	text := string(workflow)

	navigation := strings.Index(text, "-job=quran-page-navigation-v1")
	citable := strings.Index(text, "-job=citable-units-quran")
	pageSmoke := strings.Index(text, "/v1/quran/pages/1/ayahs")
	resolverSmoke := strings.Index(text, "/v1/anchors/resolve?page_number=1")

	require.NotEqual(t, -1, navigation)
	require.NotEqual(t, -1, citable)
	require.NotEqual(t, -1, pageSmoke)
	require.NotEqual(t, -1, resolverSmoke)
	assert.Less(t, navigation, citable)
	assert.Less(t, citable, pageSmoke)
	assert.Less(t, pageSmoke, resolverSmoke)
	assert.Contains(t, text, `grep -q '"target_type":"citable_unit"'`)
}
