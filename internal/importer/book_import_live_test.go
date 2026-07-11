package importer

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This suite pins the SAFE re-import contract for the Shamela book importer
// (roadmap E4 / K-0 defect D1): re-import may never hard-delete content —
// disappearing rows are staged as a reviewable diff and only become soft
// tombstones after explicit approval, so editorial work and user data survive
// by construction. Written BEFORE the behavior change (D6: this path had zero
// tests while it was hard-deleting).

func bookImportUserID(bookID int) string {
	return fmt.Sprintf("11111111-2222-3333-4444-%012d", bookID)
}

func liveBookImportPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pool, err := pgxpool.New(context.Background(), url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	return pool
}

// resetBookImportState removes everything a previous (possibly crashed) run of
// this suite left behind for the given book: the book row (content + editorial
// cascade), the suite user (saved_items/reading_progress cascade), and import
// provenance for the given release prefix.
func resetBookImportState(t *testing.T, pool *pgxpool.Pool, bookID int, releasePrefix string) {
	t.Helper()

	ctx := context.Background()
	cleanup := func() {
		_, err := pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, bookImportUserID(bookID))
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `DELETE FROM import_runs WHERE release_key LIKE $1`, releasePrefix+"%")
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `DELETE FROM source_releases WHERE release_key LIKE $1`, releasePrefix+"%")
		require.NoError(t, err)
	}

	cleanup()
	t.Cleanup(cleanup)
}

func runBookImport(t *testing.T, sourceDir, releaseKey string, bookID int, approveRun string) (Stats, error) {
	t.Helper()

	return Run(context.Background(), Options{
		SourceDir:          sourceDir,
		PostgresURL:        os.Getenv("SURAU_LIVE_PG"),
		ReleaseKey:         releaseKey,
		BookIDs:            []int{bookID},
		ApproveRemovalsRun: approveRun,
	})
}

type rowState struct {
	exists    bool
	isDeleted bool
	deletedAt *time.Time
}

func pageState(t *testing.T, pool *pgxpool.Pool, bookID, pageID int) rowState {
	t.Helper()

	var s rowState

	err := pool.QueryRow(context.Background(),
		`SELECT true, is_deleted, deleted_at FROM book_pages WHERE book_id = $1 AND page_id = $2`,
		bookID, pageID).Scan(&s.exists, &s.isDeleted, &s.deletedAt)
	if err != nil {
		return rowState{}
	}

	return s
}

func headingState(t *testing.T, pool *pgxpool.Pool, bookID, headingID int) rowState {
	t.Helper()

	var s rowState

	err := pool.QueryRow(context.Background(),
		`SELECT true, is_deleted, deleted_at FROM book_headings WHERE book_id = $1 AND heading_id = $2`,
		bookID, headingID).Scan(&s.exists, &s.isDeleted, &s.deletedAt)
	if err != nil {
		return rowState{}
	}

	return s
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()

	var n int
	require.NoError(t, pool.QueryRow(context.Background(), query, args...).Scan(&n))

	return n
}

