package importer

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// withQuranEditorialFixtureWriter is test-fixture infrastructure, not a runtime
// write path. Production importer code must always call EditorialRepo instead.
func withQuranEditorialFixtureWriter(
	ctx context.Context,
	pool *pgxpool.Pool,
	fn func(pgx.Tx) error,
) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin Quran editorial fixture: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, `SET LOCAL surau.quran_editorial_writer = 'quran-editorial-service'`); err != nil {
		return fmt.Errorf("mark Quran editorial fixture writer: %w", err)
	}

	if fixtureErr := fn(tx); fixtureErr != nil {
		return fixtureErr
	}

	if commitErr := tx.Commit(ctx); commitErr != nil {
		return fmt.Errorf("commit Quran editorial fixture: %w", commitErr)
	}

	return nil
}
