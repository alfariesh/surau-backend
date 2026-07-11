//nolint:bodyclose,wsl_v5 // Stateful HTTP story closes bodies through assertion/decode helpers; SQL fixture grouping stays readable.
package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/goccy/go-json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	licenseFixturePriorityBookID = 991401
	licenseFixtureIdleBookID     = 991402
	licenseFixturePageID         = 1
	licenseFixtureHeadingID      = 1
	licenseFixtureRAGRunID       = "b4040200-0000-4000-8000-000000000001"
	licenseFixtureGeneratedText  = "Terjemahan mesin publik berlabel, bukan bukti RAG."
)

// TestKitabLicenseGovernance exercises B-4 as one stateful story: the audit
// queue reports real registered-reader signals, new publication is fail-closed,
// an evidence-backed permitted decision opens publication, and a later
// restricted decision removes every tested public retrieval path immediately.
func TestKitabLicenseGovernance(t *testing.T) {
	seedLicenseGovernanceBooks(t)

	adminToken := adminJWT(t)
	adminID := licenseJWTSubject(t, adminToken)
	seedLicenseReaderSignals(t, adminID)

	licenseAssertAuditCoverage(t, adminToken)

	licenseURL := fmt.Sprintf(
		"%s/v1/editorial/books/%d/license",
		baseURL(),
		licenseFixturePriorityBookID,
	)
	publicationURL := fmt.Sprintf(
		"%s/v1/editorial/books/%d/publication",
		baseURL(),
		licenseFixturePriorityBookID,
	)

	resp := doJSONWithIfMatch(t, http.MethodGet, licenseURL, nil, adminToken, "")
	var initial entity.BookLicense
	decodeAndClose(t, resp, &initial)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initial license GET expected 200, got %d: %+v", resp.StatusCode, initial)
	}
	if initial.LicenseStatus != entity.LicenseStatusUnknown {
		t.Fatalf("initial license status = %q, want unknown", initial.LicenseStatus)
	}

	initialETag := resp.Header.Get("ETag")
	if initialETag == "" {
		t.Fatal("initial license GET must return an ETag")
	}

	// A new unknown Edition cannot enter the public catalog.
	resp = doJSON(
		t,
		http.MethodPut,
		publicationURL,
		bytes.NewBufferString(`{"status":"published","featured":false}`),
		adminToken,
	)
	licenseAssertError(t, resp, http.StatusConflict, "license_not_permitted")

	// The production publisher is guarded by the same stable error even when
	// the project also lacks assets: license is the first hard precondition.
	projectID := licenseCreateProductionProject(t, adminToken)
	resp = doJSON(
		t,
		http.MethodPost,
		fmt.Sprintf("%s/v1/editorial/production-projects/%s/publish", baseURL(), projectID),
		nil,
		adminToken,
	)
	licenseAssertError(t, resp, http.StatusConflict, "license_not_permitted")

	decisionBody := bytes.NewBufferString(
		`{"license_status":"needs_review","reason":"Triage awal bukti lisensi"}`,
	)
	resp = doJSONWithIfMatch(
		t,
		http.MethodPatch,
		licenseURL,
		decisionBody,
		adminToken,
		"",
	)
	licenseAssertError(t, resp, http.StatusPreconditionRequired, "if_match_header_required")

	// The first conditional decision succeeds and rotates the ETag.
	decisionBody = bytes.NewBufferString(
		`{"license_status":"needs_review","reason":"Triage awal bukti lisensi"}`,
	)
	var needsReview entity.BookLicense

	resp = doJSONWithIfMatch(t, http.MethodPatch, licenseURL, decisionBody, adminToken, initialETag)
	decodeAndClose(t, resp, &needsReview)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("conditional license PATCH expected 200, got %d: %+v", resp.StatusCode, needsReview)
	}

	needsReviewETag := resp.Header.Get("ETag")
	if needsReviewETag == "" || needsReviewETag == initialETag {
		t.Fatalf("license PATCH must rotate ETag, got initial=%q next=%q", initialETag, needsReviewETag)
	}

	// Replaying the old ETag cannot overwrite the newer decision.
	decisionBody = bytes.NewBufferString(
		`{"license_status":"restricted","reason":"Keputusan dari penulis lain"}`,
	)
	resp = doJSONWithIfMatch(t, http.MethodPatch, licenseURL, decisionBody, adminToken, initialETag)
	licenseAssertError(t, resp, http.StatusPreconditionFailed, "precondition_failed")

	const (
		permitReason   = "Izin penerbit diterima dan diverifikasi"
		permitEvidence = "https://example.test/license/991401"
	)
	decisionBody = bytes.NewBufferString(fmt.Sprintf(
		`{"license_status":"permitted","reason":%q,"evidence_url":%q}`,
		permitReason,
		permitEvidence,
	))
	resp = doJSONWithIfMatch(t, http.MethodPatch, licenseURL, decisionBody, adminToken, needsReviewETag)
	var permitted entity.BookLicense
	decodeAndClose(t, resp, &permitted)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("permitted license PATCH expected 200, got %d: %+v", resp.StatusCode, permitted)
	}

	if permitted.LicenseStatus != entity.LicenseStatusPermitted {
		t.Fatalf("updated license status = %q, want permitted", permitted.LicenseStatus)
	}
	permittedETag := resp.Header.Get("ETag")
	if permittedETag == "" || permittedETag == needsReviewETag {
		t.Fatalf("permitted decision must rotate ETag, got previous=%q next=%q", needsReviewETag, permittedETag)
	}

	licenseAssertAuditRow(t, adminID, entity.LicenseStatusPermitted, permitReason, permitEvidence)

	// Literal permitted is sufficient for the same catalog request to succeed.
	resp = doJSON(
		t,
		http.MethodPut,
		publicationURL,
		bytes.NewBufferString(`{"status":"published","featured":false}`),
		adminToken,
	)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("permitted catalog publication expected 200, got %d", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/books/%d",
		baseURL(),
		licenseFixturePriorityBookID,
	), nil, "")
	var publicBook entity.Book
	decodeAndClose(t, resp, &publicBook)
	if resp.StatusCode != http.StatusOK || publicBook.LicenseStatus != entity.LicenseStatusPermitted {
		t.Fatalf("public book status/license = %d/%q, want 200/permitted", resp.StatusCode, publicBook.LicenseStatus)
	}
	if cacheControl := resp.Header.Get("Cache-Control"); cacheControl != "public, max-age=0, must-revalidate" {
		t.Fatalf("license-sensitive book cache policy = %q", cacheControl)
	}
	licenseAssertPublicStatus(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/anchors/resolve?anchor=%s",
		baseURL(),
		url.QueryEscape(fmt.Sprintf("kitab/%d", licenseFixturePriorityBookID)),
	), nil, http.StatusOK, "")
	licenseAssertGeneratedTranslationStaysLabelled(t, projectID)

	// O-1B-1 takedown: restricted hides the still-published catalog row. A
	// fresh ETag is mandatory so this policy change cannot race another audit.
	const restrictedReason = "Pemegang hak meminta konten diturunkan"
	decisionBody = bytes.NewBufferString(fmt.Sprintf(
		`{"license_status":"restricted","reason":%q}`,
		restrictedReason,
	))
	var restricted entity.BookLicense

	resp = doJSONWithIfMatch(t, http.MethodPatch, licenseURL, decisionBody, adminToken, permittedETag)
	decodeAndClose(t, resp, &restricted)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restricted license PATCH expected 200, got %d: %+v", resp.StatusCode, restricted)
	}
	if restricted.LicenseStatus != entity.LicenseStatusRestricted {
		t.Fatalf("updated license status = %q, want restricted", restricted.LicenseStatus)
	}

	licenseAssertPublicStatus(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/books/%d",
		baseURL(),
		licenseFixturePriorityBookID,
	), nil, http.StatusNotFound, "book_not_found")
	licenseAssertPublicStatus(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/anchors/resolve?anchor=%s",
		baseURL(),
		url.QueryEscape(fmt.Sprintf("kitab/%d", licenseFixturePriorityBookID)),
	), nil, http.StatusNotFound, "anchor_not_found")
	licenseAssertPublicStatus(t, http.MethodPost, fmt.Sprintf(
		"%s/v1/books/%d/rag",
		baseURL(),
		licenseFixturePriorityBookID,
	), bytes.NewBufferString(`{"question":"Apa isi kitab ini?"}`), http.StatusNotFound, "book_not_found")

	pool := integrationDB(t)
	defer pool.Close()

	var storedPublicationStatus string
	if err := pool.QueryRow(t.Context(), `
SELECT status
FROM book_publications
WHERE book_id = $1`, licenseFixturePriorityBookID).Scan(&storedPublicationStatus); err != nil {
		t.Fatalf("read stored publication after restriction: %v", err)
	}

	if storedPublicationStatus != entity.PublicationStatusPublished {
		t.Fatalf("restriction must hide through the public gate, stored publication = %q", storedPublicationStatus)
	}
}

