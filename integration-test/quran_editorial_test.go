package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
)

type quranEditorialState struct {
	MetaTitle     *string `json:"meta_title"`
	LicenseStatus string  `json:"license_status"`
	Status        string  `json:"status"`
	UpdatedAt     string  `json:"updated_at"`
}

type quranEditorialWorkspaceResponse struct {
	Draft     *quranEditorialState `json:"draft"`
	Published *quranEditorialState `json:"published"`
}

type quranEditorialRevisionListResponse struct {
	Items []struct {
		ID      string `json:"id"`
		Version int    `json:"version"`
		Status  string `json:"status"`
		Origin  string `json:"origin"`
	} `json:"items"`
	Total int `json:"total"`
}

// TestQuranEditorialWorkflow is Q-1's end-to-end proof over the real HTTP and
// PostgreSQL stack. In particular, two writers race with the same ETag and
// exactly one loses with 412; drafts stay private; no-op saves do not create a
// revision; publish and restore each leave an immutable revision.
//
//nolint:bodyclose // one stateful acceptance story; every body is closed locally
func TestQuranEditorialWorkflow(t *testing.T) {
	seedMultilingualQuranFixture(t)

	token := adminJWT(t)
	surahAdminURL := fmt.Sprintf(
		"%s/v1/editorial/quran/surahs/%d?lang=id",
		baseURL(),
		fixtureQuranSurahID,
	)
	surahDraftURL := fmt.Sprintf(
		"%s/v1/editorial/quran/surahs/%d/draft?lang=id",
		baseURL(),
		fixtureQuranSurahID,
	)
	surahPublishURL := fmt.Sprintf(
		"%s/v1/editorial/quran/surahs/%d/publish?lang=id",
		baseURL(),
		fixtureQuranSurahID,
	)
	surahHistoryURL := fmt.Sprintf(
		"%s/v1/editorial/quran/surahs/%d/draft-revisions?lang=id",
		baseURL(),
		fixtureQuranSurahID,
	)

	// Creating the first draft is an explicit unconditional action. Omitting
	// If-Match is never silently interpreted as a force-write.
	resp := doJSONWithIfMatch(
		t,
		http.MethodPut,
		surahDraftURL,
		bytes.NewBufferString(`{"meta_title":"awal","license_status":"permitted"}`),
		token,
		"",
	)
	resp.Body.Close()

	if resp.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("surah save without If-Match = %d, want 428", resp.StatusCode)
	}

	var workspace quranEditorialWorkspaceResponse

	resp = doJSONWithIfMatch(
		t,
		http.MethodPut,
		surahDraftURL,
		bytes.NewBufferString(`{"meta_title":"awal","license_status":"permitted"}`),
		token,
		"*",
	)
	decodeAndClose(t, resp, &workspace)

	if resp.StatusCode != http.StatusOK || workspace.Draft == nil {
		t.Fatalf("first surah draft = %d %+v, want 200 with draft", resp.StatusCode, workspace)
	}

	sharedETag := resp.Header.Get("ETag")
	if sharedETag == "" {
		t.Fatal("first surah draft must return ETag")
	}

	assertPublicSurahEditorialTitle(t, nil)

	// Both requests leave the client at the same instant with the same ETag.
	// The row-level CAS must make the outcome exactly one 200 and one 412.
	type raceResult struct {
		status int
		etag   string
		body   []byte
		err    error
	}

	start := make(chan struct{})
	results := make(chan raceResult, 2)

	for _, title := range []string{"penyunting-a", "penyunting-b"} {
		go func() {
			<-start

			status, etag, body, err := rawQuranEditorialRequest(
				t.Context(), http.MethodPut, surahDraftURL,
				fmt.Sprintf(`{"meta_title":%q,"license_status":"permitted"}`, title),
				token, sharedETag,
			)
			results <- raceResult{status: status, etag: etag, body: body, err: err}
		}()
	}

	close(start)

	statuses := map[int]int{}

	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("concurrent surah edit request: %v", result.err)
		}

		statuses[result.status]++
	}

	if statuses[http.StatusOK] != 1 || statuses[http.StatusPreconditionFailed] != 1 {
		t.Fatalf("concurrent surah edit statuses = %+v, want one 200 and one 412", statuses)
	}

	resp = doJSONWithIfMatch(t, http.MethodGet, surahAdminURL, nil, token, "")
	workspace = quranEditorialWorkspaceResponse{}
	decodeAndClose(t, resp, &workspace)

	if resp.StatusCode != http.StatusOK || workspace.Draft == nil || workspace.Draft.MetaTitle == nil {
		t.Fatalf("surah workspace after race = %d %+v", resp.StatusCode, workspace)
	}

	winnerTitle := *workspace.Draft.MetaTitle
	winnerETag := resp.Header.Get("ETag")

	var history quranEditorialRevisionListResponse

	resp = doJSONWithIfMatch(t, http.MethodGet, surahHistoryURL, nil, token, "")
	decodeAndClose(t, resp, &history)

	if resp.StatusCode != http.StatusOK || history.Total != 2 || len(history.Items) != 2 {
		t.Fatalf("history after race = %d %+v, want 2 effective saves", resp.StatusCode, history)
	}

	oldestRevisionID := history.Items[1].ID

	// Re-saving the byte-equivalent snapshot is a successful no-op: its ETag
	// and revision count remain stable.
	resp = doJSONWithIfMatch(
		t,
		http.MethodPut,
		surahDraftURL,
		bytes.NewBufferString(fmt.Sprintf(`{"meta_title":%q,"license_status":"permitted"}`, winnerTitle)),
		token,
		winnerETag,
	)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK || resp.Header.Get("ETag") != winnerETag {
		t.Fatalf("no-op surah save = %d ETag %q, want 200 and unchanged %q", resp.StatusCode, resp.Header.Get("ETag"), winnerETag)
	}

	assertQuranEditorialHistoryTotal(t, surahHistoryURL, token, 2)

	// Publish is a separate effective transition. Draft content was invisible;
	// only published+permitted becomes visible on the unchanged public API.
	resp = doJSONWithIfMatch(t, http.MethodPost, surahPublishURL, nil, token, winnerETag)
	workspace = quranEditorialWorkspaceResponse{}
	decodeAndClose(t, resp, &workspace)

	if resp.StatusCode != http.StatusOK || workspace.Published == nil {
		t.Fatalf("surah publish = %d %+v", resp.StatusCode, workspace)
	}

	publishedETag := resp.Header.Get("ETag")

	assertPublicSurahEditorialTitle(t, &winnerTitle)
	assertQuranEditorialHistoryTotal(t, surahHistoryURL, token, 3)

	// A later draft remains private, and restoring v1 appends origin=restore
	// without changing the published public copy.
	resp = doJSONWithIfMatch(
		t,
		http.MethodPut,
		surahDraftURL,
		bytes.NewBufferString(`{"meta_title":"belum terbit","license_status":"permitted"}`),
		token,
		publishedETag,
	)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("later private draft = %d", resp.StatusCode)
	}

	restoreETag := resp.Header.Get("ETag")

	assertPublicSurahEditorialTitle(t, &winnerTitle)

	restoreURL := fmt.Sprintf(
		"%s/v1/editorial/quran/surahs/%d/draft-revisions/%s/restore?lang=id",
		baseURL(), fixtureQuranSurahID, oldestRevisionID,
	)
	resp = doJSONWithIfMatch(t, http.MethodPost, restoreURL, nil, token, restoreETag)
	workspace = quranEditorialWorkspaceResponse{}
	decodeAndClose(t, resp, &workspace)

	if resp.StatusCode != http.StatusOK || workspace.Draft == nil || workspace.Draft.MetaTitle == nil ||
		*workspace.Draft.MetaTitle != "awal" {
		t.Fatalf("surah restore = %d %+v, want oldest content", resp.StatusCode, workspace)
	}

	assertPublicSurahEditorialTitle(t, &winnerTitle)

	resp = doJSONWithIfMatch(t, http.MethodGet, surahHistoryURL, nil, token, "")
	history = quranEditorialRevisionListResponse{}
	decodeAndClose(t, resp, &history)

	if history.Total != 5 || len(history.Items) == 0 || history.Items[0].Origin != "restore" {
		t.Fatalf("history after restore = %+v, want fifth revision with origin restore", history)
	}

	assertAyahEditorialWorkflow(t, token)
}

