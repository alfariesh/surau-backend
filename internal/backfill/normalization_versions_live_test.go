package backfill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/importer"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	liveNormalizationQuranBookBase = 910_006_000
	liveNormalizationMatrixID      = 910_009_001
)

type liveAuthorChunkResult struct {
	cursor    int64
	processed int64
	done      bool
	err       error
}

type liveQuranStampResult struct {
	stamped int64
	err     error
}

//nolint:paralleltest // serial: intentionally replays DDL on the shared migrated schema
func TestLiveNormalizationMigrationReplayIsIdempotent(t *testing.T) {
	pool := liveBackfillPool(t)
	up, err := os.ReadFile("../../migrations/20260711000001_freeze_arabic_normalization_v1.up.sql")
	require.NoError(t, err)

	for range 2 {
		_, err = pool.Exec(context.Background(), string(up))
		require.NoError(t, err)
	}
}

//nolint:paralleltest // serial: shares the authors version checkpoint and synthetic author id range
func TestLiveBackfillAuthorNormalizationVersionPauseResume(t *testing.T) {
	pool := liveBackfillPool(t)
	resetLiveBackfillState(t, pool)
	t.Cleanup(func() { resetLiveBackfillState(t, pool) })

	ctx := context.Background()
	ids := []int64{
		liveBackfillSeedBase + 2_001,
		liveBackfillSeedBase + 2_002,
		liveBackfillSeedBase + 2_003,
		liveBackfillSeedBase + 2_004,
		liveBackfillSeedBase + 2_005,
	}
	seedLegacyAuthorsWithSearchText(t, pool, ids)

	runCtx, cancelRun := context.WithCancel(ctx)
	runner := &Runner{
		Pool:      pool,
		Log:       logger.New("error"),
		ChunkSize: 2,
		Sleep:     time.Millisecond,
		AfterChunk: func(state State) {
			if state.Status != StatusRunning {
				return
			}

			var stamped int

			err := pool.QueryRow(ctx, `
SELECT count(*) FROM authors
WHERE id = ANY($1) AND name_search_normalization_version = $2`, ids, searchtext.ProfileVersion).
				Scan(&stamped)
			if err == nil && stamped > 0 && stamped < len(ids) {
				cancelRun()
			}
		},
	}

	require.NoError(t, runner.Run(runCtx, authorsNameSearchVersionJob{}, false))
	cancelRun()

	paused := readLiveBackfillStateForJob(t, pool, authorsNameSearchVersionJobName)
	assert.Equal(t, StatusPaused, paused.Status)
	assert.Positive(t, paused.RowsDone)

	var partial int
	require.NoError(t, pool.QueryRow(ctx, `
SELECT count(*) FROM authors
WHERE id = ANY($1) AND name_search_normalization_version = $2`, ids, searchtext.ProfileVersion).
		Scan(&partial))
	assert.Positive(t, partial)
	assert.Less(t, partial, len(ids))

	runner.AfterChunk = nil
	require.NoError(t, runner.Run(ctx, authorsNameSearchVersionJob{}, false))

	completed := readLiveBackfillStateForJob(t, pool, authorsNameSearchVersionJobName)
	assert.Equal(t, StatusCompleted, completed.Status)
	assert.GreaterOrEqual(t, completed.RowsDone, int64(len(ids)))

	for _, id := range ids {
		var (
			name, normalized string
			version          int
		)

		require.NoError(t, pool.QueryRow(ctx, `
SELECT name, name_search, name_search_normalization_version
FROM authors WHERE id = $1`, id).Scan(&name, &normalized, &version))
		assert.Equal(t, searchtext.Normalize(name), normalized)
		assert.Equal(t, searchtext.ProfileVersion, version)
	}
}

