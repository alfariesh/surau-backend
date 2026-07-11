package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const requestTimeout = 20 * time.Second

const (
	fixtureCategoryID = 990001
	fixtureAuthorID   = 990001
	fixtureBookID     = 990001
	fixtureHeadingID  = 990001
)

func baseURL() string {
	if value := os.Getenv("INTEGRATION_HTTP_URL"); value != "" {
		return strings.TrimRight(value, "/")
	}

	return "http://app:8080"
}

func TestMain(m *testing.M) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		os.Exit(0)
	}

	if err := waitForApp(); err != nil {
		fmt.Fprintf(os.Stderr, "integration-test: app never became ready: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

var errProbeStatus = errors.New("probe expected 200")

// waitForApp blocks until the app accepts HTTP and reports ready, or the
// readiness budget elapses. Compose `depends_on` only orders container starts;
// nothing guarantees the listener is up before the suite fires its first
// request, and a refused connection failed the whole run — the root cause of
// the flakiness the CI retry loop used to mask (F1-E).
func waitForApp() error {
	const (
		readinessBudget = 90 * time.Second
		pollInterval    = 500 * time.Millisecond
	)

	var lastErr error

	for deadline := time.Now().Add(readinessBudget); time.Now().Before(deadline); time.Sleep(pollInterval) {
		// /healthz proves the listener is up; /readyz proves the DB behind it
		// answers. Both must pass so tests never race a half-booted stack.
		if lastErr = probeOK(baseURL() + "/healthz"); lastErr != nil {
			continue
		}

		if lastErr = probeOK(baseURL() + "/readyz"); lastErr == nil {
			return nil
		}
	}

	return lastErr
}

func probeOK(target string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: %s returned %d", errProbeStatus, target, resp.StatusCode)
	}

	return nil
}

func TestReaderRESTSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL()+"/healthz", http.NoBody)
	if err != nil {
		t.Fatalf("health request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health expected 200, got %d", resp.StatusCode)
	}

	// /version reports name/version/env so deploys can be verified per environment.
	versionResp := doJSON(t, http.MethodGet, baseURL()+"/version", nil, "")

	var version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Env     string `json:"env"`
	}

	decodeAndClose(t, versionResp, &version)

	if versionResp.StatusCode != http.StatusOK {
		t.Fatalf("version expected 200, got %d", versionResp.StatusCode)
	}

	if version.Name == "" || version.Version == "" || version.Env == "" {
		t.Fatalf("version fields must be populated, got %+v", version)
	}

	for _, path := range []string{"/v1/categories", "/v1/authors", "/v1/books"} {
		resp = doJSON(t, http.MethodGet, baseURL()+path, nil, "")
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s expected 200, got %d", path, resp.StatusCode)
		}
	}

	email := fmt.Sprintf("reader_%d@test.local", time.Now().UnixNano())
	registerBody := fmt.Sprintf(`{"username":"reader_%d","email":%q,"password":"testpass123"}`, time.Now().UnixNano(), email)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/register", bytes.NewBufferString(registerBody), "")
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register expected 201, got %d", resp.StatusCode)
	}
	verifyRegisteredEmail(t, email)

	loginBody := fmt.Sprintf(`{"email":%q,"password":"testpass123"}`, email)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(loginBody), "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login expected 200, got %d", resp.StatusCode)
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tokenResp.Token == "" {
		t.Fatal("expected token")
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/user/profile", nil, tokenResp.Token)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile expected 200, got %d", resp.StatusCode)
	}
}

