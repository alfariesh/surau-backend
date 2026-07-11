package backfill

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/importer"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var liveQuranBridgeFixtureBooks = []int{991301, 991302, 991303} //nolint:gochecknoglobals // fixed live-test scope

var liveQuranBridgeFixtureIDs = []string{ //nolint:gochecknoglobals // fixed live-test identities
	"00000000-0000-5000-8000-00000000b301",
	"00000000-0000-5000-8000-00000000b302",
	"00000000-0000-5000-8000-00000000b303",
}

func resetLiveQuranBridgeState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit; releases the connection after assertion failures

	_, err = tx.Exec(ctx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE cross_reference_registry_state
		SET quran_legacy_frozen = FALSE, updated_at = now()
		WHERE id = TRUE`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		DELETE FROM cross_references
		WHERE id IN ($1::uuid, $2::uuid, $3::uuid)`,
		liveQuranBridgeFixtureIDs[0], liveQuranBridgeFixtureIDs[1], liveQuranBridgeFixtureIDs[2])
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	_, err = pool.Exec(ctx, `
		DELETE FROM quran_book_references
		WHERE id IN ($1::uuid, $2::uuid, $3::uuid)`,
		liveQuranBridgeFixtureIDs[0], liveQuranBridgeFixtureIDs[1], liveQuranBridgeFixtureIDs[2])
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM books WHERE id = ANY($1)`, liveQuranBridgeFixtureBooks)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		DELETE FROM backfill_jobs
		WHERE job_name IN ($1, $2, $3, $4)`,
		quranCrossReferenceBridgeJobName,
		quranCrossReferenceFreezeJobName,
		quranCrossReferenceUnfreezeJobName,
		quranReferenceNormalizationVersionJobName)
	require.NoError(t, err)
}

func seedLiveQuranBridgeTarget(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO quran_surahs (
			surah_id, name_arabic, name_latin, ayah_count, metadata)
		VALUES (
			1, 'الفاتحة', 'Al-Fatihah', 1,
			'{"fixture":"b3-backfill-target"}'::jsonb)
		ON CONFLICT (surah_id) DO NOTHING`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
		INSERT INTO quran_ayahs (
			surah_id, ayah_number, ayah_key, text_qpc_hafs,
			text_imlaei_simple, search_text, metadata)
		VALUES (
			1, 1, '1:1', 'بِسْمِ ٱللَّهِ ٱلرَّحْمَٰنِ ٱلرَّحِيمِ',
			'بسم الله الرحمن الرحيم', 'بسم الله الرحمن الرحيم',
			'{"fixture":"b3-backfill-target"}'::jsonb)
		ON CONFLICT (surah_id, ayah_number) DO NOTHING`)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))

	t.Cleanup(func() { cleanupLiveQuranBridgeTarget(t, pool) })
}

func cleanupLiveQuranBridgeTarget(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		DELETE FROM quran_ayahs
		WHERE surah_id = 1
		  AND ayah_number = 1
		  AND metadata @> '{"fixture":"b3-backfill-target"}'::jsonb`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		DELETE FROM quran_surahs surah
		WHERE surah.surah_id = 1
		  AND surah.metadata @> '{"fixture":"b3-backfill-target"}'::jsonb
		  AND NOT EXISTS (
			  SELECT 1 FROM quran_ayahs ayah WHERE ayah.surah_id = surah.surah_id
		  )`)
	require.NoError(t, err)
}

func seedLiveQuranBridgeBook(t *testing.T, pool *pgxpool.Pool, fixtureIndex int, kind string) {
	t.Helper()

	ctx := context.Background()
	bookID := liveQuranBridgeFixtureBooks[fixtureIndex]
	id := liveQuranBridgeFixtureIDs[fixtureIndex]
	sourceText := fmt.Sprintf("سورة الفاتحة: 1 تجربة %d", fixtureIndex)

	_, err := pool.Exec(ctx, `INSERT INTO books (id, name, has_content) VALUES ($1, $2, TRUE)`,
		bookID, fmt.Sprintf("كتاب اختبار المرجع %d", fixtureIndex))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO book_pages (book_id, page_id, content_html, content_text)
		VALUES ($1, 1, $2, $2)`, bookID, sourceText)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO quran_book_references (
			id, book_id, page_id, source_text, normalized_text, normalization_version, reference_kind,
			surah_id, from_ayah_number, to_ayah_number, from_ayah_key, to_ayah_key,
			match_strategy, confidence, review_status, metadata)
		VALUES (
			$1::uuid, $2, 1, $3, $4, $5, $6,
			1, 1, 1, '1:1', '1:1',
			'explicit_surah_ayah', 0.95, $7, '{"fixture":"b3-backfill"}'::jsonb)`,
		id, bookID, sourceText, searchtext.Normalize(sourceText), searchtext.ProfileVersion, kind, "approved")
	require.NoError(t, err)
}