func seedLicenseGovernanceBooks(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	t.Cleanup(func() {
		cleanupPool := integrationDB(t)
		defer cleanupPool.Close()

		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cleanupCancel()

		cleanupTx, cleanupErr := cleanupPool.Begin(cleanupCtx)
		if cleanupErr != nil {
			t.Errorf("begin cleanup B-4 license books: %v", cleanupErr)

			return
		}
		defer cleanupTx.Rollback(cleanupCtx)

		resetLicenseGovernanceBooks(cleanupCtx, t, cleanupTx)
		if cleanupErr = cleanupTx.Commit(cleanupCtx); cleanupErr != nil {
			t.Errorf("commit cleanup B-4 license books: %v", cleanupErr)
		}
	})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin B-4 license fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	resetLicenseGovernanceBooks(ctx, t, tx)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO books (id, name, metadata, has_content)
VALUES ($1, 'كتاب أولوية الترخيص', '{}'::jsonb, true),
       ($2, 'كتاب بلا نشاط', '{}'::jsonb, false)`, licenseFixturePriorityBookID, licenseFixtureIdleBookID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, $2, '<article><p>نص اختبار الترخيص.</p></article>', 'نص اختبار الترخيص.')`,
		licenseFixturePriorityBookID, licenseFixturePageID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content)
