package persistent

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveBookLicenseAudit proves the B-4 API adapter against real PostgreSQL:
// every new book starts unknown, an ETag-guarded decision writes one atomic
// audit row, stale writes fail, and the coverage report can find the result.
//
//nolint:paralleltest // serial live-DB check over fixed throwaway IDs (gated on SURAU_LIVE_PG)
func TestLiveBookLicenseAudit(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	const (
		bookID  = -90404
		actorID = "b4040000-0000-4000-8000-000000000001"
	)

	ctx := context.Background()

	seedLiveUser(t, pg, actorID, "b4-license")
	cleanupBookLicenseFixture(t, pg, bookID)
	t.Cleanup(func() { cleanupBookLicenseFixture(t, pg, bookID) })

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO books (id, name, license_status)
VALUES ($1, 'B-4 unaudited permitted fixture', 'permitted')`, bookID)
	assertPostgresConstraint(t, err, "book_license_initial_unknown_check")

	_, err = pg.Pool.Exec(ctx, `INSERT INTO books (id, name) VALUES ($1, 'B-4 License Live Fixture')`, bookID)
	require.NoError(t, err)

	licenseRepo := NewEditorialRepo(pg)
	initial, err := licenseRepo.GetBookLicense(ctx, bookID)
	require.NoError(t, err)
	assert.Equal(t, entity.LicenseStatusUnknown, initial.LicenseStatus)
	assert.False(t, initial.UpdatedAt.IsZero())

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO book_publications (book_id, status)
VALUES ($1, 'published')`, bookID)
	assertPostgresConstraint(t, err, licensePublishConstraint)

	evidenceURL := "https://example.test/licenses/b4-fixture"
	permitted, err := licenseRepo.UpdateBookLicense(ctx, actorID, entity.BookLicenseUpdate{
		BookID:        bookID,
		LicenseStatus: entity.LicenseStatusPermitted,
		Reason:        "Live-test permission evidence",
		EvidenceURL:   &evidenceURL,
	}, &initial.UpdatedAt)
	require.NoError(t, err)
	assert.Equal(t, entity.LicenseStatusPermitted, permitted.LicenseStatus)
	assert.Equal(t, actorID, *permitted.UpdatedBy)
	assert.True(t, permitted.UpdatedAt.After(initial.UpdatedAt))

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO book_publications (book_id, status)
VALUES ($1, 'published')`, bookID)
	require.NoError(t, err)

	_, err = pg.Pool.Exec(ctx, `
UPDATE book_publications
SET license_grandfathered_at = now()
WHERE book_id = $1`, bookID)
	assertPostgresConstraint(t, err, "book_license_grandfather_immutable_check")

	assertLiveCitableUnitLicenseInheritance(t, pg, bookID)

	_, err = licenseRepo.UpdateBookLicense(ctx, actorID, entity.BookLicenseUpdate{
		BookID:        bookID,
		LicenseStatus: entity.LicenseStatusRestricted,
		Reason:        "Stale write must not land",
	}, &initial.UpdatedAt)
	assert.ErrorIs(t, err, entity.ErrPreconditionFailed)

	items, total, counts, err := licenseRepo.ListBookLicenseAudit(ctx, repo.LicenseAuditFilter{
		Status: entity.LicenseStatusPermitted,
		Limit:  200,
	})
	require.NoError(t, err)
	assert.Positive(t, total)
	assert.Positive(t, counts.Total)
	assert.Contains(t, licenseAuditBookIDs(items), bookID)

	var auditCount int

	err = pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM book_license_audits
WHERE book_id = $1
  AND old_status = 'unknown'
  AND new_status = 'permitted'
  AND actor_id = $2`, bookID, actorID).Scan(&auditCount)
	require.NoError(t, err)
	assert.Equal(t, 1, auditCount, "the DB trigger must write exactly one atomic audit row")
}

