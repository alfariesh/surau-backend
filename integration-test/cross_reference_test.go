//nolint:wsl_v5 // SQL-heavy integration fixtures stay clearer when setup statements are grouped
package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
)

const (
	crossReferenceCategoryID  = 992030
	crossReferenceAuthorID    = 992030
	crossReferenceBookAID     = 992031
	crossReferenceBookBID     = 992032
	crossReferenceSurahID     = 109
	crossReferenceAyahFrom    = 991
	crossReferenceAyahMiddle  = 992
	crossReferenceAyahTo      = 993
	crossReferenceAyahOutside = 994
)

const (
	crossReferenceUnitA1ID = "00000000-0000-0000-0000-00000000b301"
	crossReferenceUnitA2ID = "00000000-0000-0000-0000-00000000b302"
	crossReferenceUnitB1ID = "00000000-0000-0000-0000-00000000b303"
	crossReferenceUnitB2ID = "00000000-0000-0000-0000-00000000b304"
)

// TestCrossReferencePublicAndEditorialContract proves B-3 AC-2 and its
// decision-support count against the real API and PostgreSQL stack:
// human links stay private through every non-approved review state, the same
// approved edge is queryable exactly once in both directions, and Quran range
// containment counts distinct source Works rather than raw edges.
func TestCrossReferencePublicAndEditorialContract(t *testing.T) {
	seedCrossReferenceFixture(t)
	t.Cleanup(func() { cleanupCrossReferenceFixture(t) })

	token := adminJWT(t)
	anchorA1 := crossReferenceUnitAnchor(crossReferenceBookAID, 1)
	anchorA2 := crossReferenceUnitAnchor(crossReferenceBookAID, 2)
	anchorB1 := crossReferenceUnitAnchor(crossReferenceBookBID, 1)
	anchorB2 := crossReferenceUnitAnchor(crossReferenceBookBID, 2)

	t.Run("non-approved review states stay hidden", func(t *testing.T) {
		created, etag := createCrossReference(
			t,
			token,
			anchorA2,
			anchorB2,
			entity.CrossReferenceKindExplains,
			"B-3 hidden review-state evidence",
		)
		if created.ReviewStatus != entity.CrossReferenceStatusPending {
			t.Fatalf("new human Cross-Reference status = %q, want pending", created.ReviewStatus)
		}

		assertPublicCrossReferenceEmpty(t, anchorA2, entity.CrossReferenceDirectionOutgoing, "")

		for _, status := range []string{
			entity.CrossReferenceStatusRejected,
			entity.CrossReferenceStatusAmbiguous,
			entity.CrossReferenceStatusNeedsReview,
		} {
			created, etag = reviewCrossReference(t, token, created.ID, status, etag)
			if created.ReviewStatus != status {
				t.Fatalf("review status = %q, want %q", created.ReviewStatus, status)
			}

			assertPublicCrossReferenceEmpty(t, anchorA2, entity.CrossReferenceDirectionOutgoing, "")
		}
	})

	t.Run("kitab to kitab quote appears once after approval in both directions", func(t *testing.T) {
		created, etag := createCrossReference(
			t,
			token,
			anchorA1,
			anchorB1,
			entity.CrossReferenceKindQuotes,
			"B-3 kitab to kitab quote evidence",
		)

		assertPublicCrossReferenceEmpty(
			t,
			anchorA1,
			entity.CrossReferenceDirectionOutgoing,
			entity.CrossReferenceKindQuotes,
		)
		assertPublicCrossReferenceEmpty(
			t,
			anchorB1,
			entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindQuotes,
		)

		approved, _ := reviewCrossReference(
			t,
			token,
			created.ID,
			entity.CrossReferenceStatusApproved,
			etag,
		)
		if approved.Method != entity.CrossReferenceMethodHuman || approved.MethodDetail.ActorID == "" {
			t.Fatalf("approved human attribution = %+v", approved)
		}

		outgoing := getPublicCrossReferences(
			t,
			anchorA1,
			entity.CrossReferenceDirectionOutgoing,
			entity.CrossReferenceKindQuotes,
		)
		incoming := getPublicCrossReferences(
			t,
			anchorB1,
			entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindQuotes,
		)

		assertOneCrossReference(t, outgoing, created.ID, crossReferenceBookBID)
		assertOneCrossReference(t, incoming, created.ID, crossReferenceBookAID)
	})

	t.Run("Quran range containment reports distinct source Works", func(t *testing.T) {
		targetRange := fmt.Sprintf(
			"quran/%d:%d..quran/%d:%d",
			crossReferenceSurahID,
			crossReferenceAyahFrom,
			crossReferenceSurahID,
			crossReferenceAyahTo,
		)

		for index, source := range []string{anchorA1, anchorA2, anchorB1} {
			created, etag := createCrossReference(
				t,
				token,
				source,
				targetRange,
				entity.CrossReferenceKindCites,
				fmt.Sprintf("B-3 Quran range evidence %d", index+1),
			)
			reviewCrossReference(t, token, created.ID, entity.CrossReferenceStatusApproved, etag)
		}

		// A surah-only edge is intentionally present as a tripwire: querying one
		// ayah in that surah must not treat the broad surah claim as an ayah hit.
		surahOnly := fmt.Sprintf("quran/%d", crossReferenceSurahID)
		created, etag := createCrossReference(
			t,
			token,
			anchorB2,
			surahOnly,
			entity.CrossReferenceKindCites,
			"B-3 surah-only containment tripwire",
		)
		reviewCrossReference(t, token, created.ID, entity.CrossReferenceStatusApproved, etag)

		inside := fmt.Sprintf("quran/%d:%d", crossReferenceSurahID, crossReferenceAyahMiddle)
		result := getPublicCrossReferences(
			t,
			inside,
			entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindCites,
		)
		if result.Total != 3 || len(result.Items) != 3 {
			t.Fatalf("range-containing edges = total %d items %d, want 3/3: %+v", result.Total, len(result.Items), result)
		}
		if result.WorkTotal != 2 {
			t.Fatalf("range-containing work_total = %d, want 2 distinct kitab", result.WorkTotal)
		}

		emptyPage := getPublicCrossReferencesPage(
			t,
			inside,
			entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindCites,
			2,
			10,
		)
		if len(emptyPage.Items) != 0 || emptyPage.Total != 3 || emptyPage.WorkTotal != 2 {
			t.Fatalf("empty page lost full counts: %+v", emptyPage)
		}

		seenWorks := map[int]int{}
		for _, item := range result.Items {
			if item.SourceWorkID == nil {
				t.Fatalf("range edge source_work_id is nil: %+v", item)
			}
			seenWorks[*item.SourceWorkID]++
			if item.TargetAnchor != targetRange {
				t.Fatalf("range edge target_anchor = %q, want %q", item.TargetAnchor, targetRange)
			}
		}
		if seenWorks[crossReferenceBookAID] != 2 || seenWorks[crossReferenceBookBID] != 1 {
			t.Fatalf("range edge Work distribution = %v, want A:2 B:1", seenWorks)
		}

		outside := fmt.Sprintf("quran/%d:%d", crossReferenceSurahID, crossReferenceAyahOutside)
		assertPublicCrossReferenceEmpty(
			t,
			outside,
			entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindCites,
		)

		surahResult := getPublicCrossReferences(
			t,
			surahOnly,
			entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindCites,
		)
		assertOneCrossReference(t, surahResult, created.ID, crossReferenceBookBID)
	})
}