// seedEditorialAndUserData plants the work products that defect D1 used to
// destroy: page/heading editorial, translations, summaries, plus user rows
// pointing at the page/heading that later disappears from the source.
func seedEditorialAndUserData(t *testing.T, pool *pgxpool.Pool, bookID, pageID, headingID int) {
	t.Helper()

	ctx := context.Background()

	_, err := pool.Exec(ctx, `
INSERT INTO users (id, username, email, password_hash)
VALUES ($1, $2, $3, 'x')
ON CONFLICT (id) DO NOTHING`,
		bookImportUserID(bookID),
		fmt.Sprintf("book-import-suite-%d", bookID),
		fmt.Sprintf("book-import-suite-%d@test.local", bookID))
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO book_page_edits (book_id, page_id, status, content_html, content_text)
VALUES ($1, $2, 'draft', '<p>edited</p>', 'edited')`, bookID, pageID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO section_translations (
    book_id, heading_id, lang, title, content, provenance_class
)
VALUES ($1, $2, 'id', 'judul', 'terjemahan editorial', 'editorial')`, bookID, headingID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO book_heading_summaries (
    book_id, heading_id, lang, summary, provenance_class
)
VALUES ($1, $2, 'id', 'ringkasan editorial', 'editorial')`, bookID, headingID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO saved_items (id, user_id, item_type, book_id, page_id)
VALUES ($1, $2, 'book_page', $3, $4)`, uuid.New().String(), bookImportUserID(bookID), bookID, pageID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
INSERT INTO reading_progress (user_id, book_id, page_id, heading_id, progress_percent)
VALUES ($1, $2, $3, $4, 42.0)`, bookImportUserID(bookID), bookID, pageID, headingID)
	require.NoError(t, err)
}

func editorialAndUserCounts(t *testing.T, pool *pgxpool.Pool, bookID int) map[string]int {
	t.Helper()

	return map[string]int{
		"book_page_edits":        countRows(t, pool, `SELECT count(*) FROM book_page_edits WHERE book_id = $1`, bookID),
		"section_translations":   countRows(t, pool, `SELECT count(*) FROM section_translations WHERE book_id = $1`, bookID),
		"book_heading_summaries": countRows(t, pool, `SELECT count(*) FROM book_heading_summaries WHERE book_id = $1`, bookID),
		"saved_items":            countRows(t, pool, `SELECT count(*) FROM saved_items WHERE book_id = $1`, bookID),
		"reading_progress":       countRows(t, pool, `SELECT count(*) FROM reading_progress WHERE book_id = $1`, bookID),
	}
}

func fixtureV1(bookID int) fixtureBook {
	return fixtureBook{
		ID:   bookID,
		Name: "Kitab Uji",
		Pages: []fixturePage{
			{ID: 1, Content: "<p>page one</p>", Part: "1", Page: "1", Number: "1"},
			{ID: 2, Content: "<p>page two</p>", Part: "1", Page: "2", Number: "2"},
			{ID: 3, Content: "<p>page three</p>", Part: "1", Page: "3", Number: "3"},
		},
		Headings: []fixtureHeading{
			{ID: 1, Content: "Bab Satu", PageID: 1},
			{ID: 2, Content: "Bab Dua", PageID: 3, ParentID: 1},
		},
	}
}

// TestLiveBookImportInitial — T1: first import lands live content and run provenance.
func TestLiveBookImportInitial(t *testing.T) {
	pool := liveBookImportPool(t)

	t.Parallel()

	bookID := 9001
	resetBookImportState(t, pool, bookID, "bi-initial")

	dir := t.TempDir()
	writeBookSource(t, dir, fixtureV1(bookID))

	stats, err := runBookImport(t, dir, "bi-initial-v1", bookID, "")
	require.NoError(t, err)
	assert.Equal(t, 1, stats.ImportedBooks)
	assert.Equal(t, 3, countRows(t, pool, `SELECT count(*) FROM book_pages WHERE book_id = $1 AND is_deleted = false`, bookID))
	assert.Equal(t, 2, countRows(t, pool, `SELECT count(*) FROM book_headings WHERE book_id = $1 AND is_deleted = false`, bookID))
	assert.Equal(t, 1, countRows(t, pool, `SELECT count(*) FROM books WHERE id = $1 AND has_content = true`, bookID))
	assert.Equal(t, "success", queryRunStatus(t, pool, stats.RunID))
}

func queryRunStatus(t *testing.T, pool *pgxpool.Pool, runID string) string {
	t.Helper()

	var status string
	require.NoError(t, pool.QueryRow(context.Background(), `SELECT status FROM import_runs WHERE id = $1`, runID).Scan(&status))

	return status
}

// TestLiveBookImportIdenticalNoOp — T2: identical re-import writes nothing.
func TestLiveBookImportIdenticalNoOp(t *testing.T) {
	pool := liveBookImportPool(t)

	t.Parallel()

	bookID := 9002
	resetBookImportState(t, pool, bookID, "bi-noop")

	dir := t.TempDir()
	writeBookSource(t, dir, fixtureV1(bookID))

	_, err := runBookImport(t, dir, "bi-noop-v1", bookID, "")
	require.NoError(t, err)

	before := snapshotUpdatedAt(t, pool, bookID)

	stats, err := runBookImport(t, dir, "bi-noop-v2", bookID, "")
	require.NoError(t, err)
	assert.Zero(t, stats.StagedRemovalPages, "identical source must stage nothing")
	assert.Zero(t, stats.StagedRemovalHeadings)
	assert.Equal(t, before, snapshotUpdatedAt(t, pool, bookID), "identical re-import must not rewrite any row")
	assert.Zero(t, countRows(t, pool, `
SELECT count(*) FROM book_import_removal_stages s
JOIN import_runs r ON r.id = s.run_id WHERE r.release_key LIKE 'bi-noop%'`))
}

func snapshotUpdatedAt(t *testing.T, pool *pgxpool.Pool, bookID int) map[string]time.Time {
	t.Helper()

	snap := make(map[string]time.Time)
	rows, err := pool.Query(context.Background(), `
SELECT 'p:' || page_id::text, updated_at FROM book_pages WHERE book_id = $1
UNION ALL
SELECT 'h:' || heading_id::text, updated_at FROM book_headings WHERE book_id = $1`, bookID)
	require.NoError(t, err)

	defer rows.Close()

	for rows.Next() {
		var key string

		var ts time.Time

		require.NoError(t, rows.Scan(&key, &ts))
		snap[key] = ts
	}

	require.NoError(t, rows.Err())

	return snap
}

// TestLiveBookImportRemovalFlow — T3+T4+T5+T7: source drops a page+heading;
// default run only STAGES the removal (nothing disappears), approval turns it
// into soft tombstones (editorial and user rows survive — the D1 fixture),
// and a source that restores the page revives it with editorial intact.
func TestLiveBookImportRemovalFlow(t *testing.T) {
	pool := liveBookImportPool(t)

	t.Parallel()

	bookID := 9003
	resetBookImportState(t, pool, bookID, "bi-flow")

	v1 := t.TempDir()
	writeBookSource(t, v1, fixtureV1(bookID))
	_, err := runBookImport(t, v1, "bi-flow-v1", bookID, "")
	require.NoError(t, err)

	seedEditorialAndUserData(t, pool, bookID, 3, 2)
	baseline := editorialAndUserCounts(t, pool, bookID)

	// v2 drops page 3 and heading 2 (the ones carrying editorial + user data).
	v2 := t.TempDir()
	v2Book := fixtureV1(bookID)
	v2Book.Pages = v2Book.Pages[:2]
	v2Book.Headings = v2Book.Headings[:1]
	writeBookSource(t, v2, v2Book)

	// T3 — default (stage) mode: nothing may disappear.
	stageStats, err := runBookImport(t, v2, "bi-flow-v2", bookID, "")
	require.NoError(t, err)
	assert.Equal(t, 1, stageStats.StagedRemovalPages)
	assert.Equal(t, 1, stageStats.StagedRemovalHeadings)

	require.True(t, pageState(t, pool, bookID, 3).exists, "stage mode must not delete pages")
	assert.False(t, pageState(t, pool, bookID, 3).isDeleted, "stage mode must not tombstone pages")
	require.True(t, headingState(t, pool, bookID, 2).exists)
	assert.False(t, headingState(t, pool, bookID, 2).isDeleted)
	assert.Equal(t, baseline, editorialAndUserCounts(t, pool, bookID), "stage mode must leave editorial and user data untouched")

	var stagedPages, stagedHeadings []int32
	require.NoError(t, pool.QueryRow(context.Background(), `
SELECT page_ids, heading_ids FROM book_import_removal_stages WHERE run_id = $1 AND book_id = $2`,
		stageStats.RunID, bookID).Scan(&stagedPages, &stagedHeadings))
	assert.Equal(t, []int32{3}, stagedPages)
	assert.Equal(t, []int32{2}, stagedHeadings)

	// T4 — approval applies SOFT tombstones, never DELETE: cascade counts prove it.
	approveStats, err := runBookImport(t, v2, "bi-flow-v3", bookID, stageStats.RunID)
	require.NoError(t, err)
	assert.Equal(t, 1, approveStats.TombstonedPages)
	assert.Equal(t, 1, approveStats.TombstonedHeadings)

	page3 := pageState(t, pool, bookID, 3)
	require.True(t, page3.exists, "tombstone must keep the row")
	assert.True(t, page3.isDeleted)
	require.NotNil(t, page3.deletedAt)

	heading2 := headingState(t, pool, bookID, 2)
	require.True(t, heading2.exists)
	assert.True(t, heading2.isDeleted)

	// The whole point of E4: editorial + user data survive approval untouched.
	assert.Equal(t, baseline, editorialAndUserCounts(t, pool, bookID), "approval must not cascade into editorial/user tables")

	// T5 — user rows do not dangle: their target still resolves (as a tombstone).
	assert.Equal(t, 1, countRows(t, pool, `
SELECT count(*) FROM saved_items si
JOIN book_pages bp ON bp.book_id = si.book_id AND bp.page_id = si.page_id
WHERE si.book_id = $1`, bookID))

	// Reader-facing live filter hides the tombstoned page.
	assert.Zero(t, countRows(t, pool,
		`SELECT count(*) FROM book_pages WHERE book_id = $1 AND page_id = 3 AND is_deleted = false`, bookID))

	// T7 — the source restores page 3 + heading 2: rows revive, editorial resurfaces.
	_, err = runBookImport(t, v1, "bi-flow-v4", bookID, "")
	require.NoError(t, err)

	revived := pageState(t, pool, bookID, 3)
	assert.False(t, revived.isDeleted, "reappearing source row must clear the tombstone")
	assert.Nil(t, revived.deletedAt)
	assert.False(t, headingState(t, pool, bookID, 2).isDeleted)
	assert.False(t, headingState(t, pool, bookID, 1).isDeleted, "untouched heading must stay live through the whole flow")
	assert.Equal(t, 1, countRows(t, pool, `
SELECT count(*) FROM section_translations st
JOIN book_headings h ON h.book_id = st.book_id AND h.heading_id = st.heading_id AND h.is_deleted = false
WHERE st.book_id = $1 AND st.heading_id = 2`, bookID), "editorial must resurface with the revived heading")
}

// TestLiveBookImportApproveDrift — T6: approval against a source that drifted
// from the staged diff must abort without touching anything.
func TestLiveBookImportApproveDrift(t *testing.T) {
	pool := liveBookImportPool(t)

	t.Parallel()

	bookID := 9004
	resetBookImportState(t, pool, bookID, "bi-drift")

	v1 := t.TempDir()
	writeBookSource(t, v1, fixtureV1(bookID))
	_, err := runBookImport(t, v1, "bi-drift-v1", bookID, "")
	require.NoError(t, err)

	v2 := t.TempDir()
	v2Book := fixtureV1(bookID)
	v2Book.Pages = v2Book.Pages[:2] // drop page 3
	v2Book.Headings = v2Book.Headings[:1]
	writeBookSource(t, v2, v2Book)

	stageStats, err := runBookImport(t, v2, "bi-drift-v2", bookID, "")
	require.NoError(t, err)

	v3 := t.TempDir()
	v3Book := fixtureV1(bookID)
	v3Book.Pages = v3Book.Pages[:1] // drops pages 2 AND 3 — more than was staged
	v3Book.Headings = nil
	writeBookSource(t, v3, v3Book)

	_, err = runBookImport(t, v3, "bi-drift-v3", bookID, stageStats.RunID)
	require.ErrorIs(t, err, ErrRemovalDrift)

	assert.False(t, pageState(t, pool, bookID, 2).isDeleted, "drift abort must not tombstone anything")
	assert.False(t, pageState(t, pool, bookID, 3).isDeleted)
}

// TestLiveBookImportSourceDeletedFlag — T8: a source row flagged is_deleted is a
// removal candidate (stage → approve), never an implicit delete.
func TestLiveBookImportSourceDeletedFlag(t *testing.T) {
	pool := liveBookImportPool(t)

	t.Parallel()

	bookID := 9005
	resetBookImportState(t, pool, bookID, "bi-srcdel")

	v1 := t.TempDir()
	writeBookSource(t, v1, fixtureV1(bookID))
	_, err := runBookImport(t, v1, "bi-srcdel-v1", bookID, "")
	require.NoError(t, err)

	v2 := t.TempDir()
	v2Book := fixtureV1(bookID)
	v2Book.Pages[2].IsDeleted = true
	v2Book.Headings = v2Book.Headings[:1]
	writeBookSource(t, v2, v2Book)

	stageStats, err := runBookImport(t, v2, "bi-srcdel-v2", bookID, "")
	require.NoError(t, err)
	assert.Equal(t, 1, stageStats.StagedRemovalPages, "source-flagged deletion must be staged, not applied")
	assert.False(t, pageState(t, pool, bookID, 3).isDeleted)

	_, err = runBookImport(t, v2, "bi-srcdel-v3", bookID, stageStats.RunID)
	require.NoError(t, err)
	assert.True(t, pageState(t, pool, bookID, 3).isDeleted)
	assert.True(t, headingState(t, pool, bookID, 2).isDeleted, "heading on the source-deleted page follows it into the tombstone")
}
