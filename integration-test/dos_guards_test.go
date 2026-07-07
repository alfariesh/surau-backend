package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestPublicDoSGuards pins the E5 fixes on the live HTTP surface:
// D2 (offset clamp), D4 (headings pagination, additive), D5 (ILIKE escaping).
func TestPublicDoSGuards(t *testing.T) {
	seedMultilingualKitabFixture(t)
	seedMetacharHeadings(t)

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

		resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books/%d/headings", baseURL(), fixtureBookID), nil, "")
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

		resp = doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books/%d/headings?limit=1&offset=1", baseURL(), fixtureBookID), nil, "")
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

func searchHeadings(t *testing.T, query string) (int, []string) {
	t.Helper()

	var payload struct {
		Items []struct {
			Content string `json:"content"`
		} `json:"items"`
		Total int `json:"total"`
	}

	resp := doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/v1/books/%d/headings?q=%s", baseURL(), fixtureBookID, url.QueryEscape(query)), nil, "")
	decodeAndClose(t, resp, &payload)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("headings q=%q expected 200, got %d", query, resp.StatusCode)
	}

	contents := make([]string, 0, len(payload.Items))
	for _, item := range payload.Items {
		contents = append(contents, item.Content)
	}

	return payload.Total, contents
}

// seedMetacharHeadings adds two discriminator headings to the fixture book:
// one with a literal '%' and one with "1000" but no metacharacter, so the
// escaping assertions can tell literal matches from wildcard matches.
func seedMetacharHeadings(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	for _, row := range []struct {
		id      int
		ordinal int
		content string
	}{
		{fixtureHeadingID + 900, 90, "باب 100% مئوية"},
		{fixtureHeadingID + 901, 91, "باب 1000 قاعدة"},
	} {
		if _, err := pool.Exec(ctx, `
INSERT INTO book_headings (book_id, heading_id, parent_id, page_id, depth, ordinal, content)
VALUES ($1, $2, NULL, 1, 0, $3, $4)
ON CONFLICT (book_id, heading_id) DO UPDATE SET
    content = EXCLUDED.content,
    ordinal = EXCLUDED.ordinal,
    is_deleted = false`,
			fixtureBookID, row.id, row.ordinal, row.content); err != nil {
			t.Fatalf("seed metachar heading %d: %v", row.id, err)
		}
	}
}
