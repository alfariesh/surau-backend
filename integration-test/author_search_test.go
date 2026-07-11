package integration_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
)

const hamzaFixtureAuthorID = 990777

// TestAuthorSearchFoldsHamzaVariants proves the F1-H reader switch: an
// author stored with hamza spelling is found by the bare-alif query users
// actually type, via the normalized name_search arm (filled by the backfill
// job / importer; seeded directly here).
func TestAuthorSearchFoldsHamzaVariants(t *testing.T) {
	seedHamzaAuthorFixture(t)

	// Bare-alif query (what users type) must find the hamza-spelled author.
	resp := doJSON(
		t,
		http.MethodGet,
		baseURL()+"/v1/authors?q="+url.QueryEscape("مصنف احكام التكامل")+"&limit=10",
		nil,
		"",
	)

	var list struct {
		Items []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
		Total int `json:"total"`
	}

	decodeAndClose(t, resp, &list)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authors search expected 200, got %d", resp.StatusCode)
	}

	found := false

	for _, item := range list.Items {
		if item.ID == hamzaFixtureAuthorID {
			found = true
		}
	}

	if !found {
		t.Fatalf("bare-alif query did not find the hamza-spelled author (total=%d)", list.Total)
	}

	// The raw arm keeps working: exact hamza spelling still matches.
	resp = doJSON(
		t,
		http.MethodGet,
		baseURL()+"/v1/authors?q="+url.QueryEscape("مصنف أحكام التكامل")+"&limit=10",
		nil,
		"",
	)

	var rawList struct {
		Total int `json:"total"`
	}

	decodeAndClose(t, resp, &rawList)

	if resp.StatusCode != http.StatusOK || rawList.Total == 0 {
		t.Fatalf("hamza query regressed: status=%d total=%d", resp.StatusCode, rawList.Total)
	}
}

// seedHamzaAuthorFixture inserts an author the way the importer now writes
// them: raw name plus the canonical normalized name_search.
func seedHamzaAuthorFixture(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	_, err := pool.Exec(ctx, `
INSERT INTO authors (id, name, name_search, name_search_normalization_version, is_deleted)
VALUES ($1, 'مصنف أحكام التكامل', 'مصنف احكام التكامل', 1, false)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    name_search = EXCLUDED.name_search,
    name_search_normalization_version = EXCLUDED.name_search_normalization_version,
    is_deleted = false`, hamzaFixtureAuthorID)
	if err != nil {
		t.Fatalf("seed hamza author fixture: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cleanupCancel()

		cleanupPool := integrationDB(t)
		defer cleanupPool.Close()

		_, _ = cleanupPool.Exec(cleanupCtx, `DELETE FROM authors WHERE id = $1`, hamzaFixtureAuthorID) //nolint:errcheck // best-effort fixture cleanup
	})
}
