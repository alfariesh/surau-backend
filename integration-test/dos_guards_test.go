package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// dosBookID is a dedicated published book for this suite: its heading set is
// fully owned here, so the discriminator rows cannot disturb the TOC/heading
// expectations of the shared multilingual fixture book.
const dosBookID = 990777

// TestPublicDoSGuards pins the E5 fixes on the live HTTP surface:
// D2 (offset clamp), D4 (headings pagination, additive), D5 (ILIKE escaping).
//
//nolint:bodyclose // every response is closed by decodeAndClose or resp.Body.Close
func TestPublicDoSGuards(t *testing.T) {
	seedMultilingualKitabFixture(t) // guarantees the shared category/author rows exist
	seedDoSGuardBook(t)

	t.Run("huge offset is clamped, not executed", func(t *testing.T) {
		start := time.Now()
		resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books?offset=999999999", baseURL()), nil, "")

		var payload struct {
			Items []any `json:"items"`
			Total int   `json:"total"`
		}
		decodeAndClose(t, resp, &payload)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("clamped deep-offset query took %s — clamp not effective", elapsed)
		}
	})

	t.Run("headings are paginated with additive defaults", func(t *testing.T) {
		var full struct {
			Items []struct {
				HeadingID int    `json:"heading_id"`
				Content   string `json:"content"`
			} `json:"items"`
			Total int `json:"total"`
		}

		resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books/%d/headings", baseURL(), dosBookID), nil, "")
		decodeAndClose(t, resp, &full)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}

		if len(full.Items) > 200 {
			t.Fatalf("default response must cap at 200 items, got %d", len(full.Items))
		}

		if full.Total < 3 {
			t.Fatalf("expected total >= 3 seeded headings, got %d", full.Total)
		}

		var page struct {
			Items []any `json:"items"`
			Total int   `json:"total"`
		}

		resp = doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books/%d/headings?limit=1&offset=1", baseURL(), dosBookID), nil, "")
		decodeAndClose(t, resp, &page)

		if len(page.Items) != 1 {
			t.Fatalf("limit=1 must return exactly 1 item, got %d", len(page.Items))
		}

		if page.Total != full.Total {
			t.Fatalf("total must stay the full count across pages: %d != %d", page.Total, full.Total)
		}
	})

	t.Run("LIKE metacharacters match literally", func(t *testing.T) {
		// Exactly one seeded heading contains a literal '%' (and another contains
		// "1000" without '%'). Unescaped, q=100% would match both; escaped, only
		// the literal one.
		total, contents := searchHeadings(t, "100%")
		if total != 1 || len(contents) != 1 {
			t.Fatalf("q=100%% must match exactly the literal heading, got total=%d items=%v", total, contents)
		}

		// A bare % unescaped matches EVERY heading; escaped it matches only the
		// one containing a literal percent sign.
		total, _ = searchHeadings(t, "%")
		if total != 1 {
			t.Fatalf("q=%% must match only the literal-percent heading, got total=%d", total)
		}

		// Underscore and backslash must not crash or wildcard-match.
		if total, _ = searchHeadings(t, "_"); total != 0 {
			t.Fatalf("q=_ must match nothing (no literal underscore seeded), got %d", total)
		}

		if total, _ = searchHeadings(t, `\`); total != 0 {
			t.Fatalf(`q=\ must match nothing and not error, got %d`, total)
		}

		// Smoke the other public search surfaces with metacharacters.
		for _, path := range []string{
			"/v1/books?q=" + url.QueryEscape("%%%"),
			"/v1/authors?q=" + url.QueryEscape("_%"),
			"/v1/quran/search?q=" + url.QueryEscape("%_"),
		} {
			resp := doJSON(t, http.MethodGet, baseURL()+path, nil, "")
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s expected 200, got %d", path, resp.StatusCode)
			}
		}
	})
}

func searchHeadings(t *testing.T, query string) (total int, contents []string) {
	t.Helper()

	var payload struct {
		Items []struct {
			Content string `json:"content"`
		} `json:"items"`
		Total int `json:"total"`
	}

	resp := doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/v1/books/%d/headings?q=%s", baseURL(), dosBookID, url.QueryEscape(query)), nil, "")
	decodeAndClose(t, resp, &payload)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("headings q=%q expected 200, got %d", query, resp.StatusCode)
	}

	contents = make([]string, 0, len(payload.Items))
	for _, item := range payload.Items {
		contents = append(contents, item.Content)
	}

	return payload.Total, contents
}

// seedDoSGuardBook creates a self-contained published book with three
// headings: a plain one, one containing a literal '%', and one containing
// "1000" without any metacharacter — so the escaping assertions can tell
// literal matches from wildcard matches without touching shared fixtures.
func seedDoSGuardBook(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin dos-guard fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	exec := func(sql string, args ...any) {
		t.Helper()

		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed dos-guard book: %v (sql %s)", err, sql)
		}
	}

	exec(`
INSERT INTO books (id, name, category_id, author_id, type, has_content, license_status)
VALUES ($1, 'كتاب حراس التحميل', $2, $3, 1, true, 'unknown')
ON CONFLICT (id) DO NOTHING`,
		dosBookID, fixtureCategoryID, fixtureAuthorID)
	permitBookFixtures(ctx, t, tx, dosBookID)
	exec(`
UPDATE books
SET name = 'كتاب حراس التحميل',
    category_id = $2,
    author_id = $3,
    type = 1,
    has_content = true,
    is_deleted = false
WHERE id = $1`, dosBookID, fixtureCategoryID, fixtureAuthorID)
	exec(`
INSERT INTO book_publications (book_id, status, featured, sort_order, published_at)
VALUES ($1, 'published', false, 99, now())
ON CONFLICT (book_id) DO UPDATE SET status = 'published'`, dosBookID)
	exec(`
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, 1, '<p>صفحة الاختبار</p>', 'صفحة الاختبار')
ON CONFLICT (book_id, page_id) DO UPDATE SET is_deleted = false`, dosBookID)

	for _, row := range []struct {
		id      int
		ordinal int
		content string
	}{
		{1, 1, "باب الاختبار"},
		{2, 2, "باب 100% مئوية"},
		{3, 3, "باب 1000 قاعدة"},
	} {
		exec(`
INSERT INTO book_headings (book_id, heading_id, parent_id, page_id, depth, ordinal, content)
VALUES ($1, $2, NULL, 1, 0, $3, $4)
ON CONFLICT (book_id, heading_id) DO UPDATE SET
    content = EXCLUDED.content,
    ordinal = EXCLUDED.ordinal,
    is_deleted = false`,
			dosBookID, row.id, row.ordinal, row.content)
	}

	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit dos-guard fixture: %v", err)
	}
}
