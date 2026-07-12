package persistent

import (
	"strings"
	"testing"

	sq "github.com/Masterminds/squirrel"
	anchorgrammar "github.com/alfariesh/surau-backend/internal/anchor"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrossReferencePublicIncomingQuranSQLIsBoundedAndApproved(t *testing.T) {
	t.Parallel()

	value, err := anchorgrammar.Parse("quran/73:4")
	require.NoError(t, err)

	pg := &postgres.Postgres{Builder: sqBuilderForTest()}
	builder := pg.Builder.Select("cr.id").From("cross_references cr")
	builder = applyCrossReferenceListFilter(builder, repo.CrossReferenceFilter{
		Anchor:       value.String(),
		Direction:    entity.CrossReferenceDirectionIncoming,
		PublicOnly:   true,
		ReviewStatus: entity.CrossReferenceStatusRejected,
	}, &value)

	sql, args, err := builder.ToSql()
	require.NoError(t, err)
	assert.Contains(t, sql, "target_quran_from_ayah IS NOT NULL")
	assert.Contains(t, sql, "target_quran_to_ayah IS NOT NULL")
	assert.Contains(t, sql, "int4range")
	assert.NotContains(t, sql, "WITH RECURSIVE", "Quran point lookup must not pay the unit-lineage CTE cost")
	assert.Contains(t, sql, "cr.target_anchor =")
	assert.Contains(t, sql, "cross_reference_anchor_visible(cr.source_anchor)")
	assert.Contains(t, sql, "cross_reference_anchor_visible(cr.target_anchor)")
	assert.Contains(t, sql, "JOIN LATERAL")
	assert.Contains(t, sql, "OFFSET 0", "visibility subqueries must remain parameterized for PostgreSQL Memoize")
	assert.Contains(t, sql, "cr.review_status =")
	assert.Contains(t, args, entity.CrossReferenceStatusApproved)
	assert.NotContains(t, args, entity.CrossReferenceStatusRejected)
}

func TestCrossReferenceLineageCTEOnlyForUnitPoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		anchor      string
		wantLineage bool
	}{
		{anchor: "kitab/797/h/11/u/42", wantLineage: true},
		{anchor: "kitab/797", wantLineage: false},
		{anchor: "kitab/797/h/11", wantLineage: false},
		{anchor: "quran/73", wantLineage: false},
		{anchor: "quran/73:4", wantLineage: false},
		{anchor: "quran/73:4/u/2", wantLineage: true},
		{anchor: "kitab/797/h/11/u/42..kitab/797/h/11/u/43", wantLineage: false},
	}

	for _, test := range tests {
		t.Run(test.anchor, func(t *testing.T) {
			t.Parallel()

			value, err := anchorgrammar.Parse(test.anchor)
			require.NoError(t, err)
			assert.Equal(t, test.wantLineage, crossReferenceLineageLookup(&value))

			builder := sqBuilderForTest().Select("cr.id").From("cross_references cr")
			builder = applyCrossReferenceListFilter(builder, repo.CrossReferenceFilter{
				Anchor: test.anchor, Direction: entity.CrossReferenceDirectionIncoming,
			}, &value)
			sql, _, err := builder.ToSql()
			require.NoError(t, err)
			assert.Equal(t, test.wantLineage, strings.Contains(sql, "WITH RECURSIVE"))

			if strings.Contains(test.anchor, "quran/73:4/u/") {
				assert.NotContains(t, sql, "int4range",
					"a Quran child Anchor must match exact identity/lineage, not ayah containment")
			}
		})
	}
}

func TestRequestedAnchorCTEWalksSuccessorsAndAncestorsWithoutUndirectedSiblingClosure(t *testing.T) {
	t.Parallel()

	assert.Contains(t, requestedAnchorCTE, "JOIN citable_unit_lineage l ON l.predecessor_id = w.id")
	assert.Contains(t, requestedAnchorCTE, "JOIN citable_unit_lineage l ON l.successor_id = a.id")
	assert.Contains(t, requestedAnchorCTE, "requested_unit_ancestors")
	assert.NotContains(t, requestedAnchorCTE, "UNION ALL")
}

func TestCrossReferenceWorkKeyCountsImplicitQuranWork(t *testing.T) {
	t.Parallel()

	source := crossReferenceWorkKey("source")
	target := crossReferenceWorkKey("target")

	assert.Contains(t, source, "WHEN cr.source_corpus = 'quran' THEN 'quran'")
	assert.Contains(t, target, "WHEN cr.target_corpus = 'quran' THEN 'quran'")
	assert.Contains(t, source, "source_work_id")
	assert.Contains(t, target, "target_work_id")
}

func TestMapCrossReferenceWriteError(t *testing.T) {
	t.Parallel()

	assert.ErrorIs(t, mapCrossReferenceWriteError(&pgconn.PgError{Code: "23505"}), entity.ErrCrossReferenceConflict)
	assert.ErrorIs(t, mapCrossReferenceWriteError(&pgconn.PgError{Code: "23514"}), entity.ErrInvalidCrossReference)

	plain := entity.ErrForbidden
	assert.ErrorIs(t, mapCrossReferenceWriteError(plain), plain)
}

func sqBuilderForTest() sq.StatementBuilderType {
	return sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
}