//nolint:paralleltest // serial: overrides quranBridgeBookScope and shares one checkpoint row
func TestLiveCrossReferencesQuranBridgePauseResumeAndRerun(t *testing.T) {
	pool := liveBackfillPool(t)
	seedLiveQuranBridgeTarget(t, pool)
	originalFrozen := liveQuranReferenceWritesFrozen(t, pool)

	originalScope := quranBridgeBookScope
	quranBridgeBookScope = liveQuranBridgeFixtureBooks[:2]

	t.Cleanup(func() { quranBridgeBookScope = originalScope })

	resetLiveQuranBridgeState(t, pool)
	t.Cleanup(func() {
		resetLiveQuranBridgeState(t, pool)
		setLiveQuranReferenceWritesFrozen(t, pool, originalFrozen)
	})
	seedLiveQuranBridgeBook(t, pool, 0, "surah_ayah")
	seedLiveQuranBridgeBook(t, pool, 1, "quote")

	job, err := ByName(quranCrossReferenceBridgeJobName)
	require.NoError(t, err)

	// Run 1: one whole book commits, then cancellation persists a checkpoint.
	runCtx, cancelRun := context.WithCancel(context.Background())
	runner := &Runner{
		Pool:      pool,
		Log:       logger.New("error"),
		ChunkSize: 1,
		Sleep:     time.Millisecond,
		AfterChunk: func(state State) {
			if state.RowsDone == 1 {
				cancelRun()
			}
		},
	}
	require.NoError(t, runner.Run(runCtx, job, false))
	cancelRun()

	paused := readLiveQuranBridgeState(t, pool)
	assert.Equal(t, StatusPaused, paused.Status)
	assert.Equal(t, int64(liveQuranBridgeFixtureBooks[0]), paused.LastCursor)
	assert.Equal(t, int64(1), countLiveQuranBridgeRows(t, pool))
	assert.False(t, liveQuranReferenceWritesFrozen(t, pool), "a paused backfill must leave legacy writes open")

	freezeJob, err := ByName(quranCrossReferenceFreezeJobName)
	require.NoError(t, err)

	freezeRunner := &Runner{Pool: pool, Log: logger.New("error"), ChunkSize: 1, Sleep: time.Millisecond}
	err = freezeRunner.Run(context.Background(), freezeJob, false)
	require.ErrorIs(t, err, errQuranBridgeIncomplete)
	assert.Equal(t, StatusFailed,
		readLiveCrossReferenceJobState(t, pool, quranCrossReferenceFreezeJobName).Status)
	assert.False(t, liveQuranReferenceWritesFrozen(t, pool),
		"explicit freeze must reject a paused bridge checkpoint")

	// Simulate drift behind the cursor: an earlier book becomes unbridged while
	// writes are still open. The circular selector must process book 2, wrap to
	// book 1, and only then finish.
	deleteLiveCrossReference(t, pool, liveQuranBridgeFixtureIDs[0])
	assert.Zero(t, countLiveQuranBridgeRows(t, pool))

	// Run 2 resumes at the second book and then wraps; UUID parity is exact
	// across all three representations and approved status survives the upsert.
	runner.AfterChunk = nil
	require.NoError(t, runner.Run(context.Background(), job, false))

	completed := readLiveQuranBridgeState(t, pool)
	assert.Equal(t, StatusCompleted, completed.Status)
	assert.Equal(t, int64(3), completed.RowsDone, "wrapped drift book is processed after the forward pass")
	assert.Equal(t, int64(2), countLiveQuranBridgeRows(t, pool))
	assertLiveQuranBridgeParity(t, pool)
	assert.False(t, liveQuranReferenceWritesFrozen(t, pool),
		"a fixture-scoped run must never flip the global freeze switch")

	remaining, err := freezeJob.CountRemaining(context.Background(), pool)
	require.NoError(t, err)
	assert.Equal(t, int64(1), remaining)
	require.NoError(t, freezeRunner.Run(context.Background(), freezeJob, false))
	freezeState := readLiveCrossReferenceJobState(t, pool, quranCrossReferenceFreezeJobName)
	assert.Equal(t, StatusCompleted, freezeState.Status)
	assert.Equal(t, int64(1), freezeState.RowsDone)
	assert.True(t, liveQuranReferenceWritesFrozen(t, pool))
	remaining, err = freezeJob.CountRemaining(context.Background(), pool)
	require.NoError(t, err)
	assert.Zero(t, remaining)

	_, err = pool.Exec(context.Background(), `
		UPDATE quran_book_references SET updated_at = updated_at WHERE id = $1::uuid`,
		liveQuranBridgeFixtureIDs[0])
	require.Error(t, err, "direct legacy write must be rejected after the completed backfill freezes it")

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "42501", pgErr.Code)

	// The guarded service remains able to repair/dual-write after freeze.
	deleteLiveCrossReference(t, pool, liveQuranBridgeFixtureIDs[0])
	bridged, err := importer.BridgeLegacyQuranReferencesForBook(
		context.Background(), pool, liveQuranBridgeFixtureBooks[0],
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), bridged)
	assert.Equal(t, int64(2), countLiveQuranBridgeRows(t, pool))

	before := liveQuranBridgeSnapshot(t, pool)
	require.NoError(t, runner.Run(context.Background(), job, true))
	after := liveQuranBridgeSnapshot(t, pool)
	assert.Equal(t, before, after, "restart after completion must be an idempotent registry no-op")

	unfreezeJob, err := ByName(quranCrossReferenceUnfreezeJobName)
	require.NoError(t, err)
	remaining, err = unfreezeJob.CountRemaining(context.Background(), pool)
	require.NoError(t, err)
	assert.Equal(t, int64(1), remaining)

	unfreezeRunner := &Runner{Pool: pool, Log: logger.New("error"), ChunkSize: 1, Sleep: time.Millisecond}
	require.NoError(t, unfreezeRunner.Run(context.Background(), unfreezeJob, false))
	unfreezeState := readLiveCrossReferenceJobState(t, pool, quranCrossReferenceUnfreezeJobName)
	assert.Equal(t, StatusCompleted, unfreezeState.Status)
	assert.Equal(t, int64(1), unfreezeState.RowsDone)
	assert.False(t, liveQuranReferenceWritesFrozen(t, pool))
	remaining, err = unfreezeJob.CountRemaining(context.Background(), pool)
	require.NoError(t, err)
	assert.Zero(t, remaining)

	_, err = pool.Exec(context.Background(), `
		UPDATE quran_book_references SET updated_at = updated_at WHERE id = $1::uuid`,
		liveQuranBridgeFixtureIDs[0])
	require.NoError(t, err, "old-binary compatible direct write must work after explicit unfreeze")
}