func TestReaderMultilingualKitabContract(t *testing.T) {
	seedMultilingualKitabFixture(t)

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books/%d?lang=fr", baseURL(), fixtureBookID), nil, "")
	var errorBody struct {
		Error string `json:"error"`
	}
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported language expected 400, got %d", resp.StatusCode)
	}
	if errorBody.Error != "unsupported language" {
		t.Fatalf("unsupported language error = %q", errorBody.Error)
	}

	enBook := getBook(t, "en")
	if enBook.Name != "كتاب الاختبار" {
		t.Fatalf("en missing catalog should display Arabic name, got %q", enBook.Name)
	}
	assertLocalization(t, enBook.Localization, "en", "ar", true)
	assertHasLang(t, enBook.Localization.AvailableLangs, "id")
	if got := enBook.Localization.FieldLangs["name"]; got != "ar" {
		t.Fatalf("en missing catalog name field language = %q", got)
	}
	assertCoverage(t, enBook.LanguageCoverage, "id", 1, 1, 1)
	assertCoverage(t, enBook.LanguageCoverage, "ar", 0, 1, 0)

	idBook := getBook(t, "id")
	if idBook.Name != "Kitab Fixture Indonesia" {
		t.Fatalf("id catalog title = %q", idBook.Name)
	}
	assertLocalization(t, idBook.Localization, "id", "id", false)
	if idBook.CategoryName == nil || *idBook.CategoryName != "Kategori Fixture" {
		t.Fatalf("id category name = %v", idBook.CategoryName)
	}
	if idBook.AuthorName == nil || *idBook.AuthorName != "Penulis Fixture" {
		t.Fatalf("id author name = %v", idBook.AuthorName)
	}

	searchResult := searchBooks(t, "Kitab Fixture Indonesia", "en")
	found := findBook(searchResult.Books, fixtureBookID)
	if found == nil {
		t.Fatalf("expected search to match any catalog translation")
	}
	if found.Name != "كتاب الاختبار" {
		t.Fatalf("search display should still follow en-or-Arabic fallback, got %q", found.Name)
	}

	enTOC := getTOC(t, "en")
	if len(enTOC) != 1 {
		t.Fatalf("en toc length = %d", len(enTOC))
	}
	assertTOCNode(t, enTOC[0], "en", "ar", true, true, "ar")

	idTOC := getTOC(t, "id")
	if len(idTOC) != 1 {
		t.Fatalf("id toc length = %d", len(idTOC))
	}
	assertTOCNode(t, idTOC[0], "id", "id", false, false, "id")
	if idTOC[0].Title != "Bab Fixture Indonesia" {
		t.Fatalf("id toc title = %q", idTOC[0].Title)
	}

	enRead := getTOCRead(t, "en")
	if enRead.Translation != nil {
		t.Fatalf("en missing section translation should be null")
	}
	if !enRead.TranslationMissing {
		t.Fatal("en missing section translation should set translation_missing=true")
	}
	if enRead.TitleLang != "ar" || !enRead.IsTitleFallback {
		t.Fatalf("en read title fallback = lang %q fallback %v", enRead.TitleLang, enRead.IsTitleFallback)
	}
	assertHasLang(t, enRead.AvailableTranslationLangs, "id")
	assertAvailability(t, enRead.Availability.Translation, "offer_available_lang", "en", "ar", true)
	assertAvailability(t, enRead.Availability.Summary, "offer_available_lang", "en", "ar", true)
	assertAvailability(t, enRead.Availability.Audio, "offer_available_lang", "en", "ar", true)

	idRead := getTOCRead(t, "id")
	if idRead.Translation == nil {
		t.Fatal("id read should include exact translation")
	}
	if idRead.Translation.Lang != "id" || idRead.Translation.Content != "Konten terjemahan Indonesia" {
		t.Fatalf("id translation = %+v", idRead.Translation)
	}
	if idRead.TranslationMissing {
		t.Fatal("id exact translation should not be marked missing")
	}
	assertAvailability(t, idRead.Availability.Translation, "show_requested", "id", "id", false)
	assertAvailability(t, idRead.Availability.Summary, "show_requested", "id", "id", false)
	assertAvailability(t, idRead.Availability.Audio, "show_requested", "id", "id", false)

	arRead := getTOCRead(t, "ar")
	if arRead.Translation != nil {
		t.Fatalf("ar read should suppress translation, got %+v", arRead.Translation)
	}
	if arRead.TranslationMissing {
		t.Fatal("ar read should not be marked translation_missing")
	}
	if arRead.TitleLang != "ar" || arRead.IsTitleFallback {
		t.Fatalf("ar read title metadata = lang %q fallback %v", arRead.TitleLang, arRead.IsTitleFallback)
	}
	assertAvailability(t, arRead.Availability.Translation, "hide_translation_tab", "ar", "ar", false)

	feedbackBody := bytes.NewBufferString(`{"vote":"like","client_id":"missing-en-client"}`)
	resp = doJSON(t, http.MethodPost, fmt.Sprintf(
		"%s/v1/books/%d/toc/%d/translation-feedback?lang=en",
		baseURL(),
		fixtureBookID,
		fixtureHeadingID,
	), feedbackBody, "")
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing en feedback expected 404, got %d", resp.StatusCode)
	}
	if errorBody.Error != "translation not found" {
		t.Fatalf("missing en feedback error = %q", errorBody.Error)
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/editorial/reader/missing-assets?target_lang=en", nil, "")
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin missing queue without auth expected 401, got %d", resp.StatusCode)
	}

	adminToken := adminJWT(t)
	resp = doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/editorial/reader/missing-assets?target_lang=en&book_id=%d",
		baseURL(),
		fixtureBookID,
	), nil, adminToken)
	var allMissing missingAssetsResponse
	decodeAndClose(t, resp, &allMissing)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin missing all assets expected 200, got %d", resp.StatusCode)
	}
	if allMissing.Total != 6 {
		t.Fatalf("admin missing all assets total = %d items %+v", allMissing.Total, allMissing.Items)
	}
	for _, assetType := range []string{
		"author_metadata",
		"book_metadata",
		"category_metadata",
		"heading_summary",
		"section_audio",
		"section_translation",
	} {
		assertMissingCount(t, allMissing.Counts, assetType, "en", 1)
	}

	resp = doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/editorial/reader/missing-assets?target_lang=en&asset_type=section_translation&book_id=%d",
		baseURL(),
		fixtureBookID,
	), nil, adminToken)
	var missing missingAssetsResponse
	decodeAndClose(t, resp, &missing)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin missing section translations expected 200, got %d", resp.StatusCode)
	}
	if missing.Total != 1 || len(missing.Items) != 1 {
		t.Fatalf("admin missing section translations = total %d items %+v", missing.Total, missing.Items)
	}
	item := missing.Items[0]
	if item.AssetType != "section_translation" || item.TargetLang != "en" {
		t.Fatalf("admin missing item type/lang = %+v", item)
	}
	if item.BookID == nil || *item.BookID != fixtureBookID || item.HeadingID == nil || *item.HeadingID != fixtureHeadingID {
		t.Fatalf("admin missing item ids = %+v", item)
	}
	assertHasLang(t, item.AvailableLangs, "id")

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/editorial/reader/missing-assets?target_lang=ar", nil, adminToken)
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("admin missing queue target_lang=ar expected 400, got %d", resp.StatusCode)
	}
	if errorBody.Error != "unsupported language" {
		t.Fatalf("admin missing queue ar error = %q", errorBody.Error)
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/editorial/reader/missing-assets?target_lang=en&asset_type=metadata", nil, adminToken)
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("admin missing queue invalid asset_type expected 400, got %d", resp.StatusCode)
	}
	if errorBody.Error != "invalid asset_type" {
		t.Fatalf("admin missing queue invalid asset_type error = %q", errorBody.Error)
	}
}