VALUES ($1, $2, $3, 0, 1, 'باب اختبار الترخيص')`,
		licenseFixturePriorityBookID, licenseFixtureHeadingID, licenseFixturePageID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id)
VALUES ($1, $2, $3, $3)`,
		licenseFixturePriorityBookID, licenseFixtureHeadingID, licenseFixturePageID)

	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit B-4 license fixture: %v", err)
	}
}

// resetLicenseGovernanceBooks removes only integration-owned evidence. The
// product audit remains append-only; replication mode is available here solely
// because the disposable PostgreSQL test role is its own superuser.
func resetLicenseGovernanceBooks(ctx context.Context, t *testing.T, tx pgx.Tx) {
	t.Helper()

	execFixtureSQL(t, ctx, tx, `SET LOCAL session_replication_role = 'replica'`)
	execFixtureSQL(t, ctx, tx, `
DELETE FROM book_license_audits
WHERE book_id IN ($1, $2)`, licenseFixturePriorityBookID, licenseFixtureIdleBookID)
	execFixtureSQL(t, ctx, tx, `SET LOCAL session_replication_role = 'origin'`)
	execFixtureSQL(t, ctx, tx, `
DELETE FROM books
WHERE id IN ($1, $2)`, licenseFixturePriorityBookID, licenseFixtureIdleBookID)
}