//nolint:paralleltest // serial: shares the B-3 guarded registry and fixed fixture ids
func TestLiveQuranNormalizationVersionBackfillVerifiesAndStampsAtomically(t *testing.T) {
	pool := liveBackfillPool(t)
	seedLiveQuranBridgeTarget(t, pool)
	resetLiveQuranBridgeState(t, pool)
	t.Cleanup(func() { resetLiveQuranBridgeState(t, pool) })

	seedLiveQuranBridgeBook(t, pool, 0, "surah_ayah")
	bridged, err := importer.BridgeLegacyQuranReferencesForBook(
		context.Background(),
		pool,
		liveQuranBridgeFixtureBooks[0],
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), bridged)

	forceLiveQuranNormalizationLegacy(t, pool, false)

	// An unrelated update leaves an unversioned legacy derivative readable.
	_, err = pool.Exec(context.Background(), `
UPDATE quran_book_references
SET review_status = review_status
WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[0])
	require.NoError(t, err)

	// Changing the derivative without its version is rejected by the DB gate.
	_, err = pool.Exec(context.Background(), `
UPDATE quran_book_references
SET normalized_text = normalized_text || ' changed'
WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[0])

	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code)

	job := quranReferenceNormalizationVersionJob{}
	newCursor, processed, done, err := job.ProcessChunk(
		context.Background(),
		pool,
		int64(liveQuranBridgeFixtureBooks[0]-1),
		1,
	)
	require.NoError(t, err)
	assert.Equal(t, int64(liveQuranBridgeFixtureBooks[0]), newCursor)
	assert.Equal(t, int64(1), processed)
	assert.False(t, done)
	assertLiveQuranNormalizationVersions(t, pool, searchtext.ProfileVersion, searchtext.ProfileVersion)

	// One drifted row aborts before either table receives a version.
	forceLiveQuranNormalizationLegacy(t, pool, true)
	_, processed, _, err = job.ProcessChunk(
		context.Background(),
		pool,
		int64(liveQuranBridgeFixtureBooks[0]-1),
		1,
	)
	require.Error(t, err)
	require.ErrorContains(t, err, "does not match search-key v1")
	assert.Zero(t, processed)
	assertLiveQuranNormalizationVersions(t, pool, 0, 0)
}