//nolint:paralleltest // serial: holds a synthetic author row lock to exercise the backfill race
func TestLiveBackfillAuthorNormalizationVersionDoesNotOverwriteConcurrentWriter(t *testing.T) {
	pool := liveBackfillPool(t)
	resetLiveBackfillState(t, pool)
	t.Cleanup(func() { resetLiveBackfillState(t, pool) })

	const authorID = liveBackfillSeedBase + 2_101
	seedLegacyAuthorsWithSearchText(t, pool, []int64{authorID})

	ctx := context.Background()
	writerTx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = writerTx.Rollback(ctx) }()

	const writerName = "إبراهيم الكاتب الجديد"

	writerSearch := searchtext.Normalize(writerName)
	_, err = writerTx.Exec(ctx, `
UPDATE authors
SET name = $2,
    name_search = $3,
    name_search_normalization_version = $4
WHERE id = $1`, authorID, writerName, writerSearch, searchtext.ProfileVersion)
	require.NoError(t, err)

	resultCh := make(chan liveAuthorChunkResult, 1)

	go func() {
		cursor, processed, done, processErr := (authorsNameSearchVersionJob{}).
			ProcessChunk(ctx, pool, authorID-1, 1)
		resultCh <- liveAuthorChunkResult{cursor: cursor, processed: processed, done: done, err: processErr}
	}()

	waitForLiveAuthorBackfillLock(t, pool, resultCh)

	require.NoError(t, writerTx.Commit(ctx))

	var result liveAuthorChunkResult
	select {
	case result = <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("backfill did not continue after the concurrent writer committed")
	}

	require.NoError(t, result.err)
	assert.Equal(t, authorID-1, result.cursor)
	assert.Zero(t, result.processed)
	assert.True(t, result.done)

	var (
		gotName, gotSearch string
		gotVersion         int
	)

	require.NoError(t, pool.QueryRow(ctx, `
SELECT name, name_search, name_search_normalization_version
FROM authors WHERE id = $1`, authorID).Scan(&gotName, &gotSearch, &gotVersion))
	assert.Equal(t, writerName, gotName)
	assert.Equal(t, writerSearch, gotSearch)
	assert.Equal(t, searchtext.ProfileVersion, gotVersion)
}

func waitForLiveAuthorBackfillLock(
	t *testing.T,
	pool *pgxpool.Pool,
	resultCh <-chan liveAuthorChunkResult,
) {
	t.Helper()

	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case result := <-resultCh:
			t.Fatalf("backfill must wait for the concurrent writer row lock, returned early: %+v", result)
		default:
		}

		var waiting bool

		err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_stat_activity
    WHERE datname = current_database()
      AND pid <> pg_backend_pid()
      AND wait_event_type = 'Lock'
      AND query ILIKE '%authors%'
)`).Scan(&waiting)
		require.NoError(t, err)

		if waiting {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("backfill did not reach the concurrent writer lock")
}

//nolint:paralleltest // serial: shares the Quran normalization checkpoint and guarded legacy tables
func TestLiveBackfillQuranNormalizationVersionPauseResume(t *testing.T) {
	pool := liveBackfillPool(t)
	resetLiveQuranNormalizationVersionState(t, pool)
	t.Cleanup(func() { resetLiveQuranNormalizationVersionState(t, pool) })
	seedLegacyQuranNormalizationRows(t, pool)

	ctx := context.Background()
	runCtx, cancelRun := context.WithCancel(ctx)
	runner := &Runner{
		Pool:      pool,
		Log:       logger.New("error"),
		ChunkSize: 1,
		Sleep:     time.Millisecond,
		AfterChunk: func(state State) {
			if state.Status == StatusRunning {
				cancelRun()
			}
		},
	}

	require.NoError(t, runner.Run(runCtx, quranReferenceNormalizationVersionJob{}, false))
	cancelRun()

	paused := readLiveBackfillStateForJob(t, pool, quranReferenceNormalizationVersionJobName)
	assert.Equal(t, StatusPaused, paused.Status)
	assert.Equal(t, int64(1), paused.RowsDone)

	var stampedWorks int
	require.NoError(t, pool.QueryRow(
		ctx, `
SELECT count(DISTINCT book_id)
FROM quran_book_references
WHERE book_id IN ($1, $2) AND normalization_version = $3`,
		liveNormalizationQuranBookBase,
		liveNormalizationQuranBookBase+1,
		searchtext.ProfileVersion,
	).Scan(&stampedWorks))
	assert.Equal(t, 1, stampedWorks)

	runner.AfterChunk = nil
	require.NoError(t, runner.Run(ctx, quranReferenceNormalizationVersionJob{}, false))
	completed := readLiveBackfillStateForJob(t, pool, quranReferenceNormalizationVersionJobName)
	assert.Equal(t, StatusCompleted, completed.Status)
	assert.Equal(t, int64(2), completed.RowsDone)

	var unversioned int
	require.NoError(t, pool.QueryRow(
		ctx, `
