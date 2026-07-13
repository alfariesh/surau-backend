package persistent

import (
	"context"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	repository "github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveK1CitableCatalogSchema proves the database-owned K-1 boundaries:
// machine review eligibility cannot be bypassed, public retrieval obeys B-4,
// source mutations mark a materialized book stale, and mention bindings cannot
// claim success without an exact unit span.
//
//nolint:maintidx,paralleltest,wsl_v5 // serial live-DB invariant matrix in one rollback-only transaction
func TestLiveK1CitableCatalogSchema(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	tx, err := pg.Pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	const (
		bookID           = -91301
		actorID          = "c1010000-0000-4000-8000-000000000001"
		generationRunID  = "c1010000-0000-4000-8000-000000000002"
		extractionRunID  = "c1010000-0000-4000-8000-000000000003"
		sourceUnitID     = "c1010000-0000-4000-8000-000000000011"
		pendingUnitID    = "c1010000-0000-4000-8000-000000000012"
		approvedUnitID   = "c1010000-0000-4000-8000-000000000013"
		quranQuoteID     = "c1010000-0000-4000-8000-000000000014"
		quranFootnoteID  = "c1010000-0000-4000-8000-000000000015"
		compatKitabID    = "c1010000-0000-4000-8000-000000000016"
		compatQuranID    = "c1010000-0000-4000-8000-000000000017"
		restrictedUnitID = "c1010000-0000-4000-8000-000000000018"
	)

	_, err = tx.Exec(ctx, `
INSERT INTO users (id, username, email, password_hash)
VALUES ($1, 'k1-schema-live', 'k1-schema-live@example.test', 'x')`, actorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO books (id, name) VALUES ($1, 'K-1 schema fixture')`, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE books
SET license_status = 'permitted',
    license_reason = 'K-1 live schema fixture',
    license_updated_by = $2
WHERE id = $1`, bookID, actorID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>fixture source</p>', 'fixture source')`, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_publications (book_id, status, published_at)
VALUES ($1, 'published', now())`, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE books
SET units_derived_at = now(),
    units_stale_at = NULL,
    units_derivation_profile_version = $2
WHERE id = $1`, bookID, entity.KitabUnitDerivationProfileVersion)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `
INSERT INTO generation_runs (id, task_name, model_id, prompt_version, provider)
VALUES ($1, 'k1-citable-unit', 'schema-test-model', 'k1-schema-v1', 'test')`, generationRunID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO generation_runs (id, task_name, model_id, prompt_version, provider)
VALUES ($1, 'mentions', 'schema-test-model', 'k1-mentions-v1', 'test')`, extractionRunID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO knowledge_extraction_runs (
    id, task_name, prompt_version, model_id, provider
) VALUES ($1, 'mentions', 'k1-mentions-v1', 'schema-test-model', 'test')`, extractionRunID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	require.NoError(t, err)
	// Pre-K-1 writers omit content_role. The compatibility sentinel must map
	// kitab to book_page while leaving Quran NULL; explicit NULL is still tested
	// below as an invalid new kitab write.
	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, book_id, page_id, kind, ordinal, position, anchor, text,
    text_normalized, normalization_version, content_hash, occurrence, language,
    provenance_class, lifecycle
) VALUES (
    $2, 'kitab', $1, 1, 'quran_quote', 29, 29, 'kitab/-91301/h/0/u/29',
    'legacy kitab writer', 'legacy kitab writer', 1, decode(repeat('29', 32), 'hex'),
    1, 'ar', 'source', 'active'
)`, bookID, compatKitabID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, kind, ordinal, position, anchor, text, text_normalized,
    normalization_version, content_hash, occurrence, language, provenance_class, lifecycle
) VALUES (
    $1, 'quran', 'primary_text', 91301, 0, 'quran/114:91301/u/1',
    'legacy quran writer', 'legacy quran writer', 1, decode(repeat('30', 32), 'hex'),
    1, 'ar', 'source', 'active'
)`, compatQuranID)
	require.NoError(t, err)
	var kitabRole, quranRole *string
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT content_role FROM citable_units WHERE id = $1`, compatKitabID).Scan(&kitabRole))
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT content_role FROM citable_units WHERE id = $1`, compatQuranID).Scan(&quranRole))
	require.NotNil(t, kitabRole)
	assert.Equal(t, entity.UnitContentRoleBookPage, *kitabRole)
	assert.Nil(t, quranRole)

	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, book_id, page_id, kind, ordinal, position, anchor, text,
    text_normalized, normalization_version, content_hash, occurrence, language,
    provenance_class, generation_run_id, content_role, review_status,
    source_document_hash, source_char_start, source_char_end
) VALUES
    ($2, 'kitab', $1, 1, 'paragraph', 1, 0, 'kitab/-91301/h/0/u/1',
     'fixture source', 'fixture source', 1, decode(repeat('11', 32), 'hex'), 1,
     'ar', 'source', NULL, 'book_page', 'approved',
     decode(repeat('aa', 32), 'hex'), 0, 14),
    ($3, 'kitab', $1, 1, 'paragraph', 2, 1, 'kitab/-91301/h/0/u/2',
     'machine pending', 'machine pending', 1, decode(repeat('22', 32), 'hex'), 1,
     'ar', 'machine', $6, 'book_page', 'pending',
     decode(repeat('aa', 32), 'hex'), 15, 30),
    ($4, 'kitab', $1, 1, 'paragraph', 3, 2, 'kitab/-91301/h/0/u/3',
     'machine approved', 'machine approved', 1, decode(repeat('33', 32), 'hex'), 1,
     'ar', 'machine', $6, 'book_page', 'approved',
     decode(repeat('aa', 32), 'hex'), 31, 47),
    ($5, 'kitab', $1, 1, 'quran_quote', 4, 3, 'kitab/-91301/h/0/u/4',
     'quoted ayah', 'quoted ayah', 1, decode(repeat('44', 32), 'hex'), 1,
     'ar', 'source', NULL, 'book_page', 'approved',
     decode(repeat('aa', 32), 'hex'), 48, 59)`,
		bookID, sourceUnitID, pendingUnitID, approvedUnitID, quranQuoteID, generationRunID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, book_id, page_id, kind, ordinal, position, parent_unit_id,
    anchor, marker, text, text_normalized, normalization_version, content_hash,
    occurrence, language, provenance_class, content_role, review_status,
    source_document_hash, source_char_start, source_char_end
) VALUES (
    $2, 'kitab', $1, 1, 'quran_quote', 5, 4, $3,
    'kitab/-91301/h/0/u/5', '(١)', 'quoted ayah in footnote',
    'quoted ayah in footnote', 1, decode(repeat('45', 32), 'hex'), 1,
    'ar', 'source', 'book_page', 'approved', decode(repeat('aa', 32), 'hex'), 60, 83
)`, bookID, quranFootnoteID, sourceUnitID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO citable_units (
    id, corpus, book_id, page_id, kind, ordinal, position, anchor, text,
    text_normalized, normalization_version, content_hash, occurrence, language,
    provenance_class, content_role, review_status, license_status,
    source_document_hash, source_char_start, source_char_end
) VALUES (
    $2, 'kitab', $1, 1, 'paragraph', 6, 5, 'kitab/-91301/h/0/u/6',
    'restricted override', 'restricted override', 1, decode(repeat('46', 32), 'hex'), 1,
    'ar', 'source', 'book_page', 'approved', 'restricted',
    decode(repeat('aa', 32), 'hex'), 84, 103
)`, bookID, restrictedUnitID)
	require.NoError(t, err)

	rows, err := tx.Query(ctx, `