func doJSON(t *testing.T, method, url string, body *bytes.Buffer, token string) *http.Response {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	var reqBody io.Reader = http.NoBody
	if body != nil {
		reqBody = body
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	return resp
}

func decodeAndClose(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("decode %s %s: %v", resp.Request.Method, resp.Request.URL.String(), err)
	}
}

func integrationDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("INTEGRATION_PG_URL")
	if dbURL == "" {
		dbURL = "postgres://user:myAwEsOm3pa55@w0rd@db:5432/db?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect integration db: %v", err)
	}

	return pool
}

func verifyRegisteredEmail(t *testing.T, email string) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tag, err := pool.Exec(ctx, `
UPDATE users
SET email_verified = true,
    email_verified_at = now()
WHERE email = $1`, email)
	if err != nil {
		t.Fatalf("verify registered email: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("verify registered email affected %d rows", tag.RowsAffected())
	}
}

func setUserRoleByEmail(t *testing.T, email, role string) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tag, err := pool.Exec(ctx, `
UPDATE users
SET role = $2,
    updated_at = now()
WHERE email = $1`, email, role)
	if err != nil {
		t.Fatalf("set user role: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("set user role affected %d rows", tag.RowsAffected())
	}
}

func adminJWT(t *testing.T) string {
	t.Helper()

	nano := time.Now().UnixNano()
	email := fmt.Sprintf("admin_%d@test.local", nano)
	registerBody := fmt.Sprintf(`{"username":"admin_%d","email":%q,"password":"testpass123"}`, nano, email)
	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/register", bytes.NewBufferString(registerBody), "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("admin register expected 201, got %d", resp.StatusCode)
	}

	verifyRegisteredEmail(t, email)
	setUserRoleByEmail(t, email, "admin")

	loginBody := fmt.Sprintf(`{"email":%q,"password":"testpass123"}`, email)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(loginBody), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin login expected 200, got %d", resp.StatusCode)
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode admin token: %v", err)
	}
	if tokenResp.Token == "" {
		t.Fatal("expected admin token")
	}

	return tokenResp.Token
}