// TestLiveBookLicenseDatabaseGuards freezes the direct-SQL bypasses that B-4
// must close even when a caller does not use the Go publication services.
//
//nolint:maintidx,paralleltest,wsl_v5 // one serial transactional database invariant matrix
func TestLiveBookLicenseDatabaseGuards(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	const (
		publicBookID = -90410
		hiddenBookID = -90411
		permitBookID = -90412
		categoryID   = -90410
		authorID     = -90410
		actorID      = "b4040000-0000-4000-8000-000000000010"
		projectID    = "b4040000-0000-4000-8000-000000000011"
	)

	ctx := context.Background()

	seedLiveUser(t, pg, actorID, "b4-license-guards")

	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	_, err = tx.Exec(ctx, `INSERT INTO categories (id, name) VALUES ($1, 'B-4 guard category')`, categoryID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO authors (id, name) VALUES ($1, 'B-4 guard author')`, authorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO books (id, name, category_id, author_id, has_content)
VALUES ($1, 'B-4 public grandfather', $4, $5, true),
       ($2, 'B-4 hidden source', $4, $5, true),
       ($3, 'B-4 audited permitted', $4, $5, true)`,
		publicBookID, hiddenBookID, permitBookID, categoryID, authorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE books
SET license_status = 'permitted',
    license_reason = 'database guard fixture permission',
    license_updated_by = $2
WHERE id = $1`, permitBookID, actorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_publications (book_id, status)
VALUES ($1, 'published')`, permitBookID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>public source</p>', 'public source'),
       ($2, 99, '<p>hidden movable source</p>', 'hidden movable source')`, publicBookID, hiddenBookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content)
VALUES ($1, 1, 1, 0, 1, 'public heading')`, publicBookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id)
VALUES ($1, 1, 1, 1)`, publicBookID)
	require.NoError(t, err)

	// Reproduce only the migration-owned grandfather seed; restore all guards
	// before any assertion below.
	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_publications (book_id, status, license_grandfathered_at)
VALUES ($1, 'published', now())`, publicBookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_production_projects (
    id, book_id, lang, workflow_status, publication_status, license_grandfathered_at
) VALUES ($1, $2, 'en', 'published', 'published', now())`, projectID, publicBookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_metadata_edits (book_id, status, display_title, updated_by, published_at)
VALUES ($1, 'published', 'grandfather overlay', $2, now())`, publicBookID, actorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_heading_summaries (
    book_id, heading_id, lang, summary, summary_status, provenance_class
) VALUES ($1, 1, 'ar', 'grandfather summary', 'generated', 'source')`, publicBookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'origin'`)
	require.NoError(t, err)

	assertTxConstraint(ctx, t, tx, "book_license_grandfather_immutable_check", `
UPDATE book_publications SET book_id = $2 WHERE book_id = $1`, publicBookID, hiddenBookID)
	assertTxConstraint(ctx, t, tx, "book_license_grandfather_immutable_check", `
UPDATE book_production_projects SET book_id = $2 WHERE id = $1`, projectID, hiddenBookID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE book_pages SET book_id = $2 WHERE book_id = $1 AND page_id = 99`, hiddenBookID, publicBookID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE book_heading_ranges SET end_anchor = 'toc-99' WHERE book_id = $1 AND heading_id = 1`, publicBookID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
DELETE FROM book_metadata_edits WHERE book_id = $1 AND status = 'published'`, publicBookID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE book_metadata_edits SET book_id = $2
WHERE book_id = $1 AND status = 'published'`, publicBookID, hiddenBookID)

	// Arabic final assets and shared/raw metadata are public without an en/id
	// production-project dependency, so they must hit the same gate.
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE book_heading_summaries
SET summary = 'changed grandfather summary'
WHERE book_id = $1 AND heading_id = 1 AND lang = 'ar'`, publicBookID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE book_heading_summaries
SET book_id = $2
WHERE book_id = $1 AND heading_id = 1 AND lang = 'ar'`, publicBookID, hiddenBookID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE book_heading_summaries
SET lang = 'en'
WHERE book_id = $1 AND heading_id = 1 AND lang = 'ar'`, publicBookID)
	_, err = tx.Exec(ctx, `
UPDATE book_heading_summaries
SET updated_at = now()
WHERE book_id = $1 AND heading_id = 1 AND lang = 'ar'`, publicBookID)
	require.NoError(t, err, "timestamp-only final asset no-op must remain allowed")
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
INSERT INTO author_translations (author_id, lang, name, provenance_class)
VALUES ($1, 'en', 'changed author', 'editorial')`, authorID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
INSERT INTO category_translations (category_id, lang, name, provenance_class)
VALUES ($1, 'en', 'changed category', 'editorial')`, categoryID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE authors SET biography = 'changed biography' WHERE id = $1`, authorID)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE categories SET display_order = 99 WHERE id = $1`, categoryID)

	// Leaving the published workflow clears grandfather. Reactivation is then a
	// genuinely new publication and must be rejected while unknown.
	_, err = tx.Exec(ctx, `UPDATE book_production_projects SET workflow_status = 'archived' WHERE id = $1`, projectID)
	require.NoError(t, err)

	var (
		publicationStatus string
		markerCleared     bool
	)

	require.NoError(t, tx.QueryRow(ctx, `
SELECT publication_status, license_grandfathered_at IS NULL
FROM book_production_projects WHERE id = $1`, projectID).Scan(&publicationStatus, &markerCleared))
	assert.Equal(t, "archived", publicationStatus)
	assert.True(t, markerCleared)
	assertTxConstraint(ctx, t, tx, licensePublishConstraint, `