SELECT id::text, interpretive_retrieval_eligible
FROM citable_units
WHERE id = ANY($1::uuid[])
	ORDER BY id`, []string{
		sourceUnitID, pendingUnitID, approvedUnitID, quranQuoteID, quranFootnoteID, restrictedUnitID,
	})
	require.NoError(t, err)
	eligibility := map[string]bool{}
	for rows.Next() {
		var id string
		var eligible bool
		require.NoError(t, rows.Scan(&id, &eligible))
		eligibility[id] = eligible
	}
	rows.Close()
	require.NoError(t, rows.Err())
	assert.True(t, eligibility[sourceUnitID])
	assert.False(t, eligibility[pendingUnitID])
	assert.True(t, eligibility[approvedUnitID])
	assert.False(t, eligibility[quranQuoteID])
	assert.False(t, eligibility[quranFootnoteID], "Quran in a marker-linked footnote must fail closed")
	assert.True(t, eligibility[restrictedUnitID],
		"license is a separate query-time gate from structural interpretive eligibility")

	var publicIDs []string
	rows, err = tx.Query(ctx, `
SELECT id::text
FROM public_book_interpretive_citable_units
WHERE book_id = $1
ORDER BY id`, bookID)
	require.NoError(t, err)
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		publicIDs = append(publicIDs, id)
	}
	rows.Close()
	require.NoError(t, rows.Err())
	assert.Contains(t, publicIDs, sourceUnitID,
		"NULL unit license inherits the permitted/grandfather-capable book publication gate")
	assert.NotContains(t, publicIDs, restrictedUnitID,
		"a restricted unit override must fail closed even when its book is public")
	assert.Equal(t, []string{sourceUnitID, approvedUnitID}, publicIDs)

	_, err = tx.Exec(ctx, `