func seedMultilingualKitabFixture(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fixture tx: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `DELETE FROM book_metadata_translations WHERE book_id = $1 AND lang = 'en'`, fixtureBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM category_translations WHERE category_id = $1 AND lang = 'en'`, fixtureCategoryID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM author_translations WHERE author_id = $1 AND lang = 'en'`, fixtureAuthorID)
	execFixtureSQL(
		t, ctx, tx, `DELETE FROM section_translations WHERE book_id = $1 AND heading_id = $2 AND lang = 'en'`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(
		t, ctx, tx, `DELETE FROM section_audio WHERE book_id = $1 AND heading_id = $2 AND lang = 'en'`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(
		t, ctx, tx, `DELETE FROM book_heading_summaries WHERE book_id = $1 AND heading_id = $2 AND lang = 'en'`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(t, ctx, tx, `DELETE FROM book_production_projects WHERE book_id = $1 AND lang = 'id'`, fixtureBookID)

	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO categories (id, name, display_order)
VALUES ($1, 'التصنيف الاختباري', 1)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, display_order = EXCLUDED.display_order`,
		fixtureCategoryID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO authors (id, name, biography, death_text, death_number)
VALUES ($1, 'مؤلف الاختبار', 'سيرة عربية للاختبار', '1445 هـ', 1445)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    death_number = EXCLUDED.death_number`,
		fixtureAuthorID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO books (
    id, name, category_id, author_id, type, printed, minor_release, major_release,
    bibliography, hint, pdf_links, metadata, source_date, has_content
)
VALUES ($1, 'كتاب الاختبار', $2, $3, 1, 1, 0, 1, 'ببليوغرافيا عربية', 'تلميح عربي', '{}'::jsonb, '{}'::jsonb, '14450101', true)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    category_id = EXCLUDED.category_id,
    author_id = EXCLUDED.author_id,
    bibliography = EXCLUDED.bibliography,
    hint = EXCLUDED.hint,
    has_content = EXCLUDED.has_content`,
		fixtureBookID,
		fixtureCategoryID,
		fixtureAuthorID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_publications (book_id, status, featured, sort_order, published_at)
VALUES ($1, 'published', true, 1, now())
ON CONFLICT (book_id) DO UPDATE SET
    status = EXCLUDED.status,
    featured = EXCLUDED.featured,
    sort_order = EXCLUDED.sort_order,
    published_at = EXCLUDED.published_at`,
		fixtureBookID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<article><p>نص عربي أصلي للاختبار.</p></article>', 'نص عربي أصلي للاختبار.')
ON CONFLICT (book_id, page_id) DO UPDATE SET
    content_html = EXCLUDED.content_html,
    content_text = EXCLUDED.content_text`,
		fixtureBookID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_headings (book_id, heading_id, parent_id, page_id, depth, ordinal, content)
VALUES ($1, $2, NULL, 1, 0, 1, 'باب الاختبار')
ON CONFLICT (book_id, heading_id) DO UPDATE SET
    page_id = EXCLUDED.page_id,
    depth = EXCLUDED.depth,
    ordinal = EXCLUDED.ordinal,
    content = EXCLUDED.content`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id)
VALUES ($1, $2, 1, 1)
ON CONFLICT (book_id, heading_id) DO UPDATE SET
    start_page_id = EXCLUDED.start_page_id,
    end_page_id = EXCLUDED.end_page_id`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO category_translations (category_id, lang, name, source, provenance_class)
VALUES ($1, 'id', 'Kategori Fixture', 'integration-test', 'editorial')
ON CONFLICT (category_id, lang) DO UPDATE SET
    name = EXCLUDED.name,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL`,
		fixtureCategoryID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO author_translations (author_id, lang, name, biography, death_text, source, provenance_class)
VALUES ($1, 'id', 'Penulis Fixture', 'Biografi fixture', '1445 H', 'integration-test', 'editorial')
ON CONFLICT (author_id, lang) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL`,
		fixtureAuthorID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_metadata_translations (
    book_id, lang, display_title, bibliography, hint, description, source, provenance_class
)
VALUES (
    $1, 'id', 'Kitab Fixture Indonesia', 'Bibliografi fixture', 'Hint fixture',
    'Deskripsi fixture', 'integration-test', 'editorial'
)
ON CONFLICT (book_id, lang) DO UPDATE SET
    display_title = EXCLUDED.display_title,
    bibliography = EXCLUDED.bibliography,
    hint = EXCLUDED.hint,
    description = EXCLUDED.description,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL`,
		fixtureBookID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO section_translations (book_id, heading_id, lang, title, content, source, provenance_class)
VALUES ($1, $2, 'id', 'Bab Fixture Indonesia', 'Konten terjemahan Indonesia', 'integration-test', 'editorial')
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO section_audio (book_id, heading_id, lang, url, narrator, duration_seconds, mime_type)
VALUES ($1, $2, 'id', 'https://example.test/audio-fixture.mp3', 'Narator Fixture', 120, 'audio/mpeg')
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    url = EXCLUDED.url,
    narrator = EXCLUDED.narrator,
    duration_seconds = EXCLUDED.duration_seconds,
    mime_type = EXCLUDED.mime_type,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_heading_summaries (book_id, heading_id, lang, summary, source, provenance_class)
VALUES ($1, $2, 'ar', 'ملخص عربي للاختبار', 'integration-test', 'editorial')
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    summary = EXCLUDED.summary,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_heading_summaries (book_id, heading_id, lang, summary, source, provenance_class)
VALUES ($1, $2, 'id', 'Ringkasan fixture Indonesia', 'integration-test', 'editorial')
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    summary = EXCLUDED.summary,
    is_deleted = false,
    deleted_by = NULL,
    deleted_at = NULL,
    delete_reason = NULL`,
		fixtureBookID,
		fixtureHeadingID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO book_production_projects (
    id, book_id, lang, workflow_status, publication_status, requires_review, requires_audio,
    priority, created_at, updated_at, published_at
)
VALUES (
    '00000000-0000-0000-0000-000000990001', $1, 'id', 'published', 'published', false, true,
    0, now(), now(), now()
)`,
		fixtureBookID,
	)

	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit fixture tx: %v", err)
	}
}

