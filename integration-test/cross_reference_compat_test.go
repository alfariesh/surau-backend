//nolint:wsl_v5 // SQL-heavy compatibility fixtures stay clearer when setup statements are grouped
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5"
)

const (
	crossReferenceCompatRangeID   = "00000000-0000-0000-0000-00000000c311"
	crossReferenceCompatPendingID = "00000000-0000-0000-0000-00000000c312"
	crossReferenceCompatPointID   = "00000000-0000-0000-0000-00000000c313"
	crossReferenceCompatPage2ID   = "00000000-0000-0000-0000-00000000c314"
)

// TestQuranReferencePartialBackfillCompatibility proves B-3's no-downtime
// reader switch. A mixed projection (some rows bridged, some still legacy)
// must be semantically identical to the legacy-only result, including the old
// endpoint's ordering, pagination, heading filter, ayah attachment, and the
// TOC-reader embed. Caller-supplied status values must never expose drafts.
func TestQuranReferencePartialBackfillCompatibility(t *testing.T) {
	seedCrossReferenceFixture(t)
	t.Cleanup(func() { cleanupCrossReferenceFixture(t) })
	seedQuranReferenceCompatibilityRows(t)

	listPath := fmt.Sprintf(
		"/v1/books/%d/quran-references?lang=ar&limit=200&offset=0",
		crossReferenceBookAID,
	)
	readerPath := fmt.Sprintf(
		"/v1/books/%d/toc/1/read?lang=ar&include_quran_references=true",
		crossReferenceBookAID,
	)

	legacyListRaw, legacyList := getQuranReferenceContract(t, listPath)
	assertQuranReferenceContract(t, legacyList)
	legacyReaderRaw, legacyReader := getReaderQuranReferenceContract(t, readerPath)
	assertReferenceIDs(
		t,
		legacyReader.Items,
		[]string{crossReferenceCompatRangeID, crossReferenceCompatPointID},
	)

	// Bridge only the first and last approved rows. The middle approved row
	// deliberately stays in quran_book_references to exercise the UNION ALL
	// fallback and its anti-duplicate join during a paused/resumed backfill.
	bridgeQuranReferenceCompatibilityRows(
		t,
		crossReferenceCompatRangeID,
		crossReferenceCompatPage2ID,
	)

	mixedListRaw, mixedList := getQuranReferenceContract(t, listPath)
	assertJSONSemanticallyEqual(t, legacyListRaw, mixedListRaw, "legacy Quran endpoint")
	assertQuranReferenceContract(t, mixedList)

	mixedReaderRaw, mixedReader := getReaderQuranReferenceContract(t, readerPath)
	assertJSONSemanticallyEqual(t, legacyReaderRaw, mixedReaderRaw, "reader Quran embed")
	assertReferenceIDs(
		t,
		mixedReader.Items,
		[]string{crossReferenceCompatRangeID, crossReferenceCompatPointID},
	)

	t.Run("pagination retains full total and stable order", func(t *testing.T) {
		path := fmt.Sprintf(
			"/v1/books/%d/quran-references?lang=ar&limit=1&offset=1",
			crossReferenceBookAID,
		)
		_, page := getQuranReferenceContract(t, path)
		if page.Total != 3 {
			t.Fatalf("partial-backfill page total = %d, want 3", page.Total)
		}
		assertReferenceIDs(t, page.Items, []string{crossReferenceCompatPointID})
		if got := len(page.Items[0].Ayahs); got != 1 {
			t.Fatalf("paginated reference attached %d ayahs, want 1", got)
		}
	})

	t.Run("heading filter excludes other section", func(t *testing.T) {
		path := fmt.Sprintf(
			"/v1/books/%d/quran-references?lang=ar&heading_id=1&limit=200&offset=0",
			crossReferenceBookAID,
		)
		_, heading := getQuranReferenceContract(t, path)
		if heading.Total != 2 {
			t.Fatalf("heading-filtered total = %d, want 2", heading.Total)
		}
		assertReferenceIDs(
			t,
			heading.Items,
			[]string{crossReferenceCompatRangeID, crossReferenceCompatPointID},
		)
	})

	t.Run("public status query cannot expose draft", func(t *testing.T) {
		for _, status := range []string{"pending", "all"} {
			path := listPath + "&status=" + status
			statusRaw, statusList := getQuranReferenceContract(t, path)
			assertJSONSemanticallyEqual(t, mixedListRaw, statusRaw, "status="+status)
			if statusList.Total != 3 {
				t.Fatalf("status=%s total = %d, want approved-only 3", status, statusList.Total)
			}
			for _, item := range statusList.Items {
				if item.ID == crossReferenceCompatPendingID {
					t.Fatalf("status=%s exposed pending reference %s", status, item.ID)
				}
			}
		}
	})
}

