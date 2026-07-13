package v1

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/policy"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/usecase"
	editorialuc "github.com/alfariesh/surau-backend/internal/usecase/editorial"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	k1EditorialLiveBookID = 990213
	k1EditorialLiveActor  = "c1010000-0000-4000-8000-000000000041"
)

type staleObservingReconciler struct {
	delegate *unitregistry.UseCase
	pool     *pgxpool.Pool
	observed bool
}

func (r *staleObservingReconciler) ReconcileBookIfDerived(
	ctx context.Context,
	bookID int,
) (entity.UnitReconcileReport, bool, error) {
	var (
		stale       bool
		publicUnits int
	)

	if err := r.pool.QueryRow(ctx, `SELECT units_stale_at IS NOT NULL FROM books WHERE id = $1`, bookID).
		Scan(&stale); err != nil {
		return entity.UnitReconcileReport{}, false, err
	}

	if err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM public_book_interpretive_citable_units WHERE book_id = $1`, bookID).
		Scan(&publicUnits); err != nil {
		return entity.UnitReconcileReport{}, false, err
	}

	r.observed = r.observed || (stale && publicUnits == 0)

	return r.delegate.ReconcileBookIfDerived(ctx, bookID)
}

// TestLiveEditorialETagReconcileKeepsAnchors proves the public ETag workflow,
// same-transaction stale gate, format-only identity, and split/merge lineage
// together against PostgreSQL instead of isolated fakes.
//
//nolint:maintidx,paralleltest,wsl_v5 // serial end-to-end fixture owns one catalog book
func TestLiveEditorialETagReconcileKeepsAnchors(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	ctx := context.Background()
	cleanupK1EditorialLive(t, pg.Pool)
	t.Cleanup(func() { cleanupK1EditorialLive(t, pg.Pool) })
	seedK1EditorialLive(t, pg.Pool)

	unitRepo := persistent.NewCitableUnitRepo(pg)
	registry := unitregistry.New(unitRepo)
	_, err = registry.ReconcileBook(ctx, k1EditorialLiveBookID)
	require.NoError(t, err)

	var originalID, originalAnchor string
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT id::text, anchor
FROM citable_units
WHERE book_id = $1 AND lifecycle = 'active' AND content_role = 'book_page'
ORDER BY position, ordinal LIMIT 1`, k1EditorialLiveBookID).Scan(&originalID, &originalAnchor))

	observer := &staleObservingReconciler{delegate: registry, pool: pg.Pool}
	editorial := editorialuc.New(persistent.NewEditorialRepo(pg), observer, logger.New("error"))
	app := newK1EditorialLiveApp(editorial)

	// Formatting-only: actual GET ETag -> draft PUT If-Match -> publish POST
	// If-Match. Text is unchanged, so the issued UUID/Anchor must remain active.
	publishK1PageEdit(t, app, `<strong>نص ثابت</strong>`)
	assert.True(t, observer.observed, "publish must hide stale units before synchronous shape reconcile")

	var formatID, formatHTML string
	var formatDetail string
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT id::text, COALESCE(html, ''), provenance_detail::text
FROM citable_units
WHERE book_id = $1 AND lifecycle = 'active' AND content_role = 'book_page'
ORDER BY position, ordinal LIMIT 1`, k1EditorialLiveBookID).Scan(&formatID, &formatHTML, &formatDetail))
	assert.Equal(t, originalID, formatID)
	assert.Contains(t, formatHTML, "<strong>")
	assert.Contains(t, formatDetail, "format_edit_actor_id")

	anchorRepo := persistent.NewAnchorRepo(pg)
	formatResolution, err := anchorRepo.ResolveCanonicalUnit(ctx, originalAnchor)
	require.NoError(t, err)
	assert.Equal(t, entity.UnitLifecycleActive, formatResolution.Status)
	require.Len(t, formatResolution.ActiveRecords, 1)
	require.NotNil(t, formatResolution.ActiveRecords[0].UnitID)
	assert.Equal(t, originalID, *formatResolution.ActiveRecords[0].UnitID)

	// Text split: the old citation becomes superseded and resolves to both
	// exact current successors instead of dangling.
	publishK1PageEdit(t, app, `<p>نص أول.</p><p>نص ثان.</p>`)
	splitResolution, err := anchorRepo.ResolveCanonicalUnit(ctx, originalAnchor)
	require.NoError(t, err)
	assert.Equal(t, entity.UnitLifecycleSuperseded, splitResolution.Status)
	require.Len(t, splitResolution.ActiveRecords, 2)
	assert.NotEmpty(t, splitResolution.RedirectChain)

	var splitAnchor string
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT anchor FROM citable_units
WHERE book_id = $1 AND lifecycle = 'active' AND content_role = 'book_page'
ORDER BY position, ordinal LIMIT 1`, k1EditorialLiveBookID).Scan(&splitAnchor))

	// Text merge: either split Anchor must resolve to the single merged unit.
	publishK1PageEdit(t, app, `<p>نص أول ونص ثان.</p>`)
	mergeResolution, err := anchorRepo.ResolveCanonicalUnit(ctx, splitAnchor)
	require.NoError(t, err)
	assert.Equal(t, entity.UnitLifecycleSuperseded, mergeResolution.Status)
	require.Len(t, mergeResolution.ActiveRecords, 1)
	assert.False(t, mergeResolution.CycleDetected)
}

