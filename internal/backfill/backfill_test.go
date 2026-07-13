package backfill

import (
	"context"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionedValuesUpdateSQL(t *testing.T) {
	t.Parallel()

	sqlText, args := versionedValuesUpdateSQL(
		"authors",
		"name_search",
		"name_search_normalization_version",
		[]int64{7, 9},
		[]string{"a", "b"},
		1,
	)

	assert.Equal(
		t,
		"UPDATE authors AS t SET name_search = v.value, name_search_normalization_version = ($5)::integer "+
			"FROM (VALUES (($1)::bigint, ($2)::text), (($3)::bigint, ($4)::text)) AS v(id, value) "+
			"WHERE t.id = v.id AND t.name_search_normalization_version IS NULL",
		sqlText,
	)
	assert.Equal(t, []any{int64(7), "a", int64(9), "b", 1}, args)
}

type fakeUnitSearchVerifier struct{ calls int }

func (f *fakeUnitSearchVerifier) SearchRAGUnits(
	context.Context,
	int,
	string,
	int,
) ([]entity.RAGSearchResult, error) {
	f.calls++

	return []entity.RAGSearchResult{}, nil
}

func TestVerifyUnitSearchP95UsesWarmupsAndBoundedQueries(t *testing.T) {
	t.Parallel()

	repo := &fakeUnitSearchVerifier{}
	samples, p95, err := verifyUnitSearchP95(context.Background(), repo, []int{1, 2})
	require.NoError(t, err)
	assert.Equal(t, 10, samples)
	assert.Equal(t, 12, repo.calls, "one warm-up plus five samples per book")
	assert.GreaterOrEqual(t, p95, float64(0))
}

func TestCitableAuditViolationTotalIncludesEveryK1Gate(t *testing.T) {
	t.Parallel()

	violations := entity.CitableAuditViolations{
		BookGone: 1, SupersededNoSuccessor: 1, ActiveWithSuccessor: 1,
		LineageCycle: 1, HashMismatch: 1, AnchorMalformed: 1, FootnoteParent: 1,
		QuranBinding: 1, QuranInterpretive: 1, InterpretiveSafety: 1,
		RAGProjectionDangling: 1, ApprovedMentionAnchor: 1, MentionUnitDangling: 1,
		MentionBindingMismatch: 1, CrossReferenceAnchor: 1,
	}

	assert.Equal(t, int64(15), citableAuditViolationTotal(violations))
}

func TestAdvisoryLockKeyStableAndDistinct(t *testing.T) {
	t.Parallel()

	a1 := advisoryLockKey("authors-name-search")
	a2 := advisoryLockKey("authors-name-search")
	b := advisoryLockKey("another-job")

	assert.Equal(t, a1, a2, "lock key must be stable across runs")
	assert.NotEqual(t, a1, b, "different jobs must not share a lock key")
}

func TestByName(t *testing.T) {
	t.Parallel()

	job, err := ByName("authors-name-search")
	require.NoError(t, err)
	assert.Equal(t, "authors-name-search", job.Name())

	versionJob, err := ByName(authorsNameSearchVersionJobName)
	require.NoError(t, err)
	assert.Equal(t, 1, versionJob.ProfileVersion())

	quranVersionJob, err := ByName(quranReferenceNormalizationVersionJobName)
	require.NoError(t, err)
	assert.Equal(t, 1, quranVersionJob.ProfileVersion())

	_, err = ByName("nope")
	require.ErrorIs(t, err, ErrJobUnknown)
}

func TestCrossReferencesQuranBridgeJobRegistered(t *testing.T) {
	t.Parallel()

	job, err := ByName("cross-references-quran-bridge")
	require.NoError(t, err)
	assert.Equal(t, "cross-references-quran-bridge", job.Name())
	assert.Equal(t, 1, job.ProfileVersion())

	freezeJob, err := ByName("cross-references-quran-freeze")
	require.NoError(t, err)
	assert.Equal(t, "cross-references-quran-freeze", freezeJob.Name())
	assert.Zero(t, freezeJob.ProfileVersion())

	unfreezeJob, err := ByName("cross-references-quran-unfreeze")
	require.NoError(t, err)
	assert.Equal(t, "cross-references-quran-unfreeze", unfreezeJob.Name())
	assert.Zero(t, unfreezeJob.ProfileVersion())
}

func TestQuranPageNavigationJobRegistered(t *testing.T) {
	t.Parallel()

	job, err := ByName(quranPageNavigationJobName)
	require.NoError(t, err)
	assert.Equal(t, quranPageNavigationJobName, job.Name())
	assert.Equal(t, 1, job.ProfileVersion())
}