// TestCrossReferenceEditorialCapabilityGate pins the CapReviewEditorial gate
// on every B-3 editorial route. Authentication and capability checks must run
// before request parsing, object lookup, or If-Match validation.
func TestCrossReferenceEditorialCapabilityGate(t *testing.T) {
	plainUserToken := roleUserToken(t, "user")
	knownID := crossReferenceCompatRangeID
	createBody := fmt.Sprintf(`{
		"source_anchor":"kitab/%d/h/1",
		"target_anchor":"quran/%d:%d",
		"kind":"cites",
		"confidence":1,
		"evidence_text":"B-3 capability tripwire"
	}`, crossReferenceBookAID, crossReferenceSurahID, crossReferenceAyahFrom)

	routes := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "list", method: http.MethodGet, path: "/v1/editorial/cross-references?limit=1"},
		{name: "create", method: http.MethodPost, path: "/v1/editorial/cross-references", body: createBody},
		{name: "get", method: http.MethodGet, path: "/v1/editorial/cross-references/" + knownID},
		{
			name:   "review",
			method: http.MethodPatch,
			path:   "/v1/editorial/cross-references/" + knownID + "/review",
			body:   `{"review_status":"approved"}`,
		},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			for _, actor := range []struct {
				name       string
				token      string
				wantStatus int
			}{
				{name: "anonymous", wantStatus: http.StatusUnauthorized},
				{name: "plain user", token: plainUserToken, wantStatus: http.StatusForbidden},
			} {
				t.Run(actor.name, func(t *testing.T) {
					var body *bytes.Buffer
					if route.body != "" {
						body = bytes.NewBufferString(route.body)
					}

					resp := doJSON(t, route.method, baseURL()+route.path, body, actor.token)
					defer resp.Body.Close()
					if resp.StatusCode != actor.wantStatus {
						t.Fatalf(
							"%s %s as %s = %d, want %d",
							route.method,
							route.path,
							actor.name,
							resp.StatusCode,
							actor.wantStatus,
						)
					}
				})
			}
		})
	}
}

type quranReferenceCompatibilityList struct {
	Items []quranReferenceCompatibilityItem `json:"items"`
	Total int                               `json:"total"`
}

type quranReferenceCompatibilityItem struct {
	ID                   string          `json:"id"`
	ReviewStatus         string          `json:"review_status"`
	NormalizationVersion json.RawMessage `json:"normalization_version"`
	Ayahs                []struct {
		AyahKey string `json:"ayah_key"`
	} `json:"ayahs"`
}

type readerQuranReferenceCompatibility struct {
	Items []quranReferenceCompatibilityItem `json:"quran_references"`
}

func seedQuranReferenceCompatibilityRows(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Quran compatibility fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text, updated_at)
VALUES ($1, 2, '<p>B-3 Work A page two</p>', 'B-3 Work A page two', '2026-07-10T00:00:00Z')`,
		crossReferenceBookAID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content, updated_at)
VALUES ($1, 2, 2, 0, 2, 'B-3 Heading A two', '2026-07-10T00:00:00Z')`,
		crossReferenceBookAID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_heading_ranges (book_id, heading_id, start_page_id, end_page_id)