func execFixtureSQL(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args ...any) {
	t.Helper()

	if _, err := tx.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec fixture sql: %v\n%s", err, sql)
	}
}

type bookResponse struct {
	ID               int                `json:"id"`
	Name             string             `json:"name"`
	CategoryName     *string            `json:"category_name"`
	AuthorName       *string            `json:"author_name"`
	Localization     localization       `json:"localization"`
	LanguageCoverage []languageCoverage `json:"language_coverage"`
}

type bookListResponse struct {
	Books []bookResponse `json:"items"`
	Total int            `json:"total"`
}

type localization struct {
	RequestedLang  string               `json:"requested_lang"`
	DisplayLang    string               `json:"display_lang"`
	IsFallback     bool                 `json:"is_fallback"`
	AvailableLangs []string             `json:"available_langs"`
	FieldLangs     map[string]string    `json:"field_langs"`
	Availability   availabilityDecision `json:"availability"`
}

type languageCoverage struct {
	Lang               string `json:"lang"`
	TranslatedSections int    `json:"translated_sections"`
	SummarizedSections int    `json:"summarized_sections"`
	AudioSections      int    `json:"audio_sections"`
}

type tocNode struct {
	HeadingID                 int          `json:"heading_id"`
	Title                     string       `json:"title"`
	RequestedLang             string       `json:"requested_lang"`
	TitleLang                 string       `json:"title_lang"`
	IsTitleFallback           bool         `json:"is_title_fallback"`
	SummaryLang               *string      `json:"summary_lang"`
	HasTranslation            bool         `json:"has_translation"`
	TranslationMissing        bool         `json:"translation_missing"`
	AvailableTranslationLangs []string     `json:"available_translation_langs"`
	AvailableSummaryLangs     []string     `json:"available_summary_langs"`
	Availability              availability `json:"availability"`
	Children                  []tocNode    `json:"children"`
}