func assertAyahEditorialWorkflow(t *testing.T, token string) {
	t.Helper()

	adminBase := fmt.Sprintf("%s/v1/editorial/quran/ayahs/%s", baseURL(), fixtureQuranAyahKey)
	draftURL := adminBase + "/draft?lang=id"
	publishURL := adminBase + "/publish?lang=id"
	historyURL := adminBase + "/draft-revisions?lang=id"

	resp := doJSONWithIfMatch(
		t,
		http.MethodPut,
		draftURL,
		bytes.NewBufferString(`{"meta_title":"draft ayah","license_status":"needs_review","faq":[]}`),
		token,
		"*",
	)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ayah draft = %d", resp.StatusCode)
	}

	draftETag := resp.Header.Get("ETag")

	assertPublicAyahEditorialTitle(t, nil)

	resp = doJSONWithIfMatch(t, http.MethodPost, publishURL, nil, token, draftETag)

	var errorBody struct {
		Code string `json:"code"`
	}
	decodeAndClose(t, resp, &errorBody)

	if resp.StatusCode != http.StatusConflict || errorBody.Code != "license_not_permitted" {
		t.Fatalf("non-permitted ayah publish = %d/%q, want 409/license_not_permitted", resp.StatusCode, errorBody.Code)
	}

	resp = doJSONWithIfMatch(
		t,
		http.MethodPut,
		draftURL,
		bytes.NewBufferString(`{"meta_title":"published ayah","license_status":"permitted","faq":[]}`),
		token,
		draftETag,
	)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("permitted ayah draft = %d", resp.StatusCode)
	}

	permittedETag := resp.Header.Get("ETag")

	assertPublicAyahEditorialTitle(t, nil)

	resp = doJSONWithIfMatch(t, http.MethodPost, publishURL, nil, token, permittedETag)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ayah publish = %d", resp.StatusCode)
	}

	wantTitle := "published ayah"
	assertPublicAyahEditorialTitle(t, &wantTitle)

	var history quranEditorialRevisionListResponse

	resp = doJSONWithIfMatch(t, http.MethodGet, historyURL, nil, token, "")
	decodeAndClose(t, resp, &history)

	if resp.StatusCode != http.StatusOK || history.Total != 3 || history.Items[0].Status != "published" {
		t.Fatalf("ayah revision history = %d %+v", resp.StatusCode, history)
	}
}