func newK1EditorialLiveApp(editorial usecase.Editorial) *fiber.App {
	app := fiber.New()
	user := &fakeAuthUser{}
	controller := &V1{
		u:         user,
		editorial: editorial,
		l:         logger.New("error"),
		v:         validator.New(validator.WithRequiredStructEnabled()),
	}
	injectActor := func(ctx *fiber.Ctx) error {
		actor := entity.User{ID: k1EditorialLiveActor, Role: entity.UserRoleEditor}
		ctx.Locals("user", actor)
		ctx.Locals("userID", actor.ID)

		return ctx.Next()
	}
	group := app.Group("/v1/editorial", injectActor,
		middleware.RequireCapability(user, policy.CapReviewEditorial))
	group.Get("/books/:book_id/pages/:page_id", controller.editorialGetPageEdit)
	group.Put("/books/:book_id/pages/:page_id/draft", controller.editorialSavePageDraft)
	group.Post("/books/:book_id/pages/:page_id/publish", controller.editorialPublishPageDraft)

	return app
}

func publishK1PageEdit(t *testing.T, app *fiber.App, contentHTML string) {
	t.Helper()

	path := fmt.Sprintf("/v1/editorial/books/%d/pages/1", k1EditorialLiveBookID)
	get := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	getResponse, err := app.Test(get)
	require.NoError(t, err)
	require.NoError(t, getResponse.Body.Close())
	require.Equal(t, http.StatusOK, getResponse.StatusCode)
	etag := getResponse.Header.Get("ETag")
	require.NotEmpty(t, etag)

	body := fmt.Sprintf(`{"content_html":%q}`, contentHTML)
	put := httptest.NewRequestWithContext(t.Context(), http.MethodPut, path+"/draft", strings.NewReader(body))
	put.Header.Set("Content-Type", "application/json")
	put.Header.Set("If-Match", etag)
	putResponse, err := app.Test(put)
	require.NoError(t, err)
	require.NoError(t, putResponse.Body.Close())
	require.Equal(t, http.StatusOK, putResponse.StatusCode)
	draftETag := putResponse.Header.Get("ETag")
	require.NotEmpty(t, draftETag)

	publish := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path+"/publish", http.NoBody)
	publish.Header.Set("If-Match", draftETag)
	publishResponse, err := app.Test(publish)
	require.NoError(t, err)
	require.NoError(t, publishResponse.Body.Close())
	require.Equal(t, http.StatusOK, publishResponse.StatusCode)
}

func seedK1EditorialLive(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(t.Context()) }()

	_, err = tx.Exec(t.Context(), `
INSERT INTO users (id, username, email, password_hash, role)
VALUES ($1, 'k1-editorial-live', 'k1-editorial-live@example.test', 'x', 'editor')`, k1EditorialLiveActor)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
INSERT INTO books (id, name, has_content)
VALUES ($1, 'كتاب اختبار تحرير K-1', TRUE)`, k1EditorialLiveBookID)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
UPDATE books
SET license_status = 'permitted', license_reason = 'K-1 live ETag fixture', license_updated_by = $2
WHERE id = $1`, k1EditorialLiveBookID, k1EditorialLiveActor)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
INSERT INTO book_publications (book_id, status, published_at)
VALUES ($1, 'published', now())`, k1EditorialLiveBookID)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>نص ثابت</p>', 'نص ثابت')`, k1EditorialLiveBookID)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
INSERT INTO book_headings (book_id, heading_id, page_id, ordinal, content)
VALUES ($1, 1, 1, 1, 'باب الاختبار')`, k1EditorialLiveBookID)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id)
VALUES ($1, 1, 1, 1)`, k1EditorialLiveBookID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(t.Context()))
}

func cleanupK1EditorialLive(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM book_license_audits WHERE book_id = $1`, k1EditorialLiveBookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'origin'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM books WHERE id = $1`, k1EditorialLiveBookID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, k1EditorialLiveActor)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
}