func seedLicenseReaderSignals(t *testing.T, userID string) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	// Keep the registered-reader metrics deterministic even when the integration
	// database volume is reused across local runs.
	if _, err := pool.Exec(ctx, `DELETE FROM reading_progress WHERE book_id IN ($1, $2)`,
		licenseFixturePriorityBookID, licenseFixtureIdleBookID); err != nil {
		t.Fatalf("reset registered reading progress: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM saved_items WHERE book_id IN ($1, $2)`,
		licenseFixturePriorityBookID, licenseFixtureIdleBookID); err != nil {
		t.Fatalf("reset registered saved items: %v", err)
	}

	if _, err := pool.Exec(ctx, `
INSERT INTO reading_progress (user_id, book_id, page_id, heading_id, progress_percent, updated_at)
VALUES ($1, $2, $3, $4, 25, now())
ON CONFLICT (user_id, book_id) DO UPDATE SET
    page_id = EXCLUDED.page_id,
    heading_id = EXCLUDED.heading_id,
    progress_percent = EXCLUDED.progress_percent,
    updated_at = EXCLUDED.updated_at`,
		userID, licenseFixturePriorityBookID, licenseFixturePageID, licenseFixtureHeadingID); err != nil {
		t.Fatalf("seed registered reading progress: %v", err)
	}

	if _, err := pool.Exec(ctx, `
INSERT INTO saved_items (id, user_id, item_type, book_id, page_id, label)
VALUES ($1, $2, 'book_page', $3, $4, 'Bukti prioritas audit')`,
		uuid.NewString(), userID, licenseFixturePriorityBookID, licenseFixturePageID); err != nil {
		t.Fatalf("seed registered saved item: %v", err)
	}
}

func licenseAssertAuditCoverage(t *testing.T, token string) {
	t.Helper()

	var report entity.BookLicenseAuditReport

	resp := doJSON(
		t,
		http.MethodGet,
		baseURL()+"/v1/editorial/license-audit?limit=200",
		nil,
		token,
	)
	decodeAndClose(t, resp, &report)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("license audit report expected 200, got %d: %+v", resp.StatusCode, report)
	}

	if report.Counts.Total < 2 || report.Counts.Unknown < 2 || report.Counts.Unresolved < 2 {
		t.Fatalf("license coverage counts do not include both unknown fixtures: %+v", report.Counts)
	}

	priorityIndex, idleIndex := -1, -1
	var priority, idle entity.BookLicenseAuditItem

	for index := range report.Items {
		item := &report.Items[index]
		switch item.BookID {
		case licenseFixturePriorityBookID:
			priorityIndex = index
			priority = *item
		case licenseFixtureIdleBookID:
			idleIndex = index
			idle = *item
		}
	}
	if priorityIndex < 0 || idleIndex < 0 {
		t.Fatalf("license audit queue omitted fixtures: priority=%d idle=%d total=%d", priorityIndex, idleIndex, report.Total)
	}
	if priorityIndex >= idleIndex {
		t.Fatalf("active registered-reader book must outrank idle book: priority=%d idle=%d", priorityIndex, idleIndex)
	}
	if priority.RegisteredReaderCount != 1 || priority.SavedItemCount != 1 || priority.LastActivityAt == nil {
		t.Fatalf("priority signals must reflect registered rows exactly: %+v", priority)
	}
	if idle.RegisteredReaderCount != 0 || idle.SavedItemCount != 0 || idle.LastActivityAt != nil {
		t.Fatalf("idle book must not receive fabricated usage: %+v", idle)
	}
}

func licenseCreateProductionProject(t *testing.T, token string) string {
	t.Helper()

	resp := doJSON(
		t,
		http.MethodPost,
		baseURL()+"/v1/editorial/production-projects",
		bytes.NewBufferString(fmt.Sprintf(
			`{"book_id":%d,"lang":"en","requires_review":false,"requires_audio":false}`,
			licenseFixturePriorityBookID,
		)),
		token,
	)
	var project struct {
		ID string `json:"id"`
	}
	decodeAndClose(t, resp, &project)
	if resp.StatusCode != http.StatusCreated || project.ID == "" {
		t.Fatalf("create non-permitted production project expected 201, got %d: %+v", resp.StatusCode, project)
	}

	return project.ID
}