SELECT
    (SELECT count(*) FROM quran_book_references
     WHERE book_id IN ($1, $2) AND normalization_version IS DISTINCT FROM $3)
  + (SELECT count(*) FROM quran_cross_reference_bridge
     WHERE book_id IN ($1, $2) AND normalization_version IS DISTINCT FROM $3)`,
		liveNormalizationQuranBookBase,
		liveNormalizationQuranBookBase+1,
		searchtext.ProfileVersion,
	).Scan(&unversioned))
	assert.Zero(t, unversioned)
}

//nolint:paralleltest // serial: holds a guarded Quran row lock to exercise verification/stamp atomicity
func TestLiveQuranNormalizationV1StampWaitsForConcurrentSourceWriter(t *testing.T) {
	pool := liveBackfillPool(t)
	resetLiveQuranNormalizationVersionState(t, pool)
	t.Cleanup(func() { resetLiveQuranNormalizationVersionState(t, pool) })
	seedLegacyQuranNormalizationRows(t, pool)

	ctx := context.Background()
	writerTx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = writerTx.Rollback(ctx) }()

	_, err = writerTx.Exec(ctx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	require.NoError(t, err)
	_, err = writerTx.Exec(ctx, `
UPDATE quran_book_references
SET source_text = 'مصدر تغير أثناء التحقق'
WHERE book_id = $1`, liveNormalizationQuranBookBase)
	require.NoError(t, err)

	resultCh := make(chan liveQuranStampResult, 1)

	go func() {
		stamped, stampErr := importer.StampQuranReferenceNormalizationV1ForBook(
			ctx,
			pool,
			liveNormalizationQuranBookBase,
		)
		resultCh <- liveQuranStampResult{stamped: stamped, err: stampErr}
	}()

	waitForLiveQuranBackfillLock(t, pool, resultCh)
	require.NoError(t, writerTx.Commit(ctx))

	var result liveQuranStampResult
	select {
	case result = <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("Quran normalization stamp did not continue after the writer committed")
	}

	require.Error(t, result.err)
	require.ErrorContains(t, result.err, "does not match search-key v1")
	assert.Zero(t, result.stamped)

	var stampedRows int
	require.NoError(t, pool.QueryRow(
		ctx, `
SELECT
    (SELECT count(*) FROM quran_book_references
     WHERE book_id = $1 AND normalization_version = $2)
  + (SELECT count(*) FROM quran_cross_reference_bridge
     WHERE book_id = $1 AND normalization_version = $2)`,
		liveNormalizationQuranBookBase,
		quranutil.SearchKeyV1ProfileVersion,
	).Scan(&stampedRows))
	assert.Zero(t, stampedRows, "a concurrent source change must roll back every v1 stamp")
}

func waitForLiveQuranBackfillLock(
	t *testing.T,
	pool *pgxpool.Pool,
	resultCh <-chan liveQuranStampResult,
) {
	t.Helper()

	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case result := <-resultCh:
			t.Fatalf("Quran stamp must wait for the source row lock, returned early: %+v", result)
		default:
		}

		var waiting bool

		err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_stat_activity
    WHERE datname = current_database()
      AND pid <> pg_backend_pid()
      AND wait_event_type = 'Lock'
      AND query ILIKE '%quran_book_references%'
)`).Scan(&waiting)
		require.NoError(t, err)

		if waiting {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("Quran normalization stamp did not reach the concurrent writer lock")
}