func seedCrossReferenceFixture(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Cross-Reference fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	execFixtureSQL(t, ctx, tx, `
DELETE FROM cross_references
WHERE source_work_id = ANY($1) OR target_work_id = ANY($1)`,
		[]int{crossReferenceBookAID, crossReferenceBookBID})
	execFixtureSQL(t, ctx, tx, `DELETE FROM books WHERE id = ANY($1)`, []int{crossReferenceBookAID, crossReferenceBookBID})
	execFixtureSQL(t, ctx, tx, `
DELETE FROM quran_ayahs
WHERE surah_id = $1 AND ayah_number BETWEEN $2 AND $3`,
		crossReferenceSurahID, crossReferenceAyahFrom, crossReferenceAyahOutside)

	execFixtureSQL(t, ctx, tx, `
INSERT INTO categories (id, name, display_order)
VALUES ($1, 'B-3 Cross-Reference Fixture', $1)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, is_deleted = FALSE`, crossReferenceCategoryID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO authors (id, name)
VALUES ($1, 'B-3 Cross-Reference Fixture Author')
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, is_deleted = FALSE`, crossReferenceAuthorID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO books (id, name, category_id, author_id, has_content, is_deleted, updated_at, license_status)
VALUES
    ($1, 'B-3 Published Work A', $3, $4, TRUE, FALSE, '2026-07-10T00:00:00Z', 'unknown'),
    ($2, 'B-3 Published Work B', $3, $4, TRUE, FALSE, '2026-07-10T00:00:00Z', 'unknown')`,
		crossReferenceBookAID, crossReferenceBookBID, crossReferenceCategoryID, crossReferenceAuthorID)
	permitBookFixtures(ctx, t, tx, crossReferenceBookAID, crossReferenceBookBID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_publications (book_id, status, featured, published_at, updated_at)
VALUES
    ($1, 'published', FALSE, '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z'),
    ($2, 'published', FALSE, '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z')`,
		crossReferenceBookAID, crossReferenceBookBID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text, updated_at)
VALUES
    ($1, 1, '<p>B-3 Work A</p>', 'B-3 Work A', '2026-07-10T00:00:00Z'),
    ($2, 1, '<p>B-3 Work B</p>', 'B-3 Work B', '2026-07-10T00:00:00Z')`,
		crossReferenceBookAID, crossReferenceBookBID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content, updated_at)
VALUES
    ($1, 1, 1, 0, 1, 'B-3 Heading A', '2026-07-10T00:00:00Z'),
    ($2, 1, 1, 0, 1, 'B-3 Heading B', '2026-07-10T00:00:00Z')`,
		crossReferenceBookAID, crossReferenceBookBID)

	insertCrossReferenceUnit(t, ctx, tx, crossReferenceUnitA1ID, crossReferenceBookAID, 1, 0)
	insertCrossReferenceUnit(t, ctx, tx, crossReferenceUnitA2ID, crossReferenceBookAID, 2, 1)
	insertCrossReferenceUnit(t, ctx, tx, crossReferenceUnitB1ID, crossReferenceBookBID, 1, 0)
	insertCrossReferenceUnit(t, ctx, tx, crossReferenceUnitB2ID, crossReferenceBookBID, 2, 1)

	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_surahs (
    surah_id, name_arabic, name_latin, name_translation, revelation_type, ayah_count, metadata
) VALUES (
    $1, 'الكافرون', 'Al-Kafirun', 'Orang-orang Kafir', 'makkiyah', $2,
    '{"integration_fixture":"b3-cross-reference"}'::jsonb
)
ON CONFLICT (surah_id) DO NOTHING`, crossReferenceSurahID, crossReferenceAyahOutside)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple, search_text, updated_at
)
SELECT $1::integer,
       ayah_number,
       ($1::integer)::text || ':' || ayah_number::text,
       'B-3 Quran fixture ' || ayah_number::text,
       'B-3 Quran fixture ' || ayah_number::text,
       'b-3 quran fixture ' || ayah_number::text,
       '2026-07-10T00:00:00Z'