func rawQuranEditorialRequest(
	parent context.Context,
	method,
	requestURL,
	body,
	token,
	ifMatch string,
) (status int, etag string, payload []byte, err error) {
	ctx, cancel := context.WithTimeout(parent, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewBufferString(body))
	if err != nil {
		return 0, "", nil, err
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", ifMatch)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()

	payload, err = io.ReadAll(resp.Body)

	return resp.StatusCode, resp.Header.Get("ETag"), payload, err
}

func assertQuranEditorialHistoryTotal(t *testing.T, requestURL, token string, want int) {
	t.Helper()

	resp := doJSONWithIfMatch(t, http.MethodGet, requestURL, nil, token, "")

	var history quranEditorialRevisionListResponse

	decodeAndClose(t, resp, &history)

	if resp.StatusCode != http.StatusOK || history.Total != want {
		t.Fatalf("Quran editorial history = %d/%d, want 200/%d", resp.StatusCode, history.Total, want)
	}
}

func assertPublicSurahEditorialTitle(t *testing.T, want *string) {
	t.Helper()

	resp := doJSON(
		t,
		http.MethodGet,
		fmt.Sprintf("%s/v1/quran/surahs/%d?lang=id", baseURL(), fixtureQuranSurahID),
		nil,
		"",
	)

	var public struct {
		Editorial *struct {
			MetaTitle *string `json:"meta_title"`
		} `json:"editorial"`
	}
	decodeAndClose(t, resp, &public)
	assertOptionalEditorialTitle(t, resp.StatusCode, public.Editorial, want)
}

func assertPublicAyahEditorialTitle(t *testing.T, want *string) {
	t.Helper()

	resp := doJSON(
		t,
		http.MethodGet,
		fmt.Sprintf("%s/v1/quran/ayahs/%s?lang=id", baseURL(), fixtureQuranAyahKey),
		nil,
		"",
	)

	var public struct {
		Editorial *struct {
			MetaTitle *string `json:"meta_title"`
		} `json:"editorial"`
	}
	decodeAndClose(t, resp, &public)
	assertOptionalEditorialTitle(t, resp.StatusCode, public.Editorial, want)
}

func assertOptionalEditorialTitle(
	t *testing.T,
	status int,
	editorial *struct {
		MetaTitle *string `json:"meta_title"`
	},
	want *string,
) {
	t.Helper()

	if status != http.StatusOK {
		t.Fatalf("public Quran read = %d, want 200", status)
	}

	if want == nil {
		if editorial != nil {
			encoded, err := json.Marshal(editorial)
			if err != nil {
				t.Fatalf("encode leaked Quran editorial: %v", err)
			}

			t.Fatalf("draft Quran editorial leaked publicly: %s", encoded)
		}

		return
	}

	if editorial == nil || editorial.MetaTitle == nil || *editorial.MetaTitle != *want {
		t.Fatalf("public Quran editorial title = %+v, want %q", editorial, *want)
	}
}