//nolint:paralleltest // one transaction exercises the same trigger contract on all six actual tables
func TestLiveNormalizationVersionTriggerMatrix(t *testing.T) {
	pool := liveBackfillPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	require.NoError(t, err)
	seedLiveNormalizationMatrixLegacyRows(t, tx)
	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = origin`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	require.NoError(t, err)

	cases := []struct {
		name      string
		insertSQL string
		changeSQL string
		pairSQL   string
		otherSQL  string
	}{
		{
			name: "authors",
			insertSQL: `INSERT INTO authors (id, name, name_search)
SELECT id + 100, name, name_search FROM authors WHERE id = 910009001`,
			changeSQL: `UPDATE authors SET name_search = name_search || ' changed' WHERE id = 910009001`,
			pairSQL:   `UPDATE authors SET name_search = NULL, name_search_normalization_version = 1 WHERE id = 910009001`,
			otherSQL:  `UPDATE authors SET is_deleted = is_deleted WHERE id = 910009001`,
		},
		{
			name: "quran_book_references",
			insertSQL: `INSERT INTO quran_book_references (
    id, book_id, page_id, source_text, normalized_text, reference_kind,
    match_strategy, review_status, metadata
)
SELECT '00000000-0000-4000-8000-00000000b511'::uuid, book_id, page_id,
       source_text, normalized_text, reference_kind, match_strategy, review_status, metadata
FROM quran_book_references WHERE id = '00000000-0000-4000-8000-00000000b501'::uuid`,
			changeSQL: `UPDATE quran_book_references SET normalized_text = normalized_text || ' changed'
WHERE id = '00000000-0000-4000-8000-00000000b501'::uuid`,
			pairSQL: `UPDATE quran_book_references SET normalized_text = NULL, normalization_version = 1
WHERE id = '00000000-0000-4000-8000-00000000b501'::uuid`,
			otherSQL: `UPDATE quran_book_references SET updated_at = updated_at
WHERE id = '00000000-0000-4000-8000-00000000b501'::uuid`,
		},
		{
			name: "quran_cross_reference_bridge",
			insertSQL: `INSERT INTO quran_cross_reference_bridge (
    cross_reference_id, book_id, page_id, source_text, normalized_text,
    reference_kind, match_strategy, metadata
)
SELECT '00000000-0000-4000-8000-00000000b512'::uuid, book_id, page_id,
       source_text, normalized_text, reference_kind, match_strategy, metadata
FROM quran_cross_reference_bridge
WHERE cross_reference_id = '00000000-0000-4000-8000-00000000b501'::uuid`,
			changeSQL: `UPDATE quran_cross_reference_bridge SET normalized_text = normalized_text || ' changed'
WHERE cross_reference_id = '00000000-0000-4000-8000-00000000b501'::uuid`,
			pairSQL: `UPDATE quran_cross_reference_bridge SET normalized_text = NULL, normalization_version = 1
WHERE cross_reference_id = '00000000-0000-4000-8000-00000000b501'::uuid`,
			otherSQL: `UPDATE quran_cross_reference_bridge SET updated_at = updated_at
WHERE cross_reference_id = '00000000-0000-4000-8000-00000000b501'::uuid`,
		},
		{
			name: "knowledge_mentions",
			insertSQL: `INSERT INTO knowledge_mentions (
    id, run_id, book_id, page_id, document_id, extraction_class, extraction_text,
    exact_quote, char_start, char_end, alignment_status, normalized_text, review_status
)
SELECT '00000000-0000-4000-8000-00000000b513'::uuid, run_id, book_id, page_id,
       document_id || '-copy', extraction_class, extraction_text, exact_quote,
       char_start, char_end, alignment_status, normalized_text, review_status
FROM knowledge_mentions WHERE id = '00000000-0000-4000-8000-00000000b502'::uuid`,
			changeSQL: `UPDATE knowledge_mentions SET normalized_text = normalized_text || ' changed'
WHERE id = '00000000-0000-4000-8000-00000000b502'::uuid`,
			pairSQL: `UPDATE knowledge_mentions SET normalized_text = NULL, normalization_version = 1
WHERE id = '00000000-0000-4000-8000-00000000b502'::uuid`,
			otherSQL: `UPDATE knowledge_mentions SET review_status = review_status
WHERE id = '00000000-0000-4000-8000-00000000b502'::uuid`,
		},
		{
			name: "knowledge_entities",
			insertSQL: `INSERT INTO knowledge_entities (id, entity_type, canonical_name_ar, normalized_name_ar)
SELECT '00000000-0000-4000-8000-00000000b514'::uuid, entity_type,
       canonical_name_ar, normalized_name_ar
FROM knowledge_entities WHERE id = '00000000-0000-4000-8000-00000000b503'::uuid`,
			changeSQL: `UPDATE knowledge_entities SET normalized_name_ar = normalized_name_ar || ' changed'
WHERE id = '00000000-0000-4000-8000-00000000b503'::uuid`,
			pairSQL: `UPDATE knowledge_entities SET normalized_name_ar = NULL, normalization_version = 1
WHERE id = '00000000-0000-4000-8000-00000000b503'::uuid`,
			otherSQL: `UPDATE knowledge_entities SET updated_at = updated_at
WHERE id = '00000000-0000-4000-8000-00000000b503'::uuid`,
		},
		{
			name: "knowledge_entity_aliases",
			insertSQL: `INSERT INTO knowledge_entity_aliases (
    id, entity_id, alias_text, normalized_alias, language, alias_type, review_status
)
SELECT '00000000-0000-4000-8000-00000000b515'::uuid, entity_id,
       alias_text || ' copy', normalized_alias || ' copy', language, alias_type, review_status
FROM knowledge_entity_aliases WHERE id = '00000000-0000-4000-8000-00000000b504'::uuid`,
			changeSQL: `UPDATE knowledge_entity_aliases SET normalized_alias = normalized_alias || ' changed'
WHERE id = '00000000-0000-4000-8000-00000000b504'::uuid`,
			pairSQL: `UPDATE knowledge_entity_aliases SET normalized_alias = NULL, normalization_version = 1
WHERE id = '00000000-0000-4000-8000-00000000b504'::uuid`,
			otherSQL: `UPDATE knowledge_entity_aliases SET review_status = review_status
WHERE id = '00000000-0000-4000-8000-00000000b504'::uuid`,
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectLiveNormalizationCheckViolation(t, tx, fmt.Sprintf("b5_insert_%d", i), tc.insertSQL)
			expectLiveNormalizationCheckViolation(t, tx, fmt.Sprintf("b5_change_%d", i), tc.changeSQL)
			expectLiveNormalizationCheckViolation(t, tx, fmt.Sprintf("b5_pair_%d", i), tc.pairSQL)

			requireLiveUnrelatedUpdateAllowed(t, tx, fmt.Sprintf("b5_other_%d", i), tc.otherSQL)
		})
	}
}

func seedLegacyAuthorsWithSearchText(t *testing.T, pool *pgxpool.Pool, ids []int64) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	require.NoError(t, err)

	for i, id := range ids {
		name := fmt.Sprintf("مؤلف تجربة النسخة %d", i)
		_, err = tx.Exec(ctx, `
INSERT INTO authors (id, name, name_search, name_search_normalization_version)
VALUES ($1, $2, $3, NULL)`, id, name, searchtext.Normalize(name))
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit(ctx))
}

func resetLiveQuranNormalizationVersionState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
DELETE FROM quran_cross_reference_bridge
WHERE book_id IN ($1, $2)`, liveNormalizationQuranBookBase, liveNormalizationQuranBookBase+1)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
DELETE FROM quran_book_references
WHERE book_id IN ($1, $2)`, liveNormalizationQuranBookBase, liveNormalizationQuranBookBase+1)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM backfill_jobs WHERE job_name = $1`, quranReferenceNormalizationVersionJobName)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func seedLegacyQuranNormalizationRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	require.NoError(t, err)

	for i := range 2 {
		bookID := liveNormalizationQuranBookBase + i
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", 0xb520+i)
		source := fmt.Sprintf("مرجع قرآن قديم %d", i)
		normalized := searchtext.Normalize(source)

		_, err = tx.Exec(ctx, `
INSERT INTO quran_book_references (
    id, book_id, page_id, source_text, normalized_text, normalization_version,
    reference_kind, match_strategy, review_status
) VALUES ($1::uuid, $2, 1, $3, $4, NULL, 'ambiguous', 'legacy', 'pending')`,
			id, bookID, source, normalized)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `
INSERT INTO quran_cross_reference_bridge (
    cross_reference_id, book_id, page_id, source_text, normalized_text,
    normalization_version, reference_kind, match_strategy
) VALUES ($1::uuid, $2, 1, $3, $4, NULL, 'ambiguous', 'legacy')`,
			id, bookID, source, normalized)
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit(ctx))
}

