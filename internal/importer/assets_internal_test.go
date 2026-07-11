package importer

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectAssetGenerationRunsReportsConflictingLine(t *testing.T) {
	t.Parallel()

	input := `{"kind":"translation","book_id":797,"heading_id":10,"lang":"id","content":"one","provenance_class":"machine","generation":{"run_id":"550e8400-e29b-41d4-a716-446655440000","model_id":"model-a","prompt_version":"reader-translation-v1"}}` + "\n" +
		`{"kind":"translation","book_id":797,"heading_id":11,"lang":"id","content":"two","provenance_class":"machine","generation":{"run_id":"550e8400-e29b-41d4-a716-446655440000","model_id":"model-b","prompt_version":"reader-translation-v1"}}`

	assets, err := readReaderAssets(strings.NewReader(input))
	require.NoError(t, err)

	_, err = collectAssetGenerationRuns(assets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "line 2")
	assert.Contains(t, err.Error(), "conflicts with line 1")
}
