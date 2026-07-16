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
	assert.Contains(t, text, `grep -q '"target_type":"quran_ayah"'`)
	assert.Contains(t, text, `grep -Eq '"primary_unit_id":"[0-9a-f-]+"'`)
	assert.Contains(t, text, `grep -Eq '"primary_unit_anchor":"quran/[0-9]+:[0-9]+/u/1"'`)
	assert.NotContains(t, text, `grep -q '"target_type":"citable_unit"'`)
}

func TestQuranProdRolloutGatesReleaseOnCompletePageNavigation(t *testing.T) {
	t.Parallel()

	workflow, err := os.ReadFile("../../.github/workflows/deploy-prod.yml")
	require.NoError(t, err)

	text := string(workflow)

	healthy := strings.Index(text, `candidate_healthy=true`)
	navigation := strings.Index(text, "-job=quran-page-navigation-v1")
	citable := strings.Index(text, "-job=citable-units-quran")
	corpusGate := strings.Index(text, `test "$total_ayahs" = 6236`)
	pageSmoke := strings.Index(text, "/v1/quran/pages/1/ayahs")
	resolverSmoke := strings.Index(text, "/v1/anchors/resolve?page_number=1")
	imagePrune := strings.LastIndex(text, "sudo docker image prune -f")
	publicGate := strings.Index(text, "- name: Verify public production contracts")
	release := strings.Index(text, "- name: Create GitHub Release")

	for _, position := range []int{
		healthy, navigation, citable, corpusGate, pageSmoke, resolverSmoke, imagePrune, publicGate, release,
	} {
		require.NotEqual(t, -1, position)
	}

	assert.Less(t, healthy, navigation)
	assert.Less(t, navigation, citable)
	assert.Less(t, citable, corpusGate)
	assert.Less(t, corpusGate, pageSmoke)
	assert.Less(t, pageSmoke, resolverSmoke)
	assert.Less(t, resolverSmoke, imagePrune)
	assert.Less(t, imagePrune, publicGate)
	assert.Less(t, publicGate, release)

	assert.Contains(t, text, "timeout-minutes: 45")
	assert.Contains(t, text, "ServerAliveInterval=30")
	assert.Contains(t, text, `test "$assigned_ayahs" = 6236`)
	assert.Contains(t, text, `test "$distinct_pages" = 604`)
	assert.Contains(t, text, `test "$first_page" = 1`)
	assert.Contains(t, text, `test "$last_page" = 604`)
	assert.Contains(t, text, `test "$out_of_range" = 0`)
	assert.Contains(t, text, `test "$page_one_count" = 7`)
	assert.Contains(t, text, `test "$page_one_keys" = '1:1,1:2,1:3,1:4,1:5,1:6,1:7'`)
	assert.Contains(t, text, `test "$final_ayah" = '114:6'`)
	assert.Contains(t, text, `grep -q '"target_type":"quran_ayah"'`)
	assert.Contains(t, text, `grep -Eq '"primary_unit_id":"[0-9a-f-]+"'`)
	assert.Contains(t, text, `grep -Eq '"primary_unit_anchor":"quran/[0-9]+:[0-9]+/u/1"'`)
	assert.Contains(t, text, "-job=quran-page-navigation-v1 -chunk-size=500 -sleep=0s -restart </dev/null")
	assert.Contains(t, text, "-job=citable-units-quran -chunk-size=1 -sleep=0s -restart </dev/null")
	assert.Contains(t, text, "https://api.surau.org/v1/quran/pages/1/ayahs?view=reader_minimal")
	assert.Contains(t, text, "https://api.surau.org/v1/anchors/resolve?page_number=1")
}