UPDATE book_production_projects
SET workflow_status = 'published', publication_status = 'published'
WHERE id = $1`, projectID)

	assertTxConstraint(ctx, t, tx, "book_license_audits_immutable_check", `
UPDATE book_license_audits SET reason = 'tampered' WHERE book_id = $1`, permitBookID)
	assertTxConstraint(ctx, t, tx, "book_license_audits_immutable_check", `
DELETE FROM book_license_audits WHERE book_id = $1`, permitBookID)

	_, err = tx.Exec(ctx, `
UPDATE books
SET license_status = 'restricted',
    license_reason = 'database guard fixture takedown',
    license_updated_by = $2
WHERE id = $1`, permitBookID, actorID)
	require.NoError(t, err)

	var k1Installed bool
	require.NoError(t, tx.QueryRow(ctx, `
SELECT to_regclass('public.public_book_interpretive_citable_units') IS NOT NULL`).Scan(&k1Installed))
	if k1Installed {
		t.Log("skip isolated B-4 down replay beneath newer K-1 dependent retrieval view")

		return
	}

	down, err := os.ReadFile("../../../migrations/20260711000003_add_book_license_gate.down.sql")
	require.NoError(t, err)
	up, err := os.ReadFile("../../../migrations/20260711000003_add_book_license_gate.up.sql")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, string(down))
	require.NoError(t, err)

	var archivedAuditCount int
	require.NoError(t, tx.QueryRow(ctx, `
SELECT count(*)
FROM admin_audit_logs
WHERE book_id = $1 AND action = 'license.decision.archive'`, permitBookID).Scan(&archivedAuditCount))
	assert.Equal(t, 2, archivedAuditCount)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT status FROM book_publications WHERE book_id = $1`, permitBookID).Scan(&publicationStatus))
	assert.Equal(t, "hidden", publicationStatus, "down must materialize restricted takedown for the old reader")

	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)

	var (
		restoredStatus     string
		restoredAuditCount int
	)

	require.NoError(t, tx.QueryRow(ctx, `
SELECT license_status FROM books WHERE id = $1`, permitBookID).Scan(&restoredStatus))
	assert.Equal(t, entity.LicenseStatusRestricted, restoredStatus)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT count(*) FROM book_license_audits WHERE book_id = $1`, permitBookID).Scan(&restoredAuditCount))
	assert.Equal(t, 2, restoredAuditCount)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT status FROM book_publications WHERE book_id = $1`, permitBookID).Scan(&publicationStatus))
	assert.Equal(t, "hidden", publicationStatus)
}

// TestLiveBookLicensePublishTakedownRace proves the lock order cannot leave a
// newly published Work visible after a concurrent restricted decision. The
// publish may legitimately win or lose the race; the takedown must always
// complete and the final public-reader state must be hidden.
//
//nolint:paralleltest // serial live-DB race over one fixed throwaway book
func TestLiveBookLicensePublishTakedownRace(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	const (
		bookID  = -90420
		actorID = "b4040000-0000-4000-8000-000000000020"
	)

	ctx := context.Background()

	seedLiveUser(t, pg, actorID, "b4-license-race")
	cleanupBookLicenseFixture(t, pg, bookID)
	t.Cleanup(func() { cleanupBookLicenseFixture(t, pg, bookID) })

	_, err = pg.Pool.Exec(ctx, `INSERT INTO books (id, name) VALUES ($1, 'B-4 publish/takedown race')`, bookID)
	require.NoError(t, err)

	licenseRepo := NewEditorialRepo(pg)
	_, err = licenseRepo.UpdateBookLicense(ctx, actorID, entity.BookLicenseUpdate{
		BookID:        bookID,
		LicenseStatus: entity.LicenseStatusPermitted,
		Reason:        "Race fixture permission",
	}, nil)
	require.NoError(t, err)

	for iteration := range 8 {
		if iteration > 0 {
			_, err = licenseRepo.UpdatePublication(ctx, actorID, entity.BookPublication{
				BookID: bookID,
				Status: entity.PublicationStatusHidden,
			})
			require.NoError(t, err)
			_, err = licenseRepo.UpdateBookLicense(ctx, actorID, entity.BookLicenseUpdate{
				BookID:        bookID,
				LicenseStatus: entity.LicenseStatusPermitted,
				Reason:        "Reset race fixture permission",
			}, nil)
			require.NoError(t, err)
		}

		raceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		start := make(chan struct{})
		publishResult := make(chan error, 1)
		takedownResult := make(chan error, 1)

		var ready sync.WaitGroup
		ready.Add(2)

		go func() {
			ready.Done()
			<-start

			_, publishErr := licenseRepo.UpdatePublication(raceCtx, actorID, entity.BookPublication{
				BookID: bookID,
				Status: entity.PublicationStatusPublished,
			})
			publishResult <- publishErr
		}()
		go func() {
			ready.Done()
			<-start

			_, takedownErr := licenseRepo.UpdateBookLicense(raceCtx, actorID, entity.BookLicenseUpdate{
				BookID:        bookID,
				LicenseStatus: entity.LicenseStatusRestricted,
				Reason:        "Concurrent takedown must win final visibility",
			}, nil)
			takedownResult <- takedownErr
		}()

		ready.Wait()
		close(start)

		publishErr := <-publishResult
		takedownErr := <-takedownResult

		cancel()

		require.NoError(t, takedownErr, "iteration %d takedown must not deadlock", iteration)

		if publishErr != nil {
			assert.ErrorIs(t, publishErr, entity.ErrLicenseNotPermitted)
		}

		current, currentErr := licenseRepo.GetBookLicense(ctx, bookID)
		require.NoError(t, currentErr)
		assert.Equal(t, entity.LicenseStatusRestricted, current.LicenseStatus)

		var publiclyVisible bool
		require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT EXISTS (SELECT 1 FROM public_book_publications WHERE book_id = $1)`, bookID).Scan(&publiclyVisible))
		assert.False(t, publiclyVisible, "iteration %d must end fail-closed", iteration)
	}
}

func assertTxConstraint(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	constraint,
	query string,
	args ...any,
) {
	t.Helper()

	_, err := tx.Exec(ctx, `SAVEPOINT b4_guard_assertion`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, query, args...)
	assertPostgresConstraint(t, err, constraint)

	_, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT b4_guard_assertion`)
	require.NoError(t, rollbackErr)

	_, releaseErr := tx.Exec(ctx, `RELEASE SAVEPOINT b4_guard_assertion`)
	require.NoError(t, releaseErr)
}