//nolint:paralleltest // serial: mutates fixed legacy Quran fixtures and guarded registry rows
func TestLiveQuranBridgeLabelsOnlyVerifiedLegacyNormalization(t *testing.T) {
	pool := liveBackfillPool(t)
	seedLiveQuranBridgeTarget(t, pool)
	t.Cleanup(func() { resetLiveQuranBridgeState(t, pool) })

	t.Run("matching legacy text is stamped atomically", func(t *testing.T) {
		resetLiveQuranBridgeState(t, pool)
		seedLiveQuranBridgeBook(t, pool, 0, "surah_ayah")
		setLiveUnbridgedQuranNormalization(t, pool, false)

		bridged, err := importer.BridgeLegacyQuranReferencesForBook(
			context.Background(), pool, liveQuranBridgeFixtureBooks[0],
		)
		require.NoError(t, err)
		assert.Equal(t, int64(1), bridged)
		assertLiveQuranNormalizationVersions(
			t,
			pool,
			searchtext.ProfileVersion,
			searchtext.ProfileVersion,
		)
	})

	t.Run("drift aborts every derived write", func(t *testing.T) {
		resetLiveQuranBridgeState(t, pool)
		seedLiveQuranBridgeBook(t, pool, 0, "surah_ayah")
		setLiveUnbridgedQuranNormalization(t, pool, true)

		bridged, err := importer.BridgeLegacyQuranReferencesForBook(
			context.Background(), pool, liveQuranBridgeFixtureBooks[0],
		)
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid cross-reference")
		assert.Zero(t, bridged)

		var (
			version     *int
			derivedRows int
		)
		require.NoError(t, pool.QueryRow(context.Background(), `
SELECT normalization_version,
       (SELECT count(*) FROM cross_references WHERE id = $1::uuid)
     + (SELECT count(*) FROM quran_cross_reference_bridge WHERE cross_reference_id = $1::uuid)
FROM quran_book_references
WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[0]).Scan(&version, &derivedRows))
		assert.Nil(t, version)
		assert.Zero(t, derivedRows)
	})
}

func setLiveUnbridgedQuranNormalization(t *testing.T, pool *pgxpool.Pool, drift bool) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	require.NoError(t, err)

	normalized := searchtext.Normalize(fmt.Sprintf("سورة الفاتحة: 1 تجربة %d", 0))
	if drift {
		normalized = "drift"
	}

	_, err = tx.Exec(ctx, `
UPDATE quran_book_references
SET normalized_text = $2, normalization_version = NULL
WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[0], normalized)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func forceLiveQuranNormalizationLegacy(t *testing.T, pool *pgxpool.Pool, drift bool) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = replica`)
	require.NoError(t, err)

	if drift {
		_, err = tx.Exec(ctx, `
UPDATE quran_book_references
SET normalized_text = 'drift', normalization_version = NULL
WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[0])
	} else {
		_, err = tx.Exec(ctx, `
UPDATE quran_book_references
SET normalization_version = NULL
WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[0])
	}

	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE quran_cross_reference_bridge
SET normalization_version = NULL
WHERE cross_reference_id = $1::uuid`, liveQuranBridgeFixtureIDs[0])
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func assertLiveQuranNormalizationVersions(t *testing.T, pool *pgxpool.Pool, legacyWant, bridgeWant int) {
	t.Helper()

	var legacyVersion, bridgeVersion *int
	require.NoError(t, pool.QueryRow(context.Background(), `
SELECT qbr.normalization_version, bridge.normalization_version
FROM quran_book_references qbr
JOIN quran_cross_reference_bridge bridge ON bridge.cross_reference_id = qbr.id
WHERE qbr.id = $1::uuid`, liveQuranBridgeFixtureIDs[0]).Scan(&legacyVersion, &bridgeVersion))

	if legacyWant == 0 {
		assert.Nil(t, legacyVersion)
	} else {
		require.NotNil(t, legacyVersion)
		assert.Equal(t, legacyWant, *legacyVersion)
	}

	if bridgeWant == 0 {
		assert.Nil(t, bridgeVersion)
	} else {
		require.NotNil(t, bridgeVersion)
		assert.Equal(t, bridgeWant, *bridgeVersion)
	}
}

//nolint:paralleltest // serial: overrides quranBridgeBookScope and shares fixture ids
func TestLiveCrossReferencesQuranBridgePreflightRejectsApprovedAmbiguous(t *testing.T) {
	pool := liveBackfillPool(t)
	seedLiveQuranBridgeTarget(t, pool)
	originalFrozen := liveQuranReferenceWritesFrozen(t, pool)

	originalScope := quranBridgeBookScope
	quranBridgeBookScope = liveQuranBridgeFixtureBooks[2:]

	t.Cleanup(func() { quranBridgeBookScope = originalScope })

	resetLiveQuranBridgeState(t, pool)
	t.Cleanup(func() {
		resetLiveQuranBridgeState(t, pool)
		setLiveQuranReferenceWritesFrozen(t, pool, originalFrozen)
	})
	seedLiveQuranBridgeBook(t, pool, 2, "ambiguous")

	job, err := ByName(quranCrossReferenceBridgeJobName)
	require.NoError(t, err)
	_, err = job.CountRemaining(context.Background(), pool)
	require.ErrorIs(t, err, errApprovedQuranAmbiguous)
	require.ErrorContains(t, err, liveQuranBridgeFixtureIDs[2])
	assert.Zero(t, countLiveQuranBridgeRows(t, pool))

	// Once moderation removes the impossible approved state, an ambiguous row
	// with a clear surah target is mappable even without an ayah range.
	_, err = pool.Exec(context.Background(), `
		UPDATE quran_book_references
		SET review_status = 'needs_review',
		    from_ayah_number = NULL,
		    to_ayah_number = NULL,
		    from_ayah_key = NULL,
		    to_ayah_key = NULL
		WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[2])
	require.NoError(t, err)

	remaining, err := job.CountRemaining(context.Background(), pool)
	require.NoError(t, err)
	assert.Equal(t, int64(1), remaining)

	bridged, err := importer.BridgeLegacyQuranReferencesForBook(
		context.Background(), pool, liveQuranBridgeFixtureBooks[2],
	)
	require.NoError(t, err)
	assert.Equal(t, int64(1), bridged)

	var targetAnchor, reviewStatus string
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT target_anchor, review_status
		FROM cross_references
		WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[2]).Scan(&targetAnchor, &reviewStatus))
	assert.Equal(t, "quran/1", targetAnchor)
	assert.Equal(t, "ambiguous", reviewStatus)
	deleteLiveCrossReference(t, pool, liveQuranBridgeFixtureIDs[2])

	_, err = pool.Exec(context.Background(), `
		UPDATE quran_book_references
		SET reference_kind = 'quote',
		    review_status = 'approved',
		    surah_id = NULL,
		    from_ayah_number = NULL,
		    to_ayah_number = NULL,
		    from_ayah_key = NULL,
		    to_ayah_key = NULL
		WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[2])
	require.NoError(t, err)

	_, err = job.CountRemaining(context.Background(), pool)
	require.ErrorIs(t, err, errApprovedQuranUnmappable)
	require.ErrorContains(t, err, liveQuranBridgeFixtureIDs[2])
	assert.Zero(t, countLiveQuranBridgeRows(t, pool))

	// The same invalid source is non-public rather than fatal after moderation
	// moves it out of approved: a soft-deleted Work must be skipped and left in
	// the legacy review queue, never bridged into an unresolvable Anchor.
	_, err = pool.Exec(context.Background(), `
		UPDATE quran_book_references
		SET reference_kind = 'quote',
		    surah_id = 1,
		    from_ayah_number = 1,
		    to_ayah_number = 1,
		    from_ayah_key = '1:1',
		    to_ayah_key = '1:1',
		    review_status = 'needs_review'
		WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[2])
	require.NoError(t, err)
	_, err = pool.Exec(context.Background(), `
		UPDATE books SET is_deleted = TRUE WHERE id = $1`, liveQuranBridgeFixtureBooks[2])
	require.NoError(t, err)

	remaining, err = job.CountRemaining(context.Background(), pool)
	require.NoError(t, err)
	assert.Zero(t, remaining)

	bridged, err = importer.BridgeLegacyQuranReferencesForBook(
		context.Background(), pool, liveQuranBridgeFixtureBooks[2],
	)
	require.NoError(t, err)
	assert.Zero(t, bridged)

	_, processed, done, err := job.ProcessChunk(context.Background(), pool, 0, 1)
	require.NoError(t, err)
	assert.Zero(t, processed)
	assert.True(t, done)
	assert.Zero(t, countLiveQuranBridgeRows(t, pool))

	_, err = pool.Exec(context.Background(), `
		UPDATE quran_book_references
		SET review_status = 'approved'
		WHERE id = $1::uuid`, liveQuranBridgeFixtureIDs[2])
	require.NoError(t, err)
	_, err = job.CountRemaining(context.Background(), pool)
	require.ErrorIs(t, err, errApprovedQuranUnmappable)
	require.ErrorContains(t, err, liveQuranBridgeFixtureIDs[2])
}

func readLiveQuranBridgeState(t *testing.T, pool *pgxpool.Pool) State {
	t.Helper()

	return readLiveCrossReferenceJobState(t, pool, quranCrossReferenceBridgeJobName)
}

func readLiveCrossReferenceJobState(t *testing.T, pool *pgxpool.Pool, jobName string) State {
	t.Helper()

	var state State
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT job_name, status, last_cursor, rows_total, rows_done
		FROM backfill_jobs WHERE job_name = $1`, jobName).
		Scan(&state.JobName, &state.Status, &state.LastCursor, &state.RowsTotal, &state.RowsDone))

	return state
}

func countLiveQuranBridgeRows(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()

	var count int64
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM quran_cross_reference_bridge
		WHERE book_id = ANY($1)`, liveQuranBridgeFixtureBooks).Scan(&count))

	return count
}

func assertLiveQuranBridgeParity(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	var legacyMinusRegistry, registryMinusLegacy int
	require.NoError(t, pool.QueryRow(context.Background(), `
WITH legacy_projection AS (
	SELECT legacy.id::text AS id,
	       legacy.book_id,
	       legacy.page_id,
	       legacy.heading_id,
	       legacy.knowledge_mention_id::text AS knowledge_mention_id,
	       legacy.source_text,
	       legacy.normalized_text,
	       legacy.reference_kind,
	       legacy.surah_id,
	       legacy.from_ayah_number,
	       legacy.to_ayah_number,
	       legacy.from_ayah_key,
	       legacy.to_ayah_key,
	       legacy.match_strategy,
	       legacy.metadata AS bridge_metadata,
	       CASE
	           WHEN heading.heading_id IS NOT NULL
	               THEN 'kitab/' || legacy.book_id || '/h/' || legacy.heading_id
	           ELSE 'kitab/' || legacy.book_id
	       END AS source_anchor,
	       CASE
	           WHEN legacy.reference_kind = 'surah' THEN 'quran/' || legacy.surah_id
	           WHEN legacy.from_ayah_number = legacy.to_ayah_number
	               THEN 'quran/' || legacy.surah_id || ':' || legacy.from_ayah_number
	           ELSE 'quran/' || legacy.surah_id || ':' || legacy.from_ayah_number ||
	               '..quran/' || legacy.surah_id || ':' || legacy.to_ayah_number
	       END AS target_anchor,
	       CASE WHEN legacy.reference_kind = 'quote' THEN 'quotes' ELSE 'cites' END AS kind,
	       'resolver'::text AS method,
	       jsonb_build_object('strategy', legacy.match_strategy) AS method_detail,
	       legacy.confidence,
	       legacy.review_status,
	       legacy.source_text AS evidence_text,
	       legacy.normalized_text AS evidence_normalized,
	       $2::integer AS normalization_version,
	       'legacy_quran_reference'::text AS origin,
	       legacy.id::text AS origin_key,
	       NULL::text AS created_by,
	       NULL::text AS reviewed_by,
	       NULL::timestamptz AS reviewed_at,
	       legacy.metadata AS registry_metadata,
	       legacy.created_at,
	       legacy.updated_at
	FROM quran_book_references legacy
	LEFT JOIN book_headings heading
	       ON heading.book_id = legacy.book_id
	      AND heading.heading_id = legacy.heading_id
	      AND heading.is_deleted = FALSE
	WHERE legacy.book_id = ANY($1)
	  AND legacy.review_status = 'approved'
),
registry_projection AS (
	SELECT ref.id::text AS id,
	       bridge.book_id,
	       bridge.page_id,
	       bridge.heading_id,
	       bridge.knowledge_mention_id::text AS knowledge_mention_id,
	       bridge.source_text,
	       bridge.normalized_text,
	       bridge.reference_kind,
	       bridge.surah_id,
	       bridge.from_ayah_number,
	       bridge.to_ayah_number,
	       bridge.from_ayah_key,
	       bridge.to_ayah_key,
	       bridge.match_strategy,
	       bridge.metadata AS bridge_metadata,
	       ref.source_anchor,
	       ref.target_anchor,
	       ref.kind,
	       ref.method,
	       ref.method_detail,
	       ref.confidence,
	       ref.review_status,
	       ref.evidence_text,
	       ref.evidence_normalized,
	       ref.normalization_version,
	       ref.origin,
	       ref.origin_key,
	       ref.created_by::text AS created_by,
	       ref.reviewed_by::text AS reviewed_by,
	       ref.reviewed_at,
	       ref.metadata AS registry_metadata,
	       ref.created_at,
	       ref.updated_at
	FROM cross_references ref
	JOIN quran_cross_reference_bridge bridge ON bridge.cross_reference_id = ref.id
	WHERE bridge.book_id = ANY($1)
	  AND ref.review_status = 'approved'
),
legacy_minus_registry AS (
	SELECT * FROM legacy_projection
	EXCEPT
	SELECT * FROM registry_projection
),
registry_minus_legacy AS (
	SELECT * FROM registry_projection
	EXCEPT
	SELECT * FROM legacy_projection
)
SELECT (SELECT count(*) FROM legacy_minus_registry),
	   (SELECT count(*) FROM registry_minus_legacy)`,
		liveQuranBridgeFixtureBooks[:2], searchtext.ProfileVersion).
		Scan(&legacyMinusRegistry, &registryMinusLegacy))
	assert.Zero(t, legacyMinusRegistry, "every approved legacy field must have an identical registry projection")
	assert.Zero(t, registryMinusLegacy, "registry projection must not invent or alter approved legacy fields")
}

func liveQuranBridgeSnapshot(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	var snapshot string
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT COALESCE(string_agg(
			id::text || '|' || source_anchor || '|' || target_anchor || '|' || kind || '|' ||
			review_status || '|' || created_at::text || '|' || updated_at::text,
			E'\n' ORDER BY id), '')
		FROM cross_references
		WHERE source_work_id = ANY($1)`, liveQuranBridgeFixtureBooks[:2]).Scan(&snapshot))

	return snapshot
}

func liveQuranReferenceWritesFrozen(t *testing.T, pool *pgxpool.Pool) bool {
	t.Helper()

	var frozen bool
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT quran_legacy_frozen
		FROM cross_reference_registry_state
		WHERE id = TRUE`).Scan(&frozen))

	return frozen
}

func setLiveQuranReferenceWritesFrozen(t *testing.T, pool *pgxpool.Pool, frozen bool) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
		UPDATE cross_reference_registry_state
		SET quran_legacy_frozen = $1, updated_at = now()
		WHERE id = TRUE`, frozen)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}

func deleteLiveCrossReference(t *testing.T, pool *pgxpool.Pool, id string) {
	t.Helper()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM cross_references WHERE id = $1::uuid`, id)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}
