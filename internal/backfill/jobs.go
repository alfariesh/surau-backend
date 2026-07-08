package backfill

import (
	"context"
	"fmt"
	"strings"

	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Jobs returns every registered backfill job. New backfills (e.g. the B-1
// Citable Unit pilot) register here so the CLI, the metrics collector, and
// the playbook checklist all see them.
func Jobs() []Job {
	return []Job{authorsNameSearchJob{}}
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
	rows, err := pool.Query(ctx, `
SELECT id, name
FROM authors
WHERE id > $1 AND name_search IS NULL
ORDER BY id
LIMIT $2`, cursor, limit)
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

	sqlText, args := valuesUpdateSQL("authors", "name_search", ids, normalized)

	tag, err := pool.Exec(ctx, sqlText, args...)
	if err != nil {
		return cursor, 0, false, fmt.Errorf("authors-name-search: update chunk: %w", err)
	}

	// Deliberately no updated_at bump: the column is derived; mass-churning
	// row timestamps would lie to consumers that treat them as content
	// changes.
	return ids[len(ids)-1], tag.RowsAffected(), len(ids) < limit, nil
}

// argsPerRow: each VALUES row binds (id, value).
const argsPerRow = 2

// valuesUpdateSQL builds one batched UPDATE ... FROM (VALUES ...) statement
// (UPDATE has no ORDER BY/LIMIT in Postgres, so chunking happens in the
// SELECT and the write is a single VALUES join). Explicit casts keep
// parameter types unambiguous inside VALUES.
func valuesUpdateSQL(table, column string, ids []int64, values []string) (sqlText string, args []any) {
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

	sqlText = fmt.Sprintf(
		"UPDATE %s AS t SET %s = v.value FROM (VALUES %s) AS v(id, value) WHERE t.id = v.id",
		table, column, builder.String(),
	)

	return sqlText, args
}
