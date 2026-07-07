package persistent

import (
	"context"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
)

// syncSinceOverlap widens the requested since window so rows committed
// shortly after a previous snapshot's reads are never skipped. Sync delivery
// is therefore at-least-once; clients upsert idempotently by key.
const syncSinceOverlap = time.Minute

// maxSyncSavedItemIDs caps the deletion-reconciliation ID list. Beyond this
// the snapshot sets SavedItemsFullResync instead of returning an unbounded
// payload, and the client rebuilds via paging GET /me/saved-items.
const maxSyncSavedItemIDs = 10000

// SyncSnapshot returns the user's personal reader state changed since the
// given time (or everything when since is nil). All reads share one
// read-only repeatable-read transaction so the payload and its server_time
// cursor are mutually consistent.
func (r *PersonalRepo) SyncSnapshot(
	ctx context.Context,
	userID string,
	since *time.Time,
) (entity.PersonalSyncSnapshot, error) {
	snapshot := entity.PersonalSyncSnapshot{
		Since:           since,
		ReadingProgress: []entity.ReadingProgress{},
		QuranProgress:   []entity.QuranReadingProgress{},
		SavedItems:      []entity.SavedItem{},
		SavedItemIDs:    []string{},
		KhatamCycles:    []entity.QuranKhatamCycle{},
	}

	var effectiveSince *time.Time

	if since != nil {
		overlapped := since.Add(-syncSinceOverlap)
		effectiveSince = &overlapped
	}

	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return entity.PersonalSyncSnapshot{}, fmt.Errorf("PersonalRepo - SyncSnapshot - BeginTx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := tx.QueryRow(ctx, "SELECT now()").Scan(&snapshot.ServerTime); err != nil {
		return entity.PersonalSyncSnapshot{}, fmt.Errorf("PersonalRepo - SyncSnapshot - server time: %w", err)
	}

	if snapshot.ReadingProgress, err = r.syncReadingProgress(ctx, tx, userID, effectiveSince); err != nil {
		return entity.PersonalSyncSnapshot{}, err
	}

	if snapshot.QuranProgress, err = r.syncQuranProgress(ctx, tx, userID, effectiveSince); err != nil {
		return entity.PersonalSyncSnapshot{}, err
	}

	if snapshot.SavedItems, err = r.syncSavedItems(ctx, tx, userID, effectiveSince); err != nil {
		return entity.PersonalSyncSnapshot{}, err
	}

	if snapshot.SavedItemIDs, snapshot.SavedItemsFullResync, err = r.syncSavedItemIDs(ctx, tx, userID); err != nil {
		return entity.PersonalSyncSnapshot{}, err
	}

	if snapshot.KhatamCycles, err = r.syncKhatamCycles(ctx, tx, userID, effectiveSince); err != nil {
		return entity.PersonalSyncSnapshot{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.PersonalSyncSnapshot{}, fmt.Errorf("PersonalRepo - SyncSnapshot - Commit: %w", err)
	}

	return snapshot, nil
}

//nolint:dupl // parallel structure to syncQuranProgress over a different row type
func (r *PersonalRepo) syncReadingProgress(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	since *time.Time,
) ([]entity.ReadingProgress, error) {
	builder := r.progressSelectBuilder().
		Where(sq.Eq{"user_id": userID}).
		OrderBy("updated_at DESC", "book_id ASC")
	if since != nil {
		builder = builder.Where(sq.GtOrEq{"updated_at": *since})
	}

	sqlText, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncReadingProgress - r.Builder: %w", err)
	}

	rows, err := tx.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncReadingProgress - tx.Query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.ReadingProgress, 0)

	for rows.Next() {
		progress, err := scanProgress(rows)
		if err != nil {
			return nil, fmt.Errorf("PersonalRepo - syncReadingProgress - scanProgress: %w", err)
		}

		items = append(items, progress)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncReadingProgress - rows.Err: %w", err)
	}

	return items, nil
}