func assertLiveCitableUnitLicenseInheritance(t *testing.T, pg *postgres.Postgres, bookID int) {
	t.Helper()

	const unitID = "b4040000-0000-4000-8000-000000000002"

	ctx := context.Background()
	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)

	defer rollbackTx(ctx, tx)

	_, err = tx.Exec(ctx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, book_id, kind, ordinal, position, anchor, text,
    text_normalized, normalization_version, content_hash, occurrence,
    language, provenance_class, content_role, review_status
)
VALUES (
    $1, 'kitab', $2, 'paragraph', 1, 0, $3, 'license inheritance fixture',
    'license inheritance fixture', 1, decode(repeat('00', 32), 'hex'), 1,
    'ar', 'source', 'book_page', 'approved'
)`, unitID, bookID, "kitab/-90404/h/0/u/1")
	require.NoError(t, err)

	var effectiveStatus, source string
	require.NoError(t, tx.QueryRow(ctx, `
SELECT effective_license_status, license_source
FROM citable_units_with_effective_license
WHERE id = $1`, unitID).Scan(&effectiveStatus, &source))
	assert.Equal(t, entity.LicenseStatusPermitted, effectiveStatus)
	assert.Equal(t, "edition", source)

	_, err = tx.Exec(ctx, `
UPDATE citable_units
SET license_status = 'needs_review'
WHERE id = $1`, unitID)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT effective_license_status, license_source
FROM citable_units_with_effective_license
WHERE id = $1`, unitID).Scan(&effectiveStatus, &source))
	assert.Equal(t, entity.LicenseStatusNeedsReview, effectiveStatus)
	assert.Equal(t, "unit_override", source)

	require.NoError(t, tx.Commit(ctx))
}

func assertPostgresConstraint(t *testing.T, err error, constraint string) {
	t.Helper()

	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected PostgreSQL error, got %T: %v", err, err)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Equal(t, constraint, pgErr.ConstraintName)
}

func cleanupBookLicenseFixture(t *testing.T, pg *postgres.Postgres, bookID int) {
	t.Helper()

	ctx := context.Background()

	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Logf("begin cleanup B-4 license book %d: %v", bookID, err)

		return
	}
	defer rollbackTx(ctx, tx)

	// Only the superuser-backed live-test database may erase its own immutable
	// fixture evidence. Product roles never receive replication privileges.
	if _, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`); err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM book_license_audits WHERE book_id = $1`, bookID)
	}

	if err == nil {
		_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'origin'`)
	}

	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID)
	}

	if err == nil {
		err = tx.Commit(ctx)
	}

	if err != nil {
		t.Logf("cleanup B-4 license book %d: %v", bookID, err)
	}
}

func licenseAuditBookIDs(items []entity.BookLicenseAuditItem) []int {
	ids := make([]int, 0, len(items))

	for index := range items {
		ids = append(ids, items[index].BookID)
	}

	return ids
}
