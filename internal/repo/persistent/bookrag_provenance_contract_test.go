package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBookRAGQueriesFreezeRetrievalEligibility protects PK-1/O-4-4 at the
// query boundary. Public reader queries intentionally show generated assets,
// but every place BookRAG consumes catalog metadata, section translations, or
// summaries must require source provenance or a human-reviewed final asset.
func TestBookRAGQueriesFreezeRetrievalEligibility(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("bookrag_postgres.go")
	require.NoError(t, err)

	querySource := string(source)
	assert.Equal(t, 1, strings.Count(
		querySource,
		"AND (bmt.provenance_class = 'source' OR bmt.translation_status = 'reviewed')",
	), "catalog metadata must be gated in GetRAGBookDocument")
	assert.Equal(t, 3, strings.Count(
		querySource,
		"AND (st.provenance_class = 'source' OR st.translation_status = 'reviewed')",
	), "section translations must be gated in structure, evidence, and lexical search")
	assert.Equal(t, 2, strings.Count(
		querySource,
		"AND (bhs_lang.provenance_class = 'source' OR bhs_lang.summary_status = 'reviewed')",
	), "requested-language summaries must be gated in structure and lexical search")
	assert.Equal(t, 2, strings.Count(
		querySource,
		"AND (bhs_ar.provenance_class = 'source' OR bhs_ar.summary_status = 'reviewed')",
	), "Arabic fallback summaries must use the same eligibility rule")
}