func seedLiveNormalizationMatrixLegacyRows(t *testing.T, tx pgx.Tx) {
	t.Helper()

	ctx := context.Background()
	statements := []string{
		`INSERT INTO authors (id, name, name_search, name_search_normalization_version)
VALUES (910009001, 'Legacy Author', 'legacy author', NULL)`,
		`INSERT INTO books (id, name, has_content)
VALUES (910009001, 'Legacy normalization fixture', TRUE)`,
		`INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES (910009001, 1, '<p>Legacy</p>', 'Legacy')`,
		`INSERT INTO generation_runs (id, task_name, model_id, prompt_version, metadata)
VALUES (
    '00000000-0000-4000-8000-00000000b500', 'mentions',
    'legacy-fixture-model', 'legacy-fixture-v1', '{}'
)`,
		`INSERT INTO knowledge_extraction_runs (id, task_name, prompt_version, model_id)
VALUES (
    '00000000-0000-4000-8000-00000000b500', 'mentions',
    'legacy-fixture-v1', 'legacy-fixture-model'
)`,
		`INSERT INTO quran_book_references (
    id, book_id, page_id, source_text, normalized_text, normalization_version,
    reference_kind, match_strategy, review_status
) VALUES (
    '00000000-0000-4000-8000-00000000b501', 910009001, 1,
    'Legacy Quran', 'legacy quran', NULL, 'ambiguous', 'legacy', 'pending'
)`,
		`INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus,
    source_work_id, target_work_id, kind, method, method_detail,
    confidence, review_status, evidence_text, evidence_normalized,
    normalization_version, origin, origin_key
) VALUES (
    '00000000-0000-4000-8000-00000000b501',
    'kitab/910009001', 'kitab/910009001/h/1', 'kitab', 'kitab',
    910009001, 910009001, 'cites', 'resolver', '{"strategy":"legacy-fixture"}',
    1, 'pending', 'Legacy Quran', 'legacy quran', 1,
    'legacy_quran_reference', '00000000-0000-4000-8000-00000000b501'
)`,
		`INSERT INTO quran_cross_reference_bridge (
    cross_reference_id, book_id, page_id, source_text, normalized_text,
    normalization_version, reference_kind, match_strategy
) VALUES (
    '00000000-0000-4000-8000-00000000b501', 910009001, 1,
    'Legacy Quran', 'legacy quran', NULL, 'ambiguous', 'legacy'
)`,
		`INSERT INTO knowledge_mentions (
    id, run_id, book_id, page_id, document_id, extraction_class, extraction_text,
    exact_quote, char_start, char_end, alignment_status, normalized_text,
    normalization_version, review_status
) VALUES (
    '00000000-0000-4000-8000-00000000b502',
    '00000000-0000-4000-8000-00000000b500', 910009001, 1,
    'legacy-doc', 'term', 'Legacy mention', 'Legacy mention', 0, 14,
    'aligned', 'legacy mention', NULL, 'pending'
)`,
		`INSERT INTO knowledge_entities (
    id, entity_type, canonical_name_ar, normalized_name_ar, normalization_version
) VALUES (
    '00000000-0000-4000-8000-00000000b503', 'person',
    'Legacy Entity', 'legacy entity', NULL
)`,
		`INSERT INTO knowledge_entity_aliases (
    id, entity_id, alias_text, normalized_alias, normalization_version,
    language, alias_type, review_status
) VALUES (
    '00000000-0000-4000-8000-00000000b504',
    '00000000-0000-4000-8000-00000000b503',
    'Legacy Alias', 'legacy alias', NULL, 'ar', 'extracted', 'pending'
)`,
	}

	for _, statement := range statements {
		_, err := tx.Exec(ctx, statement)
		require.NoError(t, err)
	}
}