type tocReadResponse struct {
	Title                     string              `json:"title"`
	RequestedLang             string              `json:"requested_lang"`
	TitleLang                 string              `json:"title_lang"`
	IsTitleFallback           bool                `json:"is_title_fallback"`
	SummaryLang               *string             `json:"summary_lang"`
	TranslationMissing        bool                `json:"translation_missing"`
	AvailableTranslationLangs []string            `json:"available_translation_langs"`
	AvailableSummaryLangs     []string            `json:"available_summary_langs"`
	Translation               *translationPayload `json:"translation"`
	Availability              availability        `json:"availability"`
}

type translationPayload struct {
	Lang    string  `json:"lang"`
	Title   *string `json:"title"`
	Content string  `json:"content"`
}

type availability struct {
	Title       availabilityDecision `json:"title"`
	Translation availabilityDecision `json:"translation"`
	Summary     availabilityDecision `json:"summary"`
	Audio       availabilityDecision `json:"audio"`
}

type availabilityDecision struct {
	Action        string `json:"action"`
	RequestedLang string `json:"requested_lang"`
	DisplayLang   string `json:"display_lang"`
	Missing       bool   `json:"missing"`
}

type missingAssetsResponse struct {
	Items  []missingAssetItem  `json:"items"`
	Total  int                 `json:"total"`
	Counts []missingAssetCount `json:"counts"`
}

type missingAssetItem struct {
	AssetType       string    `json:"asset_type"`
	TargetLang      string    `json:"target_lang"`
	BookID          *int      `json:"book_id"`
	BookTitle       *string   `json:"book_title"`
	HeadingID       *int      `json:"heading_id"`
	HeadingTitle    *string   `json:"heading_title"`
	CategoryID      *int      `json:"category_id"`
	CategoryName    *string   `json:"category_name"`
	AuthorID        *int      `json:"author_id"`
	AuthorName      *string   `json:"author_name"`
	AvailableLangs  []string  `json:"available_langs"`
	SourceUpdatedAt time.Time `json:"source_updated_at"`
}

type missingAssetCount struct {
	AssetType  string `json:"asset_type"`
	TargetLang string `json:"target_lang"`
	Total      int    `json:"total"`
}

func getBook(t *testing.T, lang string) bookResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books/%d?lang=%s", baseURL(), fixtureBookID, lang), nil, "")
	var book bookResponse
	decodeAndClose(t, resp, &book)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get book %s expected 200, got %d", lang, resp.StatusCode)
	}

	return book
}

func searchBooks(t *testing.T, query, lang string) bookListResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/books?q=%s&lang=%s",
		baseURL(),
		url.QueryEscape(query),
		lang,
	), nil, "")
	var books bookListResponse
	decodeAndClose(t, resp, &books)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search books expected 200, got %d", resp.StatusCode)
	}

	return books
}

