package importer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/usecase/editorial"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This suite proves the B-1 acceptance criteria on a real imported book
// (phase-1b §4): AC-1 re-run determinism, AC-2 split-edit lineage via the
// editorial publish hook, AC-3 zero-dangling audit, AC-4 single-write-path
// enforcement. The fixture mirrors the real corpus shapes verified on book
// 797: a plain front-matter page, a tagged page (title anchor + <hr>
// footnote), and a page-start heading switch.

const (
	citableFixtureBookID  = 990797
	citableFixtureRelease = "citable-v1"

	citableLongParagraph = "الفقرة الطويلة التي سوف تنقسم إلى نصفين لاحقا"
	citableTwinLine      = "سطر مكرر حرفيا في الصفحة الأولى"
)

func citableFixtureBook() fixtureBook {
	pageOne := "نص افتتاحي قبل العناوين\n" +
		citableTwinLine + "\n" +
		citableTwinLine + "\n" +
		"{الر كتاب أنزلناه} [إبراهيم: 1]\n" +
		"متن يشير إلى الحاشية (¬١) بوضوح\n" +
		"__________\n" +
		"(¬١) نص الحاشية الأولى"
	pageTwo := "<span data-type='title' id=toc-10>الباب الأول</span>\n" +
		"فقرة أولى تحت الباب مستقرة\n" +
		citableLongParagraph + "\n" +
		"<hr><s0>\n" +
		"(١) حاشية بلا مرجع في المتن"
	pageThree := "خاتمة الباب في الصفحة الثالثة"

	return fixtureBook{
		ID:   citableFixtureBookID,
		Name: "كتاب تجريبي للوحدات",
		Pages: []fixturePage{
			{ID: 1, Content: pageOne},
			{ID: 2, Content: pageTwo},
			{ID: 3, Content: pageThree},
		},
		Headings: []fixtureHeading{
			{ID: 10, Content: "الباب الأول", PageID: 2},
			{ID: 20, Content: "الباب الثاني", PageID: 3},
		},
	}
}

func citableUnitService(pool *pgxpool.Pool) (*unitregistry.UseCase, *persistent.CitableUnitRepo) {
	pg := &postgres.Postgres{
		Pool:    pool,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}
	repo := persistent.NewCitableUnitRepo(pg)

	return unitregistry.New(repo), repo
}

type citableUnitRow struct {
	ID        string
	Anchor    string
	Kind      string
	Lifecycle string
	Ordinal   int
	Position  int
	UpdatedAt time.Time
}

func citableUnitsSnapshot(t *testing.T, pool *pgxpool.Pool, bookID int) []citableUnitRow {
	t.Helper()

	rows, err := pool.Query(context.Background(), `
		SELECT id, anchor, kind, lifecycle, ordinal, position, updated_at
		FROM citable_units WHERE book_id = $1 ORDER BY id`, bookID)
	require.NoError(t, err)

	defer rows.Close()

	out := make([]citableUnitRow, 0, 16)

	for rows.Next() {
		var r citableUnitRow
		require.NoError(t, rows.Scan(&r.ID, &r.Anchor, &r.Kind, &r.Lifecycle, &r.Ordinal, &r.Position, &r.UpdatedAt))
		out = append(out, r)
	}

	require.NoError(t, rows.Err())

	return out
}