func requireLiveUnrelatedUpdateAllowed(t *testing.T, tx pgx.Tx, savepoint, sqlText string) {
	t.Helper()

	ctx := context.Background()
	_, err := tx.Exec(ctx, "SAVEPOINT "+savepoint)
	require.NoError(t, err)

	tag, execErr := tx.Exec(ctx, sqlText)
	if execErr != nil {
		_, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint)
		require.NoError(t, rollbackErr)

		_, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint)
		require.NoError(t, releaseErr)
		require.NoError(t, execErr, "an unrelated update must preserve an untouched legacy row")

		return
	}

	assert.Equal(t, int64(1), tag.RowsAffected())

	_, err = tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint)
	require.NoError(t, err)
}

func expectLiveNormalizationCheckViolation(t *testing.T, tx pgx.Tx, savepoint, sqlText string) {
	t.Helper()

	ctx := context.Background()
	_, err := tx.Exec(ctx, "SAVEPOINT "+savepoint)
	require.NoError(t, err)

	_, execErr := tx.Exec(ctx, sqlText)
	require.Error(t, execErr)

	var pgErr *pgconn.PgError
	require.True(t, errors.As(execErr, &pgErr), "expected a PostgreSQL check violation, got %v", execErr)
	assert.Equal(t, "23514", pgErr.Code)

	_, err = tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint)
	require.NoError(t, err)
}

func readLiveBackfillStateForJob(t *testing.T, pool *pgxpool.Pool, jobName string) State {
	t.Helper()

	state := State{JobName: jobName}
	require.NoError(t, pool.QueryRow(context.Background(), `
SELECT status, last_cursor, rows_total, rows_done
FROM backfill_jobs
WHERE job_name = $1`, jobName).
		Scan(&state.Status, &state.LastCursor, &state.RowsTotal, &state.RowsDone))

	return state
}