VALUES
    ($1, 1, 1, 1),
    ($1, 2, 2, 2)`, crossReferenceBookAID)

	insertLegacyQuranCompatibilityRow(
		t, ctx, tx,
		crossReferenceCompatRangeID, 1, 1, "approved", "quote",
		crossReferenceAyahFrom, crossReferenceAyahMiddle, "2026-07-10T00:00:01Z",
	)
	insertLegacyQuranCompatibilityRow(
		t, ctx, tx,
		crossReferenceCompatPendingID, 1, 1, "pending", "surah_ayah",
		crossReferenceAyahMiddle, crossReferenceAyahMiddle, "2026-07-10T00:00:02Z",
	)
	insertLegacyQuranCompatibilityRow(
		t, ctx, tx,
		crossReferenceCompatPointID, 1, 1, "approved", "surah_ayah",
		crossReferenceAyahTo, crossReferenceAyahTo, "2026-07-10T00:00:03Z",
	)
	insertLegacyQuranCompatibilityRow(
		t, ctx, tx,
		crossReferenceCompatPage2ID, 2, 2, "approved", "surah_ayah",
		crossReferenceAyahOutside, crossReferenceAyahOutside, "2026-07-10T00:00:04Z",
	)
	execFixtureSQL(t, ctx, tx, `SET LOCAL session_replication_role = replica`)
	execFixtureSQL(t, ctx, tx, `
UPDATE quran_book_references
SET normalization_version = NULL
WHERE id = $1::uuid`, crossReferenceCompatPointID)
	execFixtureSQL(t, ctx, tx, `SET LOCAL session_replication_role = origin`)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit Quran compatibility fixture: %v", err)
	}
}

//nolint:revive // test helpers conventionally keep testing.T before context
func insertLegacyQuranCompatibilityRow(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	id string,
	pageID, headingID int,
	status, kind string,
	fromAyah, toAyah int,
	stamp string,
) {
	t.Helper()

	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO quran_book_references (
    id, book_id, page_id, heading_id, source_text, normalized_text,
    normalization_version, reference_kind, surah_id, from_ayah_number, to_ayah_number,
    from_ayah_key, to_ayah_key, match_strategy, confidence, review_status,
    metadata, created_at, updated_at
) VALUES (
    $1::uuid, $2::integer, $3::integer, $4::integer,
    'B-3 legacy evidence ' || $1::text,
    'b-3 legacy evidence ' || $1::text, 1, $5::text, $6::integer,
    $7::integer, $8::integer,
    $6::text || ':' || $7::text, $6::text || ':' || $8::text,
    'explicit_surah_ayah', 0.9500, $9::text,
    jsonb_build_object('integration_fixture', 'b3-quran-compatibility', 'id', $1::text),
    $10::timestamptz, $10::timestamptz
)`,
		id,
		crossReferenceBookAID,
		pageID,
		headingID,
		kind,
		crossReferenceSurahID,
		fromAyah,
		toAyah,
		status,
		stamp,
	)
}

func bridgeQuranReferenceCompatibilityRows(t *testing.T, ids ...string) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin partial Quran bridge: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus,
    source_work_id, target_quran_surah_id, target_quran_from_ayah,
    target_quran_to_ayah, kind, method, method_detail, confidence,
    review_status, evidence_text, evidence_normalized, normalization_version,
    origin, origin_key, metadata, created_at, updated_at
)
SELECT
    qbr.id,
    'kitab/' || qbr.book_id::text || '/h/' || qbr.heading_id::text,
    CASE
        WHEN qbr.from_ayah_number = qbr.to_ayah_number
            THEN 'quran/' || qbr.surah_id::text || ':' || qbr.from_ayah_number::text
        ELSE 'quran/' || qbr.surah_id::text || ':' || qbr.from_ayah_number::text
            || '..quran/' || qbr.surah_id::text || ':' || qbr.to_ayah_number::text
    END,
    'kitab', 'quran', qbr.book_id, qbr.surah_id,
    qbr.from_ayah_number, qbr.to_ayah_number,
    CASE WHEN qbr.reference_kind = 'quote' THEN 'quotes' ELSE 'cites' END,
    'resolver', jsonb_build_object('strategy', qbr.match_strategy),
    qbr.confidence, qbr.review_status, qbr.source_text, qbr.normalized_text, 1,
    'legacy_quran_reference', qbr.id::text, qbr.metadata,
    qbr.created_at, qbr.updated_at
FROM quran_book_references qbr
WHERE qbr.id = ANY($1::uuid[])`, ids)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_cross_reference_bridge (
    cross_reference_id, book_id, page_id, heading_id, knowledge_mention_id,
    source_text, normalized_text, normalization_version, reference_kind, surah_id, from_ayah_number,
    to_ayah_number, from_ayah_key, to_ayah_key, match_strategy, metadata,
    created_at, updated_at
)
SELECT
    id, book_id, page_id, heading_id, knowledge_mention_id,
    source_text, normalized_text, normalization_version, reference_kind, surah_id, from_ayah_number,
    to_ayah_number, from_ayah_key, to_ayah_key, match_strategy, metadata,
    created_at, updated_at
FROM quran_book_references
WHERE id = ANY($1::uuid[])`, ids)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit partial Quran bridge: %v", err)
	}
}