UPDATE book_pages
SET updated_at = updated_at
WHERE book_id = $1 AND page_id = 1`, bookID)
	require.NoError(t, err)
	var isStale bool
	require.NoError(t, tx.QueryRow(ctx, `
SELECT units_stale_at IS NOT NULL FROM books WHERE id = $1`, bookID).Scan(&isStale))
	assert.True(t, isStale)

	var publicCount int
	require.NoError(t, tx.QueryRow(ctx, `
SELECT count(*) FROM public_book_interpretive_citable_units WHERE book_id = $1`, bookID).
		Scan(&publicCount))
	assert.Zero(t, publicCount, "a stale book must disappear from unit retrieval atomically")

	_, err = tx.Exec(ctx, `
INSERT INTO book_headings (book_id, heading_id, page_id, ordinal, content)
VALUES ($1, 1, 1, 1, 'hidden asset heading')`, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_production_projects (id, book_id, lang)
VALUES ('c1010000-0000-4000-8000-000000000020', $1, 'id')`, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE books SET units_stale_at = NULL WHERE id = $1`, bookID)
	require.NoError(t, err)
	fingerprintBeforeDraft, err := catalogSourceFingerprint(ctx, tx, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO section_translations (
    book_id, heading_id, lang, content, provenance_class
) VALUES ($1, 1, 'id', 'hidden draft translation', 'source')`, bookID)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT units_stale_at IS NOT NULL FROM books WHERE id = $1`, bookID).Scan(&isStale))
	assert.False(t, isStale, "a hidden enrichment draft must not take published retrieval offline")
	fingerprintAfterDraft, err := catalogSourceFingerprint(ctx, tx, bookID)
	require.NoError(t, err)
	assert.Equal(t, fingerprintBeforeDraft, fingerprintAfterDraft,
		"hidden draft changes are not effective derivation inputs")

	_, err = tx.Exec(ctx, `UPDATE books SET major_release = 2, minor_release = 7 WHERE id = $1`, bookID)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx, `
SELECT units_stale_at IS NOT NULL FROM books WHERE id = $1`, bookID).Scan(&isStale))
	assert.True(t, isStale, "release metadata changes provenance and must invalidate units")
	_, err = tx.Exec(ctx, `UPDATE books SET units_stale_at = NULL WHERE id = $1`, bookID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `SAVEPOINT invalid_mention_binding`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO knowledge_mentions (
    id, run_id, book_id, page_id, document_id, extraction_class,
    extraction_text, exact_quote, char_start, char_end, alignment_status,
    normalized_text, normalization_version, unit_binding_status
) VALUES (
    'c1010000-0000-4000-8000-000000000021', $1, $2, 1, 'k1-schema-doc',
    'concept', 'fixture', 'fixture', 0, 7, 'aligned', 'fixture', 1, 'bound'
)`, extractionRunID, bookID)
	require.Error(t, err, "bound mention without a unit/range/hash must fail")
	_, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_mention_binding`)
	require.NoError(t, rollbackErr)

	_, err = tx.Exec(ctx, `SAVEPOINT approved_mention_without_unit`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO knowledge_mentions (
    id, run_id, book_id, page_id, document_id, extraction_class,
    extraction_text, exact_quote, char_start, char_end, alignment_status,
    normalized_text, normalization_version, review_status, source_hash
) VALUES (
    'c1010000-0000-4000-8000-000000000022', $1, $2, 1, 'k1-approved-unbound',
    'concept', 'fixture', 'fixture', 0, 7, 'aligned', 'fixture', 1, 'approved', repeat('aa', 32)
)`, extractionRunID, bookID)
	require.NoError(t, err, "the deferred constraint permits binding later in the same transaction")
	_, err = tx.Exec(ctx, `SET CONSTRAINTS trg_knowledge_mentions_approved_unit_guard IMMEDIATE`)
	require.Error(t, err, "an approved mention cannot commit without an exact resolvable unit")
	_, rollbackErr = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT approved_mention_without_unit`)
	require.NoError(t, rollbackErr)

	_, err = tx.Exec(ctx, `