func licenseAssertGeneratedTranslationStaysLabelled(t *testing.T, projectID string) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin generated reader fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `
INSERT INTO generation_runs (id, task_name, model_id, prompt_version, metadata)
VALUES ($1, 'reader-translation', 'integration-machine-model', 'reader-translation-v1',
        '{"fixture":"b4-generated-reader"}'::jsonb)
ON CONFLICT (id) DO NOTHING`, licenseFixtureRAGRunID)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO section_translations (
    book_id, heading_id, lang, title, content, translation_status,
    provenance_class, generation_run_id
)
VALUES ($1, $2, 'en', 'Generated reader heading', $3, 'generated', 'machine', $4)`,
		licenseFixturePriorityBookID,
		licenseFixtureHeadingID,
		licenseFixtureGeneratedText,
		licenseFixtureRAGRunID,
	)
	execFixtureSQL(t, ctx, tx, `
UPDATE book_production_projects
SET workflow_status = 'published',
    publication_status = 'published',
    published_at = now(),
    updated_at = now()
WHERE id = $1`, projectID)

	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit generated reader fixture: %v", err)
	}

	resp := doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/books/%d/toc/%d/read?lang=en",
		baseURL(),
		licenseFixturePriorityBookID,
		licenseFixtureHeadingID,
	), nil, "")
	var read struct {
		Translation *struct {
			Content string `json:"content"`
			Status  string `json:"translation_status"`
		} `json:"translation"`
	}
	decodeAndClose(t, resp, &read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generated reader translation expected 200, got %d", resp.StatusCode)
	}
	if read.Translation == nil ||
		read.Translation.Content != licenseFixtureGeneratedText ||
		read.Translation.Status != "generated" {
		t.Fatalf("generated reader translation lost content/status label: %+v", read.Translation)
	}
}

func licenseAssertAuditRow(
	t *testing.T,
	actorID,
	status,
	reason,
	evidenceURL string,
) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	var (
		storedActor    string
		storedReason   string
		storedEvidence string
	)
	if err := pool.QueryRow(ctx, `
SELECT actor_id::text, reason, evidence_url
FROM book_license_audits
WHERE book_id = $1 AND new_status = $2
ORDER BY created_at DESC
LIMIT 1`, licenseFixturePriorityBookID, status).Scan(
		&storedActor,
		&storedReason,
		&storedEvidence,
	); err != nil {
		t.Fatalf("read persisted license audit row: %v", err)
	}
	if storedActor != actorID || storedReason != reason || storedEvidence != evidenceURL {
		t.Fatalf(
			"license audit attribution/evidence mismatch: actor=%q reason=%q evidence=%q",
			storedActor,
			storedReason,
			storedEvidence,
		)
	}
}

func licenseAssertError(t *testing.T, resp *http.Response, status int, code string) {
	t.Helper()

	var body struct {
		Code string `json:"code"`
	}
	decodeAndClose(t, resp, &body)
	if resp.StatusCode != status || body.Code != code {
		t.Fatalf("error response status/code = %d/%q, want %d/%q", resp.StatusCode, body.Code, status, code)
	}
}

func licenseAssertPublicStatus(
	t *testing.T,
	method,
	target string,
	body *bytes.Buffer,
	status int,
	code string,
) {
	t.Helper()

	resp := doJSON(t, method, target, body, "")
	if code == "" {
		resp.Body.Close()
		if resp.StatusCode != status {
			t.Fatalf("%s %s status = %d, want %d", method, target, resp.StatusCode, status)
		}

		return
	}

	licenseAssertError(t, resp, status, code)
}

func licenseJWTSubject(t *testing.T, token string) string {
	t.Helper()

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("admin token is not a JWT")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode admin JWT payload: %v", err)
	}
	var claims struct {
		Subject string `json:"sub"`
	}
	if err = json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("decode admin JWT claims: %v", err)
	}
	if claims.Subject == "" {
		t.Fatal("admin JWT has no subject")
	}

	return claims.Subject
}