func getTOC(t *testing.T, lang string) []tocNode {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books/%d/toc?lang=%s", baseURL(), fixtureBookID, lang), nil, "")

	var tocList struct {
		Items []tocNode `json:"items"`
	}
	decodeAndClose(t, resp, &tocList)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get toc %s expected 200, got %d", lang, resp.StatusCode)
	}

	return tocList.Items
}

func getTOCRead(t *testing.T, lang string) tocReadResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/books/%d/toc/%d/read?lang=%s",
		baseURL(),
		fixtureBookID,
		fixtureHeadingID,
		lang,
	), nil, "")
	var read tocReadResponse
	decodeAndClose(t, resp, &read)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get toc read %s expected 200, got %d", lang, resp.StatusCode)
	}

	return read
}

func findBook(books []bookResponse, id int) *bookResponse {
	for i := range books {
		if books[i].ID == id {
			return &books[i]
		}
	}

	return nil
}

func assertLocalization(t *testing.T, loc localization, requested, display string, fallback bool) {
	t.Helper()

	if loc.RequestedLang != requested || loc.DisplayLang != display || loc.IsFallback != fallback {
		t.Fatalf("localization = %+v, want requested=%s display=%s fallback=%v", loc, requested, display, fallback)
	}
}

func assertCoverage(t *testing.T, coverage []languageCoverage, lang string, translated, summarized, audio int) {
	t.Helper()

	for _, item := range coverage {
		if item.Lang == lang {
			if item.TranslatedSections != translated || item.SummarizedSections != summarized || item.AudioSections != audio {
				t.Fatalf("coverage[%s] = %+v, want translated=%d summarized=%d audio=%d", lang, item, translated, summarized, audio)
			}

			return
		}
	}

	t.Fatalf("coverage missing lang %s: %+v", lang, coverage)
}

func assertTOCNode(t *testing.T, node tocNode, requested, titleLang string, titleFallback, translationMissing bool, summaryLang string) {
	t.Helper()

	if node.HeadingID != fixtureHeadingID {
		t.Fatalf("toc heading id = %d", node.HeadingID)
	}
	if node.RequestedLang != requested || node.TitleLang != titleLang || node.IsTitleFallback != titleFallback {
		t.Fatalf("toc language metadata = %+v", node)
	}
	if node.TranslationMissing != translationMissing {
		t.Fatalf("toc translation_missing = %v", node.TranslationMissing)
	}
	if node.SummaryLang == nil || *node.SummaryLang != summaryLang {
		t.Fatalf("toc summary_lang = %v", node.SummaryLang)
	}
	assertHasLang(t, node.AvailableTranslationLangs, "id")
	assertHasLang(t, node.AvailableSummaryLangs, "id")
	assertHasLang(t, node.AvailableSummaryLangs, "ar")
}

func assertAvailability(
	t *testing.T,
	availability availabilityDecision,
	action string,
	requestedLang string,
	displayLang string,
	missing bool,
) {
	t.Helper()

	if availability.Action != action ||
		availability.RequestedLang != requestedLang ||
		availability.DisplayLang != displayLang ||
		availability.Missing != missing {
		t.Fatalf(
			"availability = %+v, want action=%s requested=%s display=%s missing=%v",
			availability,
			action,
			requestedLang,
			displayLang,
			missing,
		)
	}
}

func assertMissingCount(t *testing.T, counts []missingAssetCount, assetType, targetLang string, total int) {
	t.Helper()

	for _, count := range counts {
		if count.AssetType == assetType && count.TargetLang == targetLang {
			if count.Total != total {
				t.Fatalf("missing count %s/%s = %d, want %d", assetType, targetLang, count.Total, total)
			}

			return
		}
	}

	t.Fatalf("missing count %s/%s not found in %+v", assetType, targetLang, counts)
}

func assertHasLang(t *testing.T, langs []string, lang string) {
	t.Helper()

	if slices.Contains(langs, lang) {
		return
	}

	t.Fatalf("expected lang %q in %v", lang, langs)
}