INSERT INTO knowledge_mentions (
    id, run_id, book_id, page_id, document_id, extraction_class,
    extraction_text, exact_quote, char_start, char_end, alignment_status,
    normalized_text, normalization_version, review_status, source_hash,
    unit_id, unit_char_start, unit_char_end, unit_binding_status,
    unit_binding_version, unit_source_hash
) VALUES (
    'c1010000-0000-4000-8000-000000000023', $1, $2, 1, 'k1-approved-bound',
    'concept', 'fixture', 'fixture', 0, 7, 'aligned', 'fixture', 1, 'approved', repeat('aa', 32),
    $3, 0, 7, 'bound', 1, repeat('aa', 32)
)`, extractionRunID, bookID, sourceUnitID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET CONSTRAINTS trg_knowledge_mentions_approved_unit_guard IMMEDIATE`)
	require.NoError(t, err, "an exact approved binding to an active source unit must pass")

	_, err = tx.Exec(ctx, `SAVEPOINT mutate_approved_mention_source`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE knowledge_mentions SET exact_quote = 'source'
WHERE id = 'c1010000-0000-4000-8000-000000000023'`)
	require.Error(t, err, "changing an approved source locator cannot retain a stale binding")
	_, rollbackErr = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT mutate_approved_mention_source`)
	require.NoError(t, rollbackErr)

	_, err = tx.Exec(ctx, `SAVEPOINT invalid_kitab_content_role`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
	INSERT INTO citable_units (
	    id, corpus, book_id, page_id, kind, ordinal, position, anchor, text,
	    text_normalized, normalization_version, content_hash, occurrence, language,
	    provenance_class, review_status, content_role
	) VALUES (
	    'c1010000-0000-4000-8000-000000000031', 'kitab', $1, 1, 'paragraph',
	    31, 31, 'kitab/-91301/h/0/u/31', 'invalid role', 'invalid role', 1,
	    decode(repeat('55', 32), 'hex'), 1, 'ar', 'source', 'approved', NULL
	)`, bookID)
	require.Error(t, err, "kitab unit with NULL content_role must fail structurally")
	_, rollbackErr = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT invalid_kitab_content_role`)
	require.NoError(t, rollbackErr)
	require.NoError(t, tx.Rollback(ctx))
}

// TestLiveK1SteadyStateMentionBindingCannotBypassApproval proves the catalog
// binder forces the deferred approved-mention guard before later transaction
// stages. The legacy capability is enabled only to seed an intentionally
// broken historical row, then explicitly removed before exercising the binder.
//
//nolint:paralleltest,wsl_v5 // rollback-only live invariant fixture
func TestLiveK1SteadyStateMentionBindingCannotBypassApproval(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	tx, err := pg.Pool.BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	const (
		bookID = -91305
		runID  = "c1050000-0000-4000-8000-000000000001"
	)
	_, err = tx.Exec(ctx, `
INSERT INTO generation_runs (id, task_name, model_id, prompt_version, provider)
VALUES ($1, 'mentions', 'k1-live-model', 'k1-live-v1', 'test')`, runID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO knowledge_extraction_runs (id, task_name, prompt_version, model_id, provider)
VALUES ($1, 'mentions', 'k1-live-v1', 'k1-live-model', 'test')`, runID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO books (id, name) VALUES ($1, 'K-1 mention guard fixture')`, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>exact source</p>', 'exact source')`, bookID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `SET LOCAL surau.k1_mention_binding_backfill = 'on'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO knowledge_mentions (
    id, run_id, book_id, page_id, document_id, extraction_class,
    extraction_text, exact_quote, char_start, char_end, alignment_status,
    normalized_text, normalization_version, review_status, source_hash
) VALUES (
    'c1050000-0000-4000-8000-000000000002', $1, $2, 1, 'k1-steady-invalid',
    'concept', 'stale quote', 'stale quote', 0, 11, 'aligned', 'stale quote', 1,
    'approved', repeat('aa', 32)
)`, runID, bookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL surau.k1_mention_binding_backfill = 'off'`)
	require.NoError(t, err)

	catalogTx := &citableUnitCatalogTx{tx: tx, legacyMentionBindingBypass: false}
	err = catalogTx.BindKnowledgeMentions(ctx, bookID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "approved knowledge mention")
}