//nolint:dupl // parallel structure to syncReadingProgress over a different row type
func (r *PersonalRepo) syncQuranProgress(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	since *time.Time,
) ([]entity.QuranReadingProgress, error) {
	builder := r.quranProgressSelectBuilder().
		Where(sq.Eq{"qrp.user_id": userID}).
		OrderBy("qrp.updated_at DESC", "qrp.surah_id ASC")
	if since != nil {
		builder = builder.Where(sq.GtOrEq{"qrp.updated_at": *since})
	}

	sqlText, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncQuranProgress - r.Builder: %w", err)
	}

	rows, err := tx.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncQuranProgress - tx.Query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.QuranReadingProgress, 0)

	for rows.Next() {
		progress, err := scanQuranProgress(rows)
		if err != nil {
			return nil, fmt.Errorf("PersonalRepo - syncQuranProgress - scanQuranProgress: %w", err)
		}

		items = append(items, progress)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncQuranProgress - rows.Err: %w", err)
	}

	return items, nil
}

func (r *PersonalRepo) syncSavedItems(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	since *time.Time,
) ([]entity.SavedItem, error) {
	builder := r.savedItemSelectBuilder().
		Where(sq.Eq{"user_id": userID}).
		OrderBy("updated_at DESC", "created_at DESC", "id DESC")
	if since != nil {
		builder = builder.Where(sq.GtOrEq{"updated_at": *since})
	}

	sqlText, args, err := builder.ToSql()
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncSavedItems - r.Builder: %w", err)
	}

	rows, err := tx.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncSavedItems - tx.Query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.SavedItem, 0)

	for rows.Next() {
		item, err := scanSavedItem(rows)
		if err != nil {
			return nil, fmt.Errorf("PersonalRepo - syncSavedItems - scanSavedItem: %w", err)
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncSavedItems - rows.Err: %w", err)
	}

	return items, nil
}

// syncSavedItemIDs returns every saved-item ID for delete reconciliation, or
// (nil, true, nil) when the user holds more than maxSyncSavedItemIDs items
// and the client must full-resync via paging instead.
func (r *PersonalRepo) syncSavedItemIDs(ctx context.Context, tx pgx.Tx, userID string) (ids []string, truncated bool, err error) {
	rows, err := tx.Query(
		ctx,
		`SELECT id FROM saved_items WHERE user_id = $1 ORDER BY id ASC LIMIT $2`,
		userID,
		maxSyncSavedItemIDs+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("PersonalRepo - syncSavedItemIDs - tx.Query: %w", err)
	}
	defer rows.Close()

	ids = make([]string, 0)

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, false, fmt.Errorf("PersonalRepo - syncSavedItemIDs - rows.Scan: %w", err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("PersonalRepo - syncSavedItemIDs - rows.Err: %w", err)
	}

	if len(ids) > maxSyncSavedItemIDs {
		return []string{}, true, nil
	}

	return ids, false, nil
}

func (r *PersonalRepo) syncKhatamCycles(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	since *time.Time,
) ([]entity.QuranKhatamCycle, error) {
	condition := ""
	args := []any{userID}

	if since != nil {
		condition = " AND c.updated_at >= $2"

		args = append(args, *since)
	}

	sqlText := fmt.Sprintf(`
SELECT %s
FROM quran_khatam_cycles c
%s
WHERE c.user_id = $1%s
ORDER BY c.started_at DESC, c.id DESC`, khatamCycleColumns, khatamMarksLateral, condition)

	rows, err := tx.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncKhatamCycles - tx.Query: %w", err)
	}
	defer rows.Close()

	cycles := make([]entity.QuranKhatamCycle, 0)

	for rows.Next() {
		cycle, err := scanKhatamCycle(rows)
		if err != nil {
			return nil, fmt.Errorf("PersonalRepo - syncKhatamCycles - scanKhatamCycle: %w", err)
		}

		cycles = append(cycles, cycle)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - syncKhatamCycles - rows.Err: %w", err)
	}

	return cycles, nil
}