FROM generate_series($2::integer, $3::integer) AS ayah_number`,
		crossReferenceSurahID, crossReferenceAyahFrom, crossReferenceAyahOutside)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit Cross-Reference fixture: %v", err)
	}
}

//nolint:revive // test helpers conventionally keep testing.T before context
func insertCrossReferenceUnit(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	id string,
	bookID, ordinal, position int,
) {
	t.Helper()

	anchor := crossReferenceUnitAnchor(bookID, ordinal)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO citable_units (
    id, corpus, book_id, heading_id, page_id, kind, ordinal, position, anchor,
    text, text_normalized, normalization_version, content_hash, occurrence,
    language, provenance_class, lifecycle, updated_at
) VALUES (
    $1::uuid, 'kitab', $2, 1, 1, 'paragraph', $3, $4, $5,
    $6, $6, 1, decode(md5($1::text), 'hex'), 1,
    'ar', 'source', 'active', '2026-07-10T00:00:00Z'
)`, id, bookID, ordinal, position, anchor, "B-3 Cross-Reference unit "+id)
}

func cleanupCrossReferenceFixture(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Cross-Reference cleanup: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	execFixtureSQL(t, ctx, tx, `
DELETE FROM cross_references
WHERE source_work_id = ANY($1) OR target_work_id = ANY($1)`,
		[]int{crossReferenceBookAID, crossReferenceBookBID})
	execFixtureSQL(t, ctx, tx, `DELETE FROM books WHERE id = ANY($1)`, []int{crossReferenceBookAID, crossReferenceBookBID})
	execFixtureSQL(t, ctx, tx, `DELETE FROM categories WHERE id = $1`, crossReferenceCategoryID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM authors WHERE id = $1`, crossReferenceAuthorID)
	execFixtureSQL(t, ctx, tx, `
DELETE FROM quran_ayahs
WHERE surah_id = $1 AND ayah_number BETWEEN $2 AND $3`,
		crossReferenceSurahID, crossReferenceAyahFrom, crossReferenceAyahOutside)
	execFixtureSQL(t, ctx, tx, `
DELETE FROM quran_surahs
WHERE surah_id = $1 AND metadata->>'integration_fixture' = 'b3-cross-reference'`, crossReferenceSurahID)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit Cross-Reference cleanup: %v", err)
	}
}

func crossReferenceUnitAnchor(bookID, ordinal int) string {
	return fmt.Sprintf("kitab/%d/h/1/u/%d", bookID, ordinal)
}

func createCrossReference(
	t *testing.T,
	token, source, target, kind, evidence string,
) (created entity.CrossReference, etag string) {
	t.Helper()

	body := fmt.Sprintf(`{
        "source_anchor": %q,
        "target_anchor": %q,
        "kind": %q,
        "confidence": 0.95,
        "evidence_text": %q,
        "metadata": {"integration_fixture":"b3-cross-reference"}
    }`, source, target, kind, evidence)
	resp := doJSON(
		t,
		http.MethodPost,
		baseURL()+"/v1/editorial/cross-references",
		bytes.NewBufferString(body),
		token,
	)
	etag = resp.Header.Get("ETag")

	decodeAndClose(t, resp, &created)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create Cross-Reference status = %d, want 201: %+v", resp.StatusCode, created)
	}
	if etag == "" {
		t.Fatal("create Cross-Reference did not return ETag")
	}
	if created.SourceAnchor != source || created.TargetAnchor != target || created.Kind != kind {
		t.Fatalf("created Cross-Reference = %+v", created)
	}

	return created, etag
}

func reviewCrossReference(
	t *testing.T,
	token, id, status, etag string,
) (reviewed entity.CrossReference, nextETag string) {
	t.Helper()

	body := strings.NewReader(fmt.Sprintf(`{"review_status":%q}`, status))
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		baseURL()+"/v1/editorial/cross-references/"+id+"/review",
		body,
	)
	if err != nil {
		t.Fatalf("new Cross-Reference review request: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("If-Match", etag)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("review Cross-Reference: %v", err)
	}
	nextETag = resp.Header.Get("ETag")

	decodeAndClose(t, resp, &reviewed)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("review Cross-Reference status = %d, want 200: %+v", resp.StatusCode, reviewed)
	}
	if nextETag == "" {
		t.Fatal("review Cross-Reference did not return ETag")
	}

	return reviewed, nextETag
}

func getPublicCrossReferences(t *testing.T, anchor, direction, kind string) entity.CrossReferenceList {
	t.Helper()

	return getPublicCrossReferencesPage(t, anchor, direction, kind, 200, 0)
}

func getPublicCrossReferencesPage(
	t *testing.T,
	anchor, direction, kind string,
	limit, offset int,
) entity.CrossReferenceList {
	t.Helper()

	query := url.Values{}
	query.Set("anchor", anchor)
	query.Set("direction", direction)
	query.Set("limit", fmt.Sprintf("%d", limit))
	query.Set("offset", fmt.Sprintf("%d", offset))
	if kind != "" {
		query.Set("kind", kind)
	}

	resp := doJSON(
		t,
		http.MethodGet,
		baseURL()+"/v1/cross-references?"+query.Encode(),
		nil,
		"",
	)

	var result entity.CrossReferenceList
	decodeAndClose(t, resp, &result)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list public Cross-References status = %d, want 200: %+v", resp.StatusCode, result)
	}

	return result
}

func assertPublicCrossReferenceEmpty(t *testing.T, anchor, direction, kind string) {
	t.Helper()

	result := getPublicCrossReferences(t, anchor, direction, kind)
	if result.Total != 0 || result.WorkTotal != 0 || len(result.Items) != 0 {
		t.Fatalf("public Cross-References for %s/%s/%s leaked rows: %+v", anchor, direction, kind, result)
	}
}

func assertOneCrossReference(t *testing.T, result entity.CrossReferenceList, id string, oppositeWorkID int) {
	t.Helper()

	if result.Total != 1 || result.WorkTotal != 1 || len(result.Items) != 1 {
		t.Fatalf("Cross-Reference list = total %d work_total %d items %d, want 1/1/1: %+v",
			result.Total, result.WorkTotal, len(result.Items), result)
	}
	if result.Items[0].ID != id {
		t.Fatalf("Cross-Reference id = %q, want %q", result.Items[0].ID, id)
	}

	item := result.Items[0]
	if (item.SourceWorkID == nil || *item.SourceWorkID != oppositeWorkID) &&
		(item.TargetWorkID == nil || *item.TargetWorkID != oppositeWorkID) {
		t.Fatalf("Cross-Reference opposing Work %d absent: %+v", oppositeWorkID, item)
	}
}
