package backfill

import (
	"context"
	"fmt"
	"strings"

	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Jobs returns every registered backfill job. New backfills register here so
// the CLI, the metrics collector, and the playbook checklist all see them.
func Jobs() []Job {
	return []Job{
		authorsNameSearchJob{},
		authorsNameSearchVersionJob{},
		quranReferenceNormalizationVersionJob{},
		citableUnitsPilotJob{},
		citableUnitsRederiveJob{},
		&citableUnitsCatalogJob{},
		&citableUnitsCatalogJob{rederive: true},
		quranPageNavigationJob{},
		quranCitableUnitsJob{},
		quranCitableUnitsRederiveJob{},
		crossReferencesQuranBridgeJob{},
		crossReferencesQuranFreezeJob{},
		crossReferencesQuranUnfreezeJob{},
	}
}

// ByName resolves one registered job.
func ByName(name string) (Job, error) {
	for _, job := range Jobs() {
		if job.Name() == name {
			return job, nil
		}
	}

	return nil, fmt.Errorf("%w: %q", ErrJobUnknown, name)
}

// authorsNameSearchJob fills authors.name_search with the canonical
// normalized author name (hamza-insensitive search, F1-H first real
// backfill). Idempotent: only rows with name_search IS NULL are touched, in
// id order, so an int64 cursor resumes it exactly.
type authorsNameSearchJob struct{}

func (authorsNameSearchJob) Name() string { return "authors-name-search" }

func (authorsNameSearchJob) ProfileVersion() int { return searchtext.ProfileVersion }

func (authorsNameSearchJob) CountRemaining(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var remaining int64

	err := pool.QueryRow(ctx, `SELECT count(*) FROM authors WHERE name_search IS NULL`).Scan(&remaining)
	if err != nil {
		return 0, fmt.Errorf("authors-name-search: count remaining: %w", err)
	}

	return remaining, nil
}

func (authorsNameSearchJob) ProcessChunk(
	ctx context.Context,
	pool *pgxpool.Pool,
	cursor int64,
	limit int,
) (newCursor, processed int64, done bool, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("authors-name-search: begin chunk: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Keep the source name locked through the versioned write so an importer
	// cannot make the normalized value stale between SELECT and UPDATE.
	rows, err := tx.Query(ctx, `
SELECT id, name
FROM authors
WHERE id > $1 AND name_search IS NULL
ORDER BY id
LIMIT $2
FOR UPDATE`, cursor, limit)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("authors-name-search: select chunk: %w", err)
	}

	ids := make([]int64, 0, limit)
	normalized := make([]string, 0, limit)

	for rows.Next() {
		var (
			id   int64
			name string
		)

		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()

			return cursor, 0, false, fmt.Errorf("authors-name-search: scan: %w", err)
		}

		ids = append(ids, id)
		normalized = append(normalized, searchtext.Normalize(name))
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		return cursor, 0, false, fmt.Errorf("authors-name-search: rows: %w", err)
	}

	if len(ids) == 0 {
		return cursor, 0, true, nil
	}

	processed, err = writeAuthorSearchVersionChunk(
		ctx,
		tx,
		"authors-name-search",
		ids,
		normalized,
		searchtext.ProfileVersion,
	)
	if err != nil {
		return cursor, 0, false, err
	}

	// Deliberately no updated_at bump: the column is derived; mass-churning
	// row timestamps would lie to consumers that treat them as content
	// changes.
	return ids[len(ids)-1], processed, len(ids) < limit, nil
}

func writeAuthorSearchVersionChunk(
	ctx context.Context,
	tx pgx.Tx,
	jobName string,
	ids []int64,
	normalized []string,
	profileVersion int,
) (int64, error) {
	sqlText, args := versionedValuesUpdateSQL(
		"authors",
		"name_search",
		"name_search_normalization_version",
		ids,
		normalized,
		profileVersion,
	)

	tag, err := tx.Exec(ctx, sqlText, args...)
	if err != nil {
		return 0, fmt.Errorf("%s: update chunk: %w", jobName, err)
	}

	if tag.RowsAffected() != int64(len(ids)) {
		return 0, fmt.Errorf(
			"%w: %s affected %d of %d locked rows",
			errAuthorChunkWriteConflict,
			jobName,
			tag.RowsAffected(),
			len(ids),
		)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("%s: commit chunk: %w", jobName, err)
	}

	return tag.RowsAffected(), nil
}

// argsPerRow: each VALUES row binds (id, value).
const argsPerRow = 2

// versionedValuesUpdateSQL builds one batched UPDATE ... FROM (VALUES ...)
// statement that stamps text and its normalization version atomically.
// (UPDATE has no ORDER BY/LIMIT in Postgres, so chunking happens in the
// SELECT and the write is a single VALUES join). Explicit casts keep
// parameter types unambiguous inside VALUES.
func versionedValuesUpdateSQL(
	table, column, versionColumn string,
	ids []int64,
	values []string,
	profileVersion int,
) (sqlText string, args []any) {
	var builder strings.Builder

	args = make([]any, 0, len(ids)*argsPerRow)

	for i := range ids {
		if i > 0 {
			builder.WriteString(", ")
		}

		idIdx := len(args) + 1
		valueIdx := idIdx + 1

		fmt.Fprintf(&builder, "(($%d)::bigint, ($%d)::text)", idIdx, valueIdx)

		args = append(args, ids[i], values[i])
	}

	versionArg := len(args) + 1
	args = append(args, profileVersion)

	sqlText = fmt.Sprintf(
		"UPDATE %s AS t SET %s = v.value, %s = ($%d)::integer "+
			"FROM (VALUES %s) AS v(id, value) WHERE t.id = v.id AND t.%s IS NULL",
		table, column, versionColumn, versionArg, builder.String(), versionColumn,
	)

	return sqlText, args
}