func getQuranReferenceContract(
	t *testing.T,
	path string,
) ([]byte, quranReferenceCompatibilityList) {
	t.Helper()

	resp := doJSON(t, http.MethodGet, baseURL()+path, nil, "")
	raw := readAndCloseResponse(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, resp.StatusCode, raw)
	}

	var result quranReferenceCompatibilityList
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode Quran reference contract %s: %v", path, err)
	}

	return raw, result
}

func getReaderQuranReferenceContract(
	t *testing.T,
	path string,
) ([]byte, readerQuranReferenceCompatibility) {
	t.Helper()

	resp := doJSON(t, http.MethodGet, baseURL()+path, nil, "")
	raw := readAndCloseResponse(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, resp.StatusCode, raw)
	}

	var result readerQuranReferenceCompatibility
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode reader Quran embed %s: %v", path, err)
	}

	return raw, result
}

func readAndCloseResponse(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s response: %v", resp.Request.Method, resp.Request.URL, err)
	}

	return raw
}

func assertQuranReferenceContract(t *testing.T, result quranReferenceCompatibilityList) {
	t.Helper()

	if result.Total != 3 || len(result.Items) != 3 {
		t.Fatalf("Quran reference result = total %d/items %d, want 3/3: %+v", result.Total, len(result.Items), result)
	}
	assertReferenceIDs(t, result.Items, []string{
		crossReferenceCompatRangeID,
		crossReferenceCompatPointID,
		crossReferenceCompatPage2ID,
	})

	wantAyahs := [][]string{
		{
			fmt.Sprintf("%d:%d", crossReferenceSurahID, crossReferenceAyahFrom),
			fmt.Sprintf("%d:%d", crossReferenceSurahID, crossReferenceAyahMiddle),
		},
		{fmt.Sprintf("%d:%d", crossReferenceSurahID, crossReferenceAyahTo)},
		{fmt.Sprintf("%d:%d", crossReferenceSurahID, crossReferenceAyahOutside)},
	}
	wantNormalizationVersions := []string{"1", "null", "1"}
	for index, item := range result.Items {
		if item.ReviewStatus != "approved" {
			t.Fatalf("reference %s status = %q, want approved", item.ID, item.ReviewStatus)
		}
		if string(item.NormalizationVersion) != wantNormalizationVersions[index] {
			t.Fatalf(
				"reference %s normalization_version = %s, want %s",
				item.ID,
				item.NormalizationVersion,
				wantNormalizationVersions[index],
			)
		}
		if len(item.Ayahs) != len(wantAyahs[index]) {
			t.Fatalf("reference %s ayahs = %d, want %d", item.ID, len(item.Ayahs), len(wantAyahs[index]))
		}
		for ayahIndex, ayah := range item.Ayahs {
			if ayah.AyahKey != wantAyahs[index][ayahIndex] {
				t.Fatalf(
					"reference %s ayah[%d] = %q, want %q",
					item.ID,
					ayahIndex,
					ayah.AyahKey,
					wantAyahs[index][ayahIndex],
				)
			}
		}
	}
}

func assertReferenceIDs(t *testing.T, items []quranReferenceCompatibilityItem, want []string) {
	t.Helper()

	if len(items) != len(want) {
		t.Fatalf("reference count = %d, want %d: %+v", len(items), len(want), items)
	}
	for index, id := range want {
		if items[index].ID != id {
			t.Fatalf("reference[%d] id = %q, want %q", index, items[index].ID, id)
		}
	}
}

func assertJSONSemanticallyEqual(t *testing.T, wantRaw, gotRaw []byte, label string) {
	t.Helper()

	var want, got any
	if err := json.Unmarshal(wantRaw, &want); err != nil {
		t.Fatalf("decode expected %s JSON: %v", label, err)
	}
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		t.Fatalf("decode actual %s JSON: %v", label, err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s changed during partial backfill\nbefore: %s\nafter:  %s", label, wantRaw, gotRaw)
	}
}