//nolint:paralleltest // serial by design: live corpus invariants share one database
func TestLiveCitableUnitPilot(t *testing.T) {
	pool := liveBookImportPool(t)
	ctx := context.Background()

	resetBookImportState(t, pool, citableFixtureBookID, "citable-")

	dir := t.TempDir()
	writeBookSource(t, dir, citableFixtureBook())
	_, err := runBookImport(t, dir, citableFixtureRelease, citableFixtureBookID, "")
	require.NoError(t, err)

	svc, _ := citableUnitService(pool)

	// ---- Initial derive. ----
	report, err := svc.ReconcileBook(ctx, citableFixtureBookID)
	require.NoError(t, err)
	assert.Equal(t, 10, report.Minted, "5 front-matter body + 1 footnote + 2 body + 1 footnote + 1 body")
	assert.Equal(t, 3, report.Scopes)
	assert.Zero(t, report.Superseded)
	assert.Zero(t, report.Tombstoned)

	// Scope ownership: front-matter (NULL), anchored heading 10, page-start
	// heading 20.
	assert.Equal(t, 6, countRows(t, pool,
		`SELECT count(*) FROM citable_units WHERE book_id = $1 AND heading_id IS NULL`, citableFixtureBookID))
	assert.Equal(t, 3, countRows(t, pool,
		`SELECT count(*) FROM citable_units WHERE book_id = $1 AND heading_id = 10`, citableFixtureBookID))
	assert.Equal(t, 1, countRows(t, pool,
		`SELECT count(*) FROM citable_units WHERE book_id = $1 AND heading_id = 20`, citableFixtureBookID))

	// Identical twins: same content hash, distinct ids via occurrence 1 and 2.
	assert.Equal(t, 2, countRows(t, pool,
		`SELECT count(DISTINCT id) FROM citable_units WHERE book_id = $1 AND text = $2`,
		citableFixtureBookID, citableTwinLine))
	assert.Equal(t, 1, countRows(t, pool,
		`SELECT count(DISTINCT content_hash) FROM citable_units WHERE book_id = $1 AND text = $2`,
		citableFixtureBookID, citableTwinLine))

	// Quran quote classified; footnotes linked (marker on page 1, fallback on
	// page 2 — its parent is the long paragraph, the last body block).
	assert.Equal(t, 1, countRows(t, pool,
		`SELECT count(*) FROM citable_units WHERE book_id = $1 AND kind = 'quran_quote'`, citableFixtureBookID))
	assert.Equal(t, 1, countRows(t, pool, `
		SELECT count(*) FROM citable_units f
		JOIN citable_units p ON p.id = f.parent_unit_id
		WHERE f.book_id = $1 AND f.kind = 'footnote'
		  AND f.provenance_detail->>'footnote_link' = 'marker'
		  AND p.text LIKE '%(¬١)%'`, citableFixtureBookID))
	assert.Equal(t, 1, countRows(t, pool, `
		SELECT count(*) FROM citable_units f
		JOIN citable_units p ON p.id = f.parent_unit_id
		WHERE f.book_id = $1 AND f.kind = 'footnote'
		  AND f.provenance_detail->>'footnote_link' = 'fallback'
		  AND p.text = $2`, citableFixtureBookID, citableLongParagraph))

	// Source provenance carries the release.
	assert.Equal(t, 10, countRows(t, pool, `
		SELECT count(*) FROM citable_units
		WHERE book_id = $1 AND provenance_class = 'source'
		  AND provenance_detail->>'release' = '0.0'`, citableFixtureBookID))

	// ---- AC-1: re-run over the unchanged source is a byte-identical no-op. ----
	before := citableUnitsSnapshot(t, pool, citableFixtureBookID)
	rerun, err := svc.ReconcileBook(ctx, citableFixtureBookID)
	require.NoError(t, err)
	assert.Zero(t, rerun.Minted, "AC-1: no mints on unchanged source")
	assert.Zero(t, rerun.Superseded)
	assert.Zero(t, rerun.Tombstoned)
	assert.Zero(t, rerun.Updated)
	assert.Equal(t, 10, rerun.Matched)
	after := citableUnitsSnapshot(t, pool, citableFixtureBookID)
	assert.Equal(t, before, after, "AC-1: unit rows identical after re-run (ids, ordinals, timestamps)")

	// ---- AC-2: a published edit that splits a paragraph, via the REAL
	// editorial publish hook. ----
	var oldUnitID string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id FROM citable_units
		WHERE book_id = $1 AND text = $2 AND lifecycle = 'active'`,
		citableFixtureBookID, citableLongParagraph).Scan(&oldUnitID))

	actorID := bookImportUserID(citableFixtureBookID)
	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, username, email, password_hash)
		VALUES ($1, $2, $3, 'x') ON CONFLICT (id) DO NOTHING`,
		actorID, "citable-pilot-editor", "citable-pilot@example.test")
	require.NoError(t, err)

	pg := &postgres.Postgres{Pool: pool, Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar)}
	editorialUC := editorial.New(persistent.NewEditorialRepo(pg), svc, nil)

	editedPageTwo := "<span data-type='title' id=toc-10>الباب الأول</span>\n" +
		"فقرة أولى تحت الباب مستقرة\n" +
		"النصف الأول من الفقرة الطويلة بعد التقسيم\n" +
		"النصف الثاني من الفقرة الطويلة بعد التقسيم\n" +
		"<hr>\n" +
		"(١) حاشية بلا مرجع في المتن"
	_, err = editorialUC.SavePageDraft(ctx, actorID, entity.BookPageEdit{
		BookID:      citableFixtureBookID,
		PageID:      2,
		ContentHTML: editedPageTwo,
	}, nil, entity.EditOriginREST)
	require.NoError(t, err)
	_, err = editorialUC.PublishPageDraft(ctx, actorID, citableFixtureBookID, 2, nil)
	require.NoError(t, err)

	var lifecycle string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT lifecycle FROM citable_units WHERE id = $1`, oldUnitID).Scan(&lifecycle))
	assert.Equal(t, entity.UnitLifecycleSuperseded, lifecycle,
		"AC-2: the split paragraph's predecessor is superseded")

	assert.Equal(t, 2, countRows(t, pool, `
		SELECT count(*) FROM citable_unit_lineage l
		JOIN citable_units s ON s.id = l.successor_id
		WHERE l.predecessor_id = $1 AND s.lifecycle = 'active' AND l.reason = 'edit'`, oldUnitID),
		"AC-2: two lineage edges fan out to the two halves")

	resolution, err := svc.ResolveUnit(ctx, oldUnitID)
	require.NoError(t, err)
	assert.Equal(t, entity.UnitLifecycleSuperseded, resolution.Unit.Lifecycle)
	require.Len(t, resolution.Successors, 2, "AC-2: the old anchor still resolves, to both successors")

	for _, successor := range resolution.Successors {
		assert.Equal(t, entity.UnitLifecycleActive, successor.Lifecycle)
		assert.Contains(t, successor.Text, "من الفقرة الطويلة بعد التقسيم")
	}

	// The page's fallback-linked footnote is re-pointed to the new last body
	// block, and the edited page's units now carry editorial provenance.
	assert.Equal(t, 1, countRows(t, pool, `
		SELECT count(*) FROM citable_units f
		JOIN citable_units p ON p.id = f.parent_unit_id
		WHERE f.book_id = $1 AND f.kind = 'footnote' AND f.lifecycle = 'active'
		  AND p.lifecycle = 'active' AND p.text LIKE 'النصف الثاني%'`, citableFixtureBookID))
	assert.Equal(t, 2, countRows(t, pool, `
		SELECT count(*) FROM citable_units
		WHERE book_id = $1 AND lifecycle = 'active'
		  AND provenance_class = 'editorial'
		  AND provenance_detail->>'edit_actor_id' = $2
		  AND text LIKE 'النصف%'`, citableFixtureBookID, actorID))

	// ---- Review regression: a MATCHED footnote whose body is later deleted
	// becomes unlinked; its footnote_link must refresh to 'unlinked' (not keep a
	// stale 'fallback'), or the audit footnote_parent check false-positives on a
	// legitimate edit. Edit 1 mints the footnote linked; edit 2 removes the body
	// so the (text-unchanged) footnote matches and unlinks via the update path. ----
	const sharedFootnoteText = "حاشية مشتركة النص لا تتغير"

	_, err = editorialUC.SavePageDraft(ctx, actorID, entity.BookPageEdit{
		BookID: citableFixtureBookID, PageID: 3,
		ContentHTML: "متن ثالث سيحذف لاحقا\n__________\n(٥) " + sharedFootnoteText,
	}, nil, entity.EditOriginREST)
	require.NoError(t, err)
	_, err = editorialUC.PublishPageDraft(ctx, actorID, citableFixtureBookID, 3, nil)
	require.NoError(t, err)

	var initialLink string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT provenance_detail->>'footnote_link' FROM citable_units
		WHERE book_id = $1 AND page_id = 3 AND kind = 'footnote' AND lifecycle = 'active'`,
		citableFixtureBookID).Scan(&initialLink))
	assert.Equal(t, entity.FootnoteLinkFallback, initialLink, "footnote starts linked (fallback) to the body")

	// Edit 2: body gone, footnote text identical → footnote matches, unlinks.
	_, err = editorialUC.SavePageDraft(ctx, actorID, entity.BookPageEdit{
		BookID: citableFixtureBookID, PageID: 3,
		ContentHTML: "__________\n(٥) " + sharedFootnoteText,
	}, nil, entity.EditOriginREST)
	require.NoError(t, err)
	_, err = editorialUC.PublishPageDraft(ctx, actorID, citableFixtureBookID, 3, nil)
	require.NoError(t, err)

	var footnoteParent *string

	var footnoteLink string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT parent_unit_id, provenance_detail->>'footnote_link'
		FROM citable_units
		WHERE book_id = $1 AND page_id = 3 AND kind = 'footnote' AND lifecycle = 'active'`,
		citableFixtureBookID).Scan(&footnoteParent, &footnoteLink))
	assert.Nil(t, footnoteParent, "footnote with no body on its page is unlinked")
	assert.Equal(t, entity.FootnoteLinkUnlinked, footnoteLink, "footnote_link refreshed to 'unlinked' (not stale)")

	// ---- AC-4: the registry rejects any write outside the service. ----
	assertGuardRejects := func(sql string, args ...any) {
		t.Helper()

		_, execErr := pool.Exec(ctx, sql, args...)

		var pgErr *pgconn.PgError
		require.ErrorAs(t, execErr, &pgErr, "direct write must fail")
		assert.Equal(t, "42501", pgErr.Code, "insufficient_privilege from citable_registry_guard")
	}
	assertGuardRejects(`
		INSERT INTO citable_units (id, corpus, book_id, kind, ordinal, position, anchor,
			text, text_normalized, normalization_version, content_hash, occurrence, provenance_class)
		VALUES (gen_random_uuid(), 'kitab', $1, 'paragraph', 999, 0, 'kitab/x', 't', 't', 1, '\x00', 1, 'source')`,
		citableFixtureBookID)
	assertGuardRejects(`UPDATE citable_units SET text = 'tampered' WHERE id = $1`, oldUnitID)
	assertGuardRejects(`DELETE FROM citable_units WHERE id = $1`, oldUnitID)
	assertGuardRejects(`INSERT INTO citable_unit_lineage (predecessor_id, successor_id) VALUES ($1, $1)`, oldUnitID)

	// ---- AC-3: the audit reports zero violations... ----
	audit, err := svc.AuditPass(ctx)
	require.NoError(t, err)
	assert.Zero(t, audit.Violations.BookGone)
	assert.Zero(t, audit.Violations.SupersededNoSuccessor)
	assert.Zero(t, audit.Violations.ActiveWithSuccessor)
	assert.Zero(t, audit.Violations.HashMismatch)
	assert.Zero(t, audit.Violations.AnchorMalformed)
	assert.Zero(t, audit.Violations.FootnoteParent)

	// ...and catches a write that abused the escape hatch (hash tripwire).
	tamper := func(newText string) {
		t.Helper()

		tx, txErr := pool.Begin(ctx)
		require.NoError(t, txErr)
		_, txErr = tx.Exec(ctx, `SET LOCAL surau.registry_writer = 'unit-service'`)
		require.NoError(t, txErr)

		var unitID string
		require.NoError(t, tx.QueryRow(ctx, `
			SELECT id FROM citable_units
			WHERE book_id = $1 AND lifecycle = 'active' AND kind = 'paragraph'
			ORDER BY anchor LIMIT 1`, citableFixtureBookID).Scan(&unitID))
		_, txErr = tx.Exec(ctx,
			`UPDATE citable_units SET text = $2 WHERE id = $1`, unitID, newText)
		require.NoError(t, txErr)
		require.NoError(t, tx.Commit(ctx))
	}

	var originalText string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT text FROM citable_units
		WHERE book_id = $1 AND lifecycle = 'active' AND kind = 'paragraph'
		ORDER BY anchor LIMIT 1`, citableFixtureBookID).Scan(&originalText))

	tamper(originalText + " دخيل")

	audit, err = svc.AuditPass(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), audit.Violations.HashMismatch,
		"AC-4 detection layer: tampered text caught by hash recompute")

	tamper(originalText)

	audit, err = svc.AuditPass(ctx)
	require.NoError(t, err)
	assert.Zero(t, audit.Violations.HashMismatch)

	// Unknown unit id resolves to a typed error, not a silent empty result.
	_, err = svc.ResolveUnit(ctx, "00000000-0000-0000-0000-000000000000")
	require.True(t, errors.Is(err, entity.ErrUnitNotFound))
}
