package integration_test

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
)

// TestPersonalDataIsolationBetweenUsers verifies that one user's personal
// reader data (progress, saved items, khatam, activity, sessions) is never
// visible to — or mutable by — another authenticated user.
//
//nolint:bodyclose // every response is closed by decodeAndClose or resp.Body.Close
func TestPersonalDataIsolationBetweenUsers(t *testing.T) {
	seedMultilingualKitabFixture(t)
	seedMultilingualQuranFixture(t)

	emailA, _ := registerVerifiedUser(t)
	emailB, _ := registerVerifiedUser(t)
	tokenA := loginWithUA(t, emailA, "isolation-test-device-a").AccessToken
	tokenB := loginWithUA(t, emailB, "isolation-test-device-b").AccessToken

	// User A writes one of everything.
	resp := doJSON(t, http.MethodPut,
		fmt.Sprintf("%s/v1/me/progress/%d", baseURL(), fixtureBookID),
		bytes.NewBufferString(fmt.Sprintf(`{"heading_id":%d,"progress_percent":42.5}`, fixtureHeadingID)),
		tokenA)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user A save kitab progress expected 200, got %d", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodPut, baseURL()+"/v1/me/quran/progress",
		bytes.NewBufferString(fmt.Sprintf(`{"ayah_key":"%d:1"}`, fixtureQuranSurahID)), tokenA)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user A save quran progress expected 200, got %d", resp.StatusCode)
	}

	var savedItem struct {
		ID string `json:"id"`
	}

	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/me/saved-items",
		bytes.NewBufferString(fmt.Sprintf(
			`{"item_type":"quran_ayah","surah_id":%d,"ayah_key":"%d:1","label":"milik A"}`,
			fixtureQuranSurahID, fixtureQuranSurahID,
		)), tokenA)
	decodeAndClose(t, resp, &savedItem)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("user A create saved item expected 201/200, got %d", resp.StatusCode)
	}

	if savedItem.ID == "" {
		t.Fatal("user A saved item has no id")
	}

	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/me/quran/khatam",
		bytes.NewBufferString(`{"notes":"cycle milik A"}`), tokenA)
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("user A start khatam expected 201, got %d", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodPut, baseURL()+"/v1/me/quran/khatam/juz/1", nil, tokenA)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user A mark juz expected 200, got %d", resp.StatusCode)
	}

	// Sanity: A sees its own data, so empty results for B below cannot come
	// from writes silently failing.
	var syncA struct {
		ReadingProgress []map[string]any `json:"reading_progress"`
		QuranProgress   []map[string]any `json:"quran_progress"`
		SavedItems      []map[string]any `json:"saved_items"`
		SavedItemIDs    []string         `json:"saved_item_ids"`
		KhatamCycles    []map[string]any `json:"khatam_cycles"`
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/me/sync", nil, tokenA)
	decodeAndClose(t, resp, &syncA)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user A sync expected 200, got %d", resp.StatusCode)
	}

	if len(syncA.ReadingProgress) != 1 || len(syncA.QuranProgress) != 1 ||
		len(syncA.SavedItems) != 1 || len(syncA.SavedItemIDs) != 1 || len(syncA.KhatamCycles) != 1 {
		t.Fatalf("user A sync should contain its own rows, got %+v", syncA)
	}

	// User B must see none of it.
	var syncB struct {
		ReadingProgress []map[string]any `json:"reading_progress"`
		QuranProgress   []map[string]any `json:"quran_progress"`
		SavedItems      []map[string]any `json:"saved_items"`
		SavedItemIDs    []string         `json:"saved_item_ids"`
		KhatamCycles    []map[string]any `json:"khatam_cycles"`
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/me/sync", nil, tokenB)
	decodeAndClose(t, resp, &syncB)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user B sync expected 200, got %d", resp.StatusCode)
	}

	if len(syncB.ReadingProgress) != 0 || len(syncB.QuranProgress) != 0 ||
		len(syncB.SavedItems) != 0 || len(syncB.SavedItemIDs) != 0 || len(syncB.KhatamCycles) != 0 {
		t.Fatalf("user B sync leaked user A data: %+v", syncB)
	}

	// Direct reads return empty lists or 404 for B.
	for _, check := range []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"progress detail", http.MethodGet, fmt.Sprintf("/v1/me/progress/%d", fixtureBookID), http.StatusNotFound},
		{"quran progress", http.MethodGet, "/v1/me/quran/progress", http.StatusNotFound},
		{"active khatam", http.MethodGet, "/v1/me/quran/khatam", http.StatusNotFound},
		{"patch A's saved item", http.MethodPatch, "/v1/me/saved-items/" + savedItem.ID, http.StatusNotFound},
		{"delete A's saved item", http.MethodDelete, "/v1/me/saved-items/" + savedItem.ID, http.StatusNotFound},
	} {
		var body *bytes.Buffer
		if check.method == http.MethodPatch {
			body = bytes.NewBufferString(`{"label":"hijacked"}`)
		}

		resp = doJSON(t, check.method, baseURL()+check.path, body, tokenB)
		resp.Body.Close()

		if resp.StatusCode != check.wantStatus {
			t.Fatalf("user B %s expected %d, got %d", check.name, check.wantStatus, resp.StatusCode)
		}
	}

	var listB struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/me/progress", nil, tokenB)
	decodeAndClose(t, resp, &listB)

	if resp.StatusCode != http.StatusOK || len(listB.Items) != 0 || listB.Total != 0 {
		t.Fatalf("user B progress list should be empty, got status %d body %+v", resp.StatusCode, listB)
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/me/saved-items", nil, tokenB)
	decodeAndClose(t, resp, &listB)

	if resp.StatusCode != http.StatusOK || len(listB.Items) != 0 || listB.Total != 0 {
		t.Fatalf("user B saved items should be empty, got status %d body %+v", resp.StatusCode, listB)
	}

	// B's activity is untouched by A's writes.
	var activityB struct {
		ActiveDays int `json:"active_days"`
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/me/activity", nil, tokenB)
	decodeAndClose(t, resp, &activityB)

	if resp.StatusCode != http.StatusOK || activityB.ActiveDays != 0 {
		t.Fatalf("user B activity should be empty, got status %d active_days %d", resp.StatusCode, activityB.ActiveDays)
	}

	// B cannot revoke A's session.
	sessionsA := listSessions(t, tokenA)
	if len(sessionsA.Sessions) == 0 {
		t.Fatal("user A should have at least one session")
	}

	resp = doJSON(t, http.MethodDelete, baseURL()+"/v1/auth/sessions/"+sessionsA.Sessions[0].ID, nil, tokenB)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("user B revoking A's session expected 404, got %d", resp.StatusCode)
	}

	// And A's saved item survives B's attempts.
	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/me/saved-items", nil, tokenA)

	var listA struct {
		Items []struct {
			ID    string  `json:"id"`
			Label *string `json:"label"`
		} `json:"items"`
		Total int `json:"total"`
	}
	decodeAndClose(t, resp, &listA)

	if listA.Total != 1 || len(listA.Items) != 1 || listA.Items[0].ID != savedItem.ID {
		t.Fatalf("user A saved item should be intact, got %+v", listA)
	}

	if listA.Items[0].Label == nil || *listA.Items[0].Label != "milik A" {
		t.Fatalf("user A saved item label should be unchanged, got %+v", listA.Items[0].Label)
	}
}
