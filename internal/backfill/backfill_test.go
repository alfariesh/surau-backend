package backfill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValuesUpdateSQL(t *testing.T) {
	t.Parallel()

	sqlText, args := valuesUpdateSQL("authors", "name_search", []int64{7, 9}, []string{"a", "b"})

	assert.Equal(
		t,
		"UPDATE authors AS t SET name_search = v.value "+
			"FROM (VALUES (($1)::bigint, ($2)::text), (($3)::bigint, ($4)::text)) AS v(id, value) "+
			"WHERE t.id = v.id",
		sqlText,
	)
	assert.Equal(t, []any{int64(7), "a", int64(9), "b"}, args)
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

	_, err = ByName("nope")
	require.ErrorIs(t, err, ErrJobUnknown)
}
