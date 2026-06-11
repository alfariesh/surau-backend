package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
)

// TestEditorialSourcePageDraftConcurrency exercises the atomic If-Match
// enforcement on source page drafts: the second writer holding a stale ETag
// must get 412 instead of silently overwriting (the old TOCTOU race), and
// page draft writes without If-Match must get 428.
func TestEditorialSourcePageDraftConcurrency(t *testing.T) {
	seedMultilingualKitabFixture(t)
	cleanupSourcePageEdits(t)

	token := adminJWT(t)
	pageURL := fmt.Sprintf("%s/v1/editorial/books/%d/pages/1", baseURL(), fixtureBookID)

	// Initial GET hands out the raw page's ETag (no draft row yet).
	resp := doJSONWithIfMatch(t, http.MethodGet, pageURL, nil, token, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get page edit expected 200, got %d", resp.StatusCode)
	}
	rawETag := resp.Header.Get("ETag")
	if rawETag == "" {
		t.Fatal("expected ETag on page edit response")
	}

	// Page draft save without If-Match is rejected up front.
	resp = doJSONWithIfMatch(t, http.MethodPut, pageURL+"/draft",
		bytes.NewBufferString(`{"content_html":"<p>tanpa precondition</p>"}`), token, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("save without If-Match expected 428, got %d", resp.StatusCode)
	}

	// First conditional save against the raw ETag succeeds.
	resp = doJSONWithIfMatch(t, http.MethodPut, pageURL+"/draft",
		bytes.NewBufferString(`{"content_html":"<p>revisi pertama</p>"}`), token, rawETag)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first conditional save expected 200, got %d", resp.StatusCode)
	}
	draftETag := resp.Header.Get("ETag")
	if draftETag == "" || draftETag == rawETag {
		t.Fatalf("expected fresh draft ETag, got %q (raw %q)", draftETag, rawETag)
	}

	// A second writer replaying the now-stale raw ETag loses atomically.
	// Before the SQL-level CAS this request would have overwritten the draft.
	resp = doJSONWithIfMatch(t, http.MethodPut, pageURL+"/draft",
		bytes.NewBufferString(`{"content_html":"<p>penimpa basi</p>"}`), token, rawETag)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale conditional save expected 412, got %d", resp.StatusCode)
	}

	// The fresh ETag still works.
	resp = doJSONWithIfMatch(t, http.MethodPut, pageURL+"/draft",
		bytes.NewBufferString(`{"content_html":"<p>revisi kedua</p>"}`), token, draftETag)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fresh conditional save expected 200, got %d", resp.StatusCode)
	}
	secondETag := resp.Header.Get("ETag")

	// Publish with a stale draft ETag is rejected; with the fresh one it lands.
	resp = doJSONWithIfMatch(t, http.MethodPost, pageURL+"/publish", nil, token, draftETag)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("stale publish expected 412, got %d", resp.StatusCode)
	}

	resp = doJSONWithIfMatch(t, http.MethodPost, pageURL+"/publish", nil, token, secondETag)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish expected 200, got %d", resp.StatusCode)
	}

	// Wildcard If-Match stays available as the explicit last-write-wins escape hatch.
	resp = doJSONWithIfMatch(t, http.MethodPut, pageURL+"/draft",
		bytes.NewBufferString(`{"content_html":"<p>penulisan paksa</p>"}`), token, "*")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("wildcard save expected 200, got %d", resp.StatusCode)
	}
}

func doJSONWithIfMatch(t *testing.T, method, url string, body *bytes.Buffer, token, ifMatch string) *http.Response {
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
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	return resp
}

func cleanupSourcePageEdits(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	if _, err := pool.Exec(ctx, `DELETE FROM book_page_edits WHERE book_id = $1`, fixtureBookID); err != nil {
		t.Fatalf("cleanup book_page_edits: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM book_source_edit_revisions WHERE book_id = $1`, fixtureBookID); err != nil {
		t.Fatalf("cleanup book_source_edit_revisions: %v", err)
	}
}

// TestEditorialSourcePageDraftRevisions exercises the new source-edit revision
// history: every effective save snapshots a version, identical saves do not
// churn versions, and restore replays an old snapshot as a new draft.
func TestEditorialSourcePageDraftRevisions(t *testing.T) {
	seedMultilingualKitabFixture(t)
	cleanupSourcePageEdits(t)

	token := adminJWT(t)
	pageURL := fmt.Sprintf("%s/v1/editorial/books/%d/pages/1", baseURL(), fixtureBookID)

	for _, content := range []string{"<p>versi satu</p>", "<p>versi dua</p>", "<p>versi tiga</p>", "<p>versi tiga</p>"} {
		resp := doJSONWithIfMatch(t, http.MethodPut, pageURL+"/draft",
			bytes.NewBufferString(fmt.Sprintf(`{"content_html":%q}`, content)), token, "*")
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("save draft expected 200, got %d", resp.StatusCode)
		}
	}

	var list struct {
		Revisions []struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
			Origin  string `json:"origin"`
		} `json:"revisions"`
		Total int `json:"total"`
	}
	resp := doJSONWithIfMatch(t, http.MethodGet, pageURL+"/draft-revisions", nil, token, "")
	decodeAndClose(t, resp, &list)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list revisions expected 200, got %d", resp.StatusCode)
	}
	// Four saves, but the last one is identical to the third → three versions.
	if list.Total != 3 || len(list.Revisions) != 3 {
		t.Fatalf("expected 3 revisions after deduplicated saves, got %+v", list)
	}
	if list.Revisions[0].Version != 3 || list.Revisions[2].Version != 1 {
		t.Fatalf("revisions should be ordered newest-first: %+v", list.Revisions)
	}

	restoreURL := fmt.Sprintf("%s/draft-revisions/%s/restore", pageURL, list.Revisions[2].ID)
	resp = doJSONWithIfMatch(t, http.MethodPost, restoreURL, nil, token, "")
	var restored struct {
		ContentHTML string `json:"content_html"`
	}
	decodeAndClose(t, resp, &restored)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore expected 200, got %d", resp.StatusCode)
	}
	if restored.ContentHTML != "<p>versi satu</p>" {
		t.Fatalf("restore should bring back version 1 content, got %q", restored.ContentHTML)
	}

	resp = doJSONWithIfMatch(t, http.MethodGet, pageURL+"/draft-revisions", nil, token, "")
	decodeAndClose(t, resp, &list)
	if list.Total != 4 {
		t.Fatalf("restore should append a 4th revision, got %+v", list)
	}
	if list.Revisions[0].Version != 4 || list.Revisions[0].Origin != "restore" {
		t.Fatalf("newest revision should be the restore: %+v", list.Revisions[0])
	}
}