// TestLiveK1IntermediateReconcileKeepsBookStale proves the raw-first bootstrap
// cannot accidentally open the public unit view between its two service-owned
// passes. The effective pass is the only path allowed to clear units_stale_at.
//
//nolint:paralleltest,wsl_v5 // commits one isolated negative-id fixture for the repo-owned transaction
func TestLiveK1IntermediateReconcileKeepsBookStale(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	const bookID = -91302
	_, err = pg.Pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, bookID)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, cleanupErr := pg.Pool.Exec(context.Background(), `DELETE FROM books WHERE id = $1`, bookID); cleanupErr != nil {
			t.Errorf("cleanup K-1 intermediate fixture: %v", cleanupErr)
		}
	})

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO books (id, name)
VALUES ($1, 'K-1 intermediate fixture')`, bookID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, 'raw fixture', 'raw fixture')`, bookID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `UPDATE books SET units_stale_at = NULL WHERE id = $1`, bookID)
	require.NoError(t, err)

	repo := NewCitableUnitRepo(&postgres.Postgres{Pool: pg.Pool})
	source, err := repo.LoadBookSource(ctx, bookID)
	require.NoError(t, err)
	snapshot, err := repo.Snapshot(ctx, bookID)
	require.NoError(t, err)

	plan := entity.UnitReconcilePlan{
		BookID:         bookID,
		LoadedAt:       source.LoadedAt,
		BasedOn:        snapshot.Fingerprint,
		Intermediate:   true,
		ExpectedActive: 0,
	}
	require.NoError(t, repo.ApplyReconcile(ctx, &plan))

	var (
		stale         bool
		profileIsNull bool
	)
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT units_stale_at IS NOT NULL, units_derivation_profile_version IS NULL
FROM books WHERE id = $1`, bookID).Scan(&stale, &profileIsNull))
	assert.True(t, stale, "intermediate raw pass must remain structurally invisible")
	assert.True(t, profileIsNull, "intermediate pass must not claim a finalized profile")
}

// TestLiveK1CatalogTransactionRollsBackRegistryAndQueue proves a failure at
// the final durable-checkpoint stage cannot leave registry writes, mention
// binding, or book materialization metadata committed independently.
//
//nolint:paralleltest,wsl_v5 // commits and removes one isolated negative-id fixture
func TestLiveK1CatalogTransactionRollsBackRegistryAndQueue(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	const (
		bookID  = -91303
		jobName = "k1-atomic-live"
	)
	cleanupK1CatalogBook(t, pg, bookID)
	t.Cleanup(func() { cleanupK1CatalogBook(t, pg, bookID) })

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO books (id, name, has_content)
VALUES ($1, 'K-1 atomic fixture', TRUE)`, bookID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>نص ذري</p>', 'نص ذري')`, bookID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO backfill_jobs (job_name, status, profile_version)
VALUES ($1, 'running', $2)`, jobName, entity.KitabUnitDerivationProfileVersion)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO citable_unit_catalog_queue (
    job_name, book_id, sequence, status, attempts, started_at
) VALUES ($1, $2, 1, 'running', 1, now())`, jobName, bookID)
	require.NoError(t, err)

	citableRepo := NewCitableUnitRepo(&postgres.Postgres{Pool: pg.Pool})
	err = citableRepo.WithCatalogTransaction(ctx, bookID, func(tx repository.CitableUnitCatalogTx) error {
		source, loadErr := tx.LoadBookSource(ctx, bookID)
		if loadErr != nil {
			return loadErr
		}
		snapshot, snapshotErr := tx.Snapshot(ctx, bookID)
		if snapshotErr != nil {
			return snapshotErr
		}
		derived, _, deriveErr := unitregistry.DeriveBook(&source)
		if deriveErr != nil {
			return deriveErr
		}
		plan := unitregistry.PlanBook(bookID, source.LoadedAt, derived, &snapshot)
		if applyErr := tx.ApplyReconcile(ctx, &plan); applyErr != nil {
			return applyErr
		}
		if bindingErr := tx.BindKnowledgeMentions(ctx, bookID); bindingErr != nil {
			return bindingErr
		}

		// A missing/changed queue ownership is a CAS failure after every other
		// per-book stage. It must roll the whole transaction back.
		return tx.CompleteQueueItem(ctx, jobName+"-wrong", bookID, [32]byte{1}, [32]byte{2})
	})
	require.ErrorIs(t, err, entity.ErrUnitReconcileConflict)

	var (
		unitCount int
		derived   bool
		status    string
	)
	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM citable_units WHERE book_id = $1`, bookID).Scan(&unitCount))
	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT units_derived_at IS NOT NULL FROM books WHERE id = $1`, bookID).Scan(&derived))
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT status FROM citable_unit_catalog_queue
WHERE job_name = $1 AND book_id = $2`, jobName, bookID).Scan(&status))
	assert.Zero(t, unitCount)
	assert.False(t, derived)
	assert.Equal(t, "running", status)
}

// TestLiveK1CatalogTransactionRejectsBackdatedConcurrentSourceCommit covers
// the adversarial order T0 update → T1 catalog load → source commit. Timestamp
// comparison alone cannot see T0, so PostgreSQL's repeatable-read write
// conflict must abort the catalog transaction and preserve the stale marker.
//
//nolint:paralleltest,wsl_v5 // commits and removes one isolated negative-id fixture
func TestLiveK1CatalogTransactionRejectsBackdatedConcurrentSourceCommit(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	const bookID = -91304
	cleanupK1CatalogBook(t, pg, bookID)
	t.Cleanup(func() { cleanupK1CatalogBook(t, pg, bookID) })

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO books (id, name, has_content)
VALUES ($1, 'K-1 concurrent-source fixture', TRUE)`, bookID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>النص الأول</p>', 'النص الأول')`, bookID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `UPDATE books SET units_stale_at = NULL WHERE id = $1`, bookID)
	require.NoError(t, err)

	// This update receives T0 now, but remains uncommitted while the catalog
	// transaction establishes its T1 snapshot and loads the old source.
	sourceConn, err := pgx.Connect(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sourceConn.Close(context.Background()) })
	sourceTx, err := sourceConn.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sourceTx.Rollback(context.Background()) })
	_, err = sourceTx.Exec(ctx, `
UPDATE book_pages
SET content_html = '<p>النص المتزامن</p>',
    content_text = 'النص المتزامن',
    updated_at = now()
WHERE book_id = $1 AND page_id = 1`, bookID)
	require.NoError(t, err)

	citableRepo := NewCitableUnitRepo(&postgres.Postgres{Pool: pg.Pool})
	err = citableRepo.WithCatalogTransaction(ctx, bookID, func(tx repository.CitableUnitCatalogTx) error {
		source, loadErr := tx.LoadBookSource(ctx, bookID)
		if loadErr != nil {
			return loadErr
		}
		snapshot, snapshotErr := tx.Snapshot(ctx, bookID)
		if snapshotErr != nil {
			return snapshotErr
		}
		if commitErr := sourceTx.Commit(ctx); commitErr != nil {
			return commitErr
		}
		derived, _, deriveErr := unitregistry.DeriveBook(&source)
		if deriveErr != nil {
			return deriveErr
		}
		plan := unitregistry.PlanBook(bookID, source.LoadedAt, derived, &snapshot)

		return tx.ApplyReconcile(ctx, &plan)
	})
	require.ErrorIs(t, err, entity.ErrUnitReconcileConflict)

	var (
		unitCount int
		isStale   bool
		text      string
	)
	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM citable_units WHERE book_id = $1`, bookID).Scan(&unitCount))
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT units_stale_at IS NOT NULL FROM books WHERE id = $1`, bookID).Scan(&isStale))
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT content_text FROM book_pages WHERE book_id = $1 AND page_id = 1`, bookID).Scan(&text))
	assert.Zero(t, unitCount)
	assert.True(t, isStale)
	assert.Equal(t, "النص المتزامن", text)
}

//nolint:wsl_v5 // best-effort fixture cleanup keeps each error beside its statement
func cleanupK1CatalogBook(t *testing.T, pg *postgres.Postgres, bookID int) {
	t.Helper()
	_, err := pg.Pool.Exec(context.Background(), `
DELETE FROM backfill_jobs WHERE job_name = 'k1-atomic-live'`)
	if err != nil {
		t.Errorf("cleanup K-1 atomic job: %v", err)
	}
	_, err = pg.Pool.Exec(context.Background(), `DELETE FROM books WHERE id = $1`, bookID)
	if err != nil {
		t.Errorf("cleanup K-1 catalog fixture %d: %v", bookID, err)
	}
}
