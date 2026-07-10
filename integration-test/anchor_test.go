//nolint:wsl_v5 // SQL-heavy integration fixtures stay clearer when setup statements are grouped
package integration_test

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
)

const (
	anchorFixtureCategoryID = 992010
	anchorFixtureAuthorID   = 992010
	anchorFixtureBookID     = 992010
	anchorHiddenBookID      = 992011
	anchorFallbackBookID    = 992012
	anchorFixtureHeadingID  = 101
	anchorFixturePageID     = 101
	anchorFixtureAyahKey    = "110:999"

	anchorPerformanceCategoryID   = 992020
	anchorPerformanceAuthorID     = 992020
	anchorPerformanceBookID       = 992020
	anchorPerformanceHeadingStart = 2000
	anchorPerformancePageStart    = 1000
	anchorPerformanceUnitCount    = 20500
	anchorPerformanceHistoryCount = 20500
	anchorPerformanceShadowStart  = 993000
	anchorPerformanceShadowCount  = 2000
)

const (
	anchorFixtureRootID        = "00000000-0000-0000-0000-00000000b201"
	anchorFixtureMiddleID      = "00000000-0000-0000-0000-00000000b202"
	anchorFixtureFirstActiveID = "00000000-0000-0000-0000-00000000b203"
	anchorFixtureLastActiveID  = "00000000-0000-0000-0000-00000000b204"
	anchorFixtureTombstoneID   = "00000000-0000-0000-0000-00000000b205"
	anchorFixtureMergeRootID   = "00000000-0000-0000-0000-00000000b206"
	anchorFixtureMergeTargetID = "00000000-0000-0000-0000-00000000b207"
	anchorFixtureCycleAID      = "00000000-0000-0000-0000-00000000b208"
	anchorFixtureCycleBID      = "00000000-0000-0000-0000-00000000b209"
	anchorFixtureHiddenUnitID  = "00000000-0000-0000-0000-00000000b210"
)

func TestAnchorResolverPublicContract(t *testing.T) {
	seedAnchorFixture(t)
	t.Cleanup(func() { cleanupAnchorFixture(t) })

	t.Run("canonical Work", func(t *testing.T) {
		resolved := getAnchorResolution(t, fmt.Sprintf("anchor=kitab%%2F%d", anchorFixtureBookID))
		assertCanonicalAnchor(t, resolved, fmt.Sprintf("kitab/%d", anchorFixtureBookID))
		target := onlyAnchorTarget(t, resolved)
		if target.TargetType != entity.AnchorTargetBook || target.NavigationURL != fmt.Sprintf("/v1/books/%d", anchorFixtureBookID) {
			t.Fatalf("canonical Work target = %+v", target)
		}
	})

	t.Run("canonical heading returns all active Citable Units", func(t *testing.T) {
		canonical := fmt.Sprintf("kitab/%d/h/%d", anchorFixtureBookID, anchorFixtureHeadingID)
		resolved := getAnchorResolution(t, "anchor="+url.QueryEscape(canonical))
		assertCanonicalAnchor(t, resolved, canonical)
		boundary := onlyAnchorBoundary(t, resolved)
		if len(boundary.ActiveTargets) != 3 {
			t.Fatalf("heading active targets = %d, want 3: %+v", len(boundary.ActiveTargets), boundary.ActiveTargets)
		}
		assertAnchorUnitOrder(t, boundary.ActiveTargets, []string{
			anchorFixtureFirstActiveID,
			anchorFixtureLastActiveID,
			anchorFixtureMergeTargetID,
		})
	})

	t.Run("canonical Quran ayah", func(t *testing.T) {
		canonical := "quran/" + anchorFixtureAyahKey
		resolved := getAnchorResolution(t, "anchor="+url.QueryEscape(canonical))
		assertCanonicalAnchor(t, resolved, canonical)
		target := onlyAnchorTarget(t, resolved)
		if target.TargetType != entity.AnchorTargetQuranAyah || target.AyahKey == nil || *target.AyahKey != anchorFixtureAyahKey {
			t.Fatalf("canonical Quran target = %+v", target)
		}
		if target.NavigationURL != "/v1/quran/ayahs/"+anchorFixtureAyahKey {
			t.Fatalf("canonical Quran navigation URL = %q", target.NavigationURL)
		}
	})

	t.Run("canonical range resolves two boundaries without expansion", func(t *testing.T) {
		start := fmt.Sprintf("kitab/%d/h/%d/u/1", anchorFixtureBookID, anchorFixtureHeadingID)
		end := fmt.Sprintf("kitab/%d/h/%d/u/4", anchorFixtureBookID, anchorFixtureHeadingID)
		resolved := getAnchorResolution(t, "anchor="+url.QueryEscape(start+".."+end))
		if len(resolved.Boundaries) != 2 {
			t.Fatalf("range boundaries = %d, want 2", len(resolved.Boundaries))
		}
		if resolved.Boundaries[0].Role != entity.AnchorBoundaryStart || resolved.Boundaries[1].Role != entity.AnchorBoundaryEnd {
			t.Fatalf("range roles = %q, %q", resolved.Boundaries[0].Role, resolved.Boundaries[1].Role)
		}
		if len(resolved.Boundaries[0].ActiveTargets) != 2 || len(resolved.Boundaries[1].ActiveTargets) != 1 {
			t.Fatalf("range boundary targets = %d, %d", len(resolved.Boundaries[0].ActiveTargets), len(resolved.Boundaries[1].ActiveTargets))
		}
	})

	t.Run("legacy ayah_key remains a permanent alias", func(t *testing.T) {
		resolved := getAnchorResolution(t, "anchor="+url.QueryEscape(anchorFixtureAyahKey))
		if resolved.Requested.Form != entity.AnchorFormLegacyAyahKey {
			t.Fatalf("legacy ayah requested form = %q", resolved.Requested.Form)
		}
		assertCanonicalAnchor(t, resolved, "quran/"+anchorFixtureAyahKey)
		redirect := onlyAnchorRedirect(t, resolved)
		if redirect.From != anchorFixtureAyahKey || redirect.To != "quran/"+anchorFixtureAyahKey || redirect.Depth != 1 {
			t.Fatalf("legacy ayah redirect = %+v", redirect)
		}
	})

	t.Run("legacy toc remains scoped and returns every active unit", func(t *testing.T) {
		resolved := getAnchorResolution(t, fmt.Sprintf("anchor=toc-%d&book_id=%d", anchorFixtureHeadingID, anchorFixtureBookID))
		if resolved.Requested.Form != entity.AnchorFormLegacyTOC || resolved.Requested.BookID == nil || *resolved.Requested.BookID != anchorFixtureBookID {
			t.Fatalf("legacy toc requested = %+v", resolved.Requested)
		}
		assertCanonicalAnchor(t, resolved, fmt.Sprintf("kitab/%d/h/%d", anchorFixtureBookID, anchorFixtureHeadingID))
		boundary := onlyAnchorBoundary(t, resolved)
		if len(boundary.ActiveTargets) != 3 {
			t.Fatalf("legacy toc active targets = %d, want 3", len(boundary.ActiveTargets))
		}
		if len(boundary.RedirectChain) != 1 || boundary.RedirectChain[0].Reason != entity.AnchorRedirectLegacyAlias {
			t.Fatalf("legacy toc redirects = %+v", boundary.RedirectChain)
		}
	})

	t.Run("legacy page returns multiple targets without inventing a canonical Anchor", func(t *testing.T) {
		resolved := getAnchorResolution(t, fmt.Sprintf("book_id=%d&page_id=%d", anchorFixtureBookID, anchorFixturePageID))
		if resolved.Requested.Form != entity.AnchorFormLegacyPage || resolved.CanonicalAnchor != nil {
			t.Fatalf("legacy page response = %+v", resolved)
		}
		boundary := onlyAnchorBoundary(t, resolved)
		if len(boundary.ActiveTargets) != 3 || len(boundary.RedirectChain) != 3 {
			t.Fatalf("legacy page targets/redirects = %d/%d", len(boundary.ActiveTargets), len(boundary.RedirectChain))
		}
		for _, redirect := range boundary.RedirectChain {
			if redirect.From != fmt.Sprintf("page:%d:%d", anchorFixtureBookID, anchorFixturePageID) || redirect.Depth != 1 {
				t.Fatalf("legacy page redirect = %+v", redirect)
			}
		}

		// Nullable is an explicit wire contract, not permission to omit the
		// field: both the response and its point boundary must serialize null.
		query := fmt.Sprintf("book_id=%d&page_id=%d", anchorFixtureBookID, anchorFixturePageID)
		resp := doJSON(t, http.MethodGet, baseURL()+"/v1/anchors/resolve?"+query, nil, "")
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read raw legacy page response: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("raw legacy page response status = %d: %s", resp.StatusCode, raw)
		}
		if count := strings.Count(string(raw), `"canonical_anchor":null`); count != 2 {
			t.Fatalf("legacy page explicit canonical_anchor null count = %d, want 2: %s", count, raw)
		}
	})

	t.Run("non-pilot heading and page fall back to active source rows", func(t *testing.T) {
		headingID, pageID := 201, 201
		toc := getAnchorResolution(t, fmt.Sprintf("anchor=toc-%d&book_id=%d", headingID, anchorFallbackBookID))
		tocTarget := onlyAnchorTarget(t, toc)
		if tocTarget.TargetType != entity.AnchorTargetBookHeading || tocTarget.UnitID != nil {
			t.Fatalf("fallback toc target = %+v", tocTarget)
		}

		page := getAnchorResolution(t, fmt.Sprintf("book_id=%d&page_id=%d", anchorFallbackBookID, pageID))
		pageTarget := onlyAnchorTarget(t, page)
		if pageTarget.TargetType != entity.AnchorTargetBookPage || pageTarget.CanonicalAnchor != nil || pageTarget.UnitID != nil {
			t.Fatalf("fallback page target = %+v", pageTarget)
		}
	})

	t.Run("superseded Anchor follows all multi-hop split successors", func(t *testing.T) {
		root := fmt.Sprintf("kitab/%d/h/%d/u/1", anchorFixtureBookID, anchorFixtureHeadingID)
		resolved := getAnchorResolution(t, "anchor="+url.QueryEscape(root))
		boundary := onlyAnchorBoundary(t, resolved)
		if boundary.Status != entity.UnitLifecycleSuperseded {
			t.Fatalf("root lifecycle = %q", boundary.Status)
		}
		assertAnchorUnitOrder(t, boundary.ActiveTargets, []string{anchorFixtureFirstActiveID, anchorFixtureLastActiveID})
		if len(boundary.RedirectChain) != 3 {
			t.Fatalf("root redirect chain = %+v", boundary.RedirectChain)
		}
		depths := []int{boundary.RedirectChain[0].Depth, boundary.RedirectChain[1].Depth, boundary.RedirectChain[2].Depth}
		if fmt.Sprint(depths) != "[1 2 2]" {
			t.Fatalf("root redirect depths = %v, want [1 2 2]", depths)
		}
	})

	t.Run("merge predecessor resolves to its one active target", func(t *testing.T) {
		root := fmt.Sprintf("kitab/%d/h/%d/u/6", anchorFixtureBookID, anchorFixtureHeadingID)
		resolved := getAnchorResolution(t, "anchor="+url.QueryEscape(root))
		boundary := onlyAnchorBoundary(t, resolved)
		assertAnchorUnitOrder(t, boundary.ActiveTargets, []string{anchorFixtureMergeTargetID})
		redirect := onlyAnchorRedirect(t, resolved)
		if redirect.Depth != 1 || redirect.Reason != "content_move" {
			t.Fatalf("merge redirect = %+v", redirect)
		}
	})

	t.Run("known tombstone returns 200 with no active target", func(t *testing.T) {
		anchor := fmt.Sprintf("kitab/%d/h/%d/u/5", anchorFixtureBookID, anchorFixtureHeadingID)
		resolved := getAnchorResolution(t, "anchor="+url.QueryEscape(anchor))
		boundary := onlyAnchorBoundary(t, resolved)
		if boundary.Status != entity.UnitLifecycleTombstoned || len(boundary.ActiveTargets) != 0 {
			t.Fatalf("tombstone boundary = %+v", boundary)
		}
	})

	t.Run("cycle is surfaced as a safe 500 envelope", func(t *testing.T) {
		anchor := fmt.Sprintf("kitab/%d/h/%d/u/8", anchorFixtureBookID, anchorFixtureHeadingID)
		assertAnchorError(t, "anchor="+url.QueryEscape(anchor), http.StatusInternalServerError, "internal server error", "internal_server_error")
	})

	t.Run("unknown and unpublished Anchors are indistinguishable", func(t *testing.T) {
		assertAnchorError(t, "anchor=kitab%2F2147483647", http.StatusNotFound, "anchor not found", "anchor_not_found")
		assertAnchorError(t, fmt.Sprintf("anchor=kitab%%2F%d", anchorHiddenBookID), http.StatusNotFound, "anchor not found", "anchor_not_found")
		hiddenUnit := fmt.Sprintf("kitab/%d/h/301/u/1", anchorHiddenBookID)
		assertAnchorError(t, "anchor="+url.QueryEscape(hiddenUnit), http.StatusNotFound, "anchor not found", "anchor_not_found")
	})

	t.Run("invalid ambiguous and missing scope shapes are rejected", func(t *testing.T) {
		queries := []string{
			"",
			"anchor=toc-101",
			"anchor=" + url.QueryEscape("quran/110:999") + "&book_id=992010",
			"anchor=" + url.QueryEscape("kitab/992010") + "&page_id=101",
			"book_id=992010",
			"page_id=101",
			"book_id=nope&page_id=101",
			"anchor=toc-101&book_id=0992010",
			"book_id=992010&page_id=0101",
			"anchor=quran%2F110%3A999&anchor=quran%2F110%3A998",
			"book_id=992010&book_id=992012&page_id=101",
			"book_id=992010&page_id=101&page_id=102",
			"anchor=quran%2F110%3A999&lang=id",
		}
		for _, query := range queries {
			assertAnchorError(t, query, http.StatusBadRequest, "invalid anchor", "invalid_anchor")
		}
	})

	t.Run("response is compact and supports ETag 304", func(t *testing.T) {
		query := "anchor=" + url.QueryEscape(fmt.Sprintf("kitab/%d/h/%d/u/3", anchorFixtureBookID, anchorFixtureHeadingID))
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL()+"/v1/anchors/resolve?"+query, http.NoBody)
		if err != nil {
			t.Fatalf("new Anchor request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("resolve Anchor: %v", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read Anchor response: %v", err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Anchor response status = %d: %s", resp.StatusCode, body)
		}
		if strings.Contains(string(body), `"text"`) || strings.Contains(string(body), `"html"`) {
			t.Fatalf("Anchor resolver leaked content fields: %s", body)
		}
		etag := resp.Header.Get("ETag")
		if etag == "" || resp.Header.Get("Cache-Control") == "" {
			t.Fatalf("Anchor cache validators missing: %+v", resp.Header)
		}

		conditional, err := http.NewRequestWithContext(t.Context(), http.MethodGet, req.URL.String(), http.NoBody)
		if err != nil {
			t.Fatalf("new conditional Anchor request: %v", err)
		}
		conditional.Header.Set("If-None-Match", etag)
		notModified, err := http.DefaultClient.Do(conditional)
		if err != nil {
			t.Fatalf("conditional Anchor request: %v", err)
		}
		defer notModified.Body.Close()
		if notModified.StatusCode != http.StatusNotModified {
			t.Fatalf("conditional Anchor status = %d, want 304", notModified.StatusCode)
		}
		notModifiedBody, err := io.ReadAll(notModified.Body)
		if err != nil {
			t.Fatalf("read conditional Anchor response: %v", err)
		}
		if len(notModifiedBody) != 0 {
			t.Fatalf("conditional Anchor 304 body = %q, want empty", notModifiedBody)
		}
	})
}

func TestAnchorResolverP95Under50Milliseconds(t *testing.T) {
	seedAnchorPerformanceFixture(t)
	t.Cleanup(func() { cleanupAnchorPerformanceFixture(t) })
	assertAnchorLookupPlans(t)

	heading := anchorPerformanceHeadingStart
	page := anchorPerformancePageStart
	activeFirst := fmt.Sprintf("kitab/%d/h/%d/u/1", anchorPerformanceBookID, heading)
	activeLast := fmt.Sprintf("kitab/%d/h/%d/u/100", anchorPerformanceBookID, heading)
	lineageRoot := fmt.Sprintf("kitab/%d/h/%d/u/101", anchorPerformanceBookID, heading)
	paths := []string{
		fmt.Sprintf("/v1/anchors/resolve?anchor=kitab%%2F%d", anchorPerformanceBookID),
		"/v1/anchors/resolve?anchor=" + url.QueryEscape("quran/"+anchorFixtureAyahKey),
		"/v1/anchors/resolve?anchor=" + url.QueryEscape(anchorFixtureAyahKey),
		"/v1/anchors/resolve?anchor=" + url.QueryEscape(fmt.Sprintf("kitab/%d/h/%d", anchorPerformanceBookID, heading)),
		fmt.Sprintf("/v1/anchors/resolve?anchor=toc-%d&book_id=%d", heading, anchorPerformanceBookID),
		fmt.Sprintf("/v1/anchors/resolve?book_id=%d&page_id=%d", anchorPerformanceBookID, page),
		"/v1/anchors/resolve?anchor=" + url.QueryEscape(activeFirst),
		"/v1/anchors/resolve?anchor=" + url.QueryEscape(lineageRoot),
		"/v1/anchors/resolve?anchor=" + url.QueryEscape(activeFirst+".."+activeLast),
		"/v1/anchors/resolve?anchor=" + url.QueryEscape(fmt.Sprintf("kitab/%d/h/%d/u/100", anchorPerformanceBookID, heading+204)),
	}

	transport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     30 * time.Second,
	}

	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}

	defer transport.CloseIdleConnections()

	const warmupRequests = 50
	for i := range warmupRequests {
		performTimedAnchorRequest(t, client, paths[i%len(paths)])
	}

	const measuredRequests = 500
	durations := make([]time.Duration, 0, measuredRequests)
	for i := range measuredRequests {
		durations = append(durations, performTimedAnchorRequest(t, client, paths[i%len(paths)]))
	}

	slices.Sort(durations)
	p50 := nearestRankDuration(durations, 0.50)
	p95 := nearestRankDuration(durations, 0.95)
	maximum := durations[len(durations)-1]
	t.Logf(
		"Anchor HTTP performance: active_units=%d historical_lineage_units=%d warmup=%d samples=%d p50=%s p95=%s max=%s",
		anchorPerformanceUnitCount,
		anchorPerformanceHistoryCount,
		warmupRequests,
		measuredRequests,
		p50,
		p95,
		maximum,
	)
	if p95 > 50*time.Millisecond {
		t.Fatalf("Anchor HTTP nearest-rank p95 = %s, must be <= 50ms", p95)
	}
}

func seedAnchorFixture(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Anchor fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	execFixtureSQL(t, ctx, tx, `DELETE FROM books WHERE id = ANY($1)`, []int{anchorFixtureBookID, anchorHiddenBookID, anchorFallbackBookID})
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_ayahs WHERE ayah_key = $1`, anchorFixtureAyahKey)
	seedAnchorQuranAyah(t, ctx, tx)

	execFixtureSQL(t, ctx, tx, `
INSERT INTO categories (id, name, display_order) VALUES ($1, 'B-2 Anchor Fixture', 992010)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, is_deleted = FALSE`, anchorFixtureCategoryID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO authors (id, name) VALUES ($1, 'B-2 Anchor Fixture Author')
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, is_deleted = FALSE`, anchorFixtureAuthorID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO books (id, name, category_id, author_id, has_content, is_deleted, updated_at)
VALUES
    ($1, 'B-2 Anchor Published', $4, $5, TRUE, FALSE, '2026-07-10T00:00:00Z'),
    ($2, 'B-2 Anchor Hidden', $4, $5, TRUE, FALSE, '2026-07-10T00:00:00Z'),
    ($3, 'B-2 Anchor Fallback', $4, $5, TRUE, FALSE, '2026-07-10T00:00:00Z')`,
		anchorFixtureBookID, anchorHiddenBookID, anchorFallbackBookID, anchorFixtureCategoryID, anchorFixtureAuthorID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_publications (book_id, status, featured, published_at, updated_at)
VALUES
    ($1, 'published', FALSE, '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z'),
    ($2, 'hidden', FALSE, NULL, '2026-07-10T00:00:00Z'),
    ($3, 'published', FALSE, '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z')`,
		anchorFixtureBookID, anchorHiddenBookID, anchorFallbackBookID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text, updated_at)
VALUES
    ($1, $2, '<p>Anchor fixture</p>', 'Anchor fixture', '2026-07-10T00:00:00Z'),
    ($3, 301, '<p>Hidden</p>', 'Hidden', '2026-07-10T00:00:00Z'),
    ($4, 201, '<p>Fallback</p>', 'Fallback', '2026-07-10T00:00:00Z')`,
		anchorFixtureBookID, anchorFixturePageID, anchorHiddenBookID, anchorFallbackBookID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content, updated_at)
VALUES
    ($1, $2, $3, 0, 1, 'Anchor heading', '2026-07-10T00:00:00Z'),
    ($4, 301, 301, 0, 1, 'Hidden heading', '2026-07-10T00:00:00Z'),
    ($5, 201, 201, 0, 1, 'Fallback heading', '2026-07-10T00:00:00Z')`,
		anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, anchorHiddenBookID, anchorFallbackBookID)

	insertAnchorUnit(t, ctx, tx, anchorFixtureRootID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 1, 0, entity.UnitLifecycleSuperseded)
	insertAnchorUnit(t, ctx, tx, anchorFixtureMiddleID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 2, 0, entity.UnitLifecycleSuperseded)
	insertAnchorUnit(t, ctx, tx, anchorFixtureFirstActiveID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 3, 1, entity.UnitLifecycleActive)
	insertAnchorUnit(t, ctx, tx, anchorFixtureLastActiveID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 4, 2, entity.UnitLifecycleActive)
	insertAnchorUnit(t, ctx, tx, anchorFixtureTombstoneID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 5, 3, entity.UnitLifecycleTombstoned)
	insertAnchorUnit(t, ctx, tx, anchorFixtureMergeRootID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 6, 3, entity.UnitLifecycleSuperseded)
	insertAnchorUnit(t, ctx, tx, anchorFixtureMergeTargetID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 7, 3, entity.UnitLifecycleActive)
	insertAnchorUnit(t, ctx, tx, anchorFixtureCycleAID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 8, 4, entity.UnitLifecycleSuperseded)
	insertAnchorUnit(t, ctx, tx, anchorFixtureCycleBID, anchorFixtureBookID, anchorFixtureHeadingID, anchorFixturePageID, 9, 4, entity.UnitLifecycleSuperseded)
	insertAnchorUnit(t, ctx, tx, anchorFixtureHiddenUnitID, anchorHiddenBookID, 301, 301, 1, 0, entity.UnitLifecycleActive)

	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO citable_unit_lineage (predecessor_id, successor_id, reason) VALUES
    ($1, $2, 'edit'),
    ($2, $3, 'edit'),
    ($2, $4, 'content_move'),
    ($5, $6, 'content_move'),
    ($7, $8, 'edit'),
    ($8, $7, 'edit')`,
		anchorFixtureRootID,
		anchorFixtureMiddleID,
		anchorFixtureFirstActiveID,
		anchorFixtureLastActiveID,
		anchorFixtureMergeRootID,
		anchorFixtureMergeTargetID,
		anchorFixtureCycleAID,
		anchorFixtureCycleBID,
	)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit Anchor fixture: %v", err)
	}
}

//nolint:revive // test helpers conventionally keep testing.T before context
func insertAnchorUnit(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	id string,
	bookID, headingID, pageID, ordinal, position int,
	lifecycle string,
) {
	t.Helper()

	anchor := fmt.Sprintf("kitab/%d/h/%d/u/%d", bookID, headingID, ordinal)
	retiredAt := any(nil)
	if lifecycle != entity.UnitLifecycleActive {
		retiredAt = time.Date(2026, time.July, 10, 0, 0, 0, 0, time.UTC)
	}

	execFixtureSQL(t, ctx, tx, `
INSERT INTO citable_units (
    id, corpus, book_id, heading_id, page_id, kind, ordinal, position, anchor,
    text, text_normalized, normalization_version, content_hash, occurrence,
    language, provenance_class, lifecycle, retired_at, updated_at
) VALUES (
	    $1::uuid, 'kitab', $2, $3, $4, 'paragraph', $5, $6, $7,
    $8, $8, 1, decode(md5($1::text), 'hex'), 1,
    'ar', 'source', $9, $10, '2026-07-10T00:00:00Z'
)`, id, bookID, headingID, pageID, ordinal, position, anchor, "Anchor fixture unit "+id, lifecycle, retiredAt)
}

func seedAnchorPerformanceFixture(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Anchor performance fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	execFixtureSQL(t, ctx, tx, `DELETE FROM books WHERE id = $1`, anchorPerformanceBookID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM books WHERE id >= $1 AND id < $1 + $2`, anchorPerformanceShadowStart, anchorPerformanceShadowCount)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_ayahs WHERE ayah_key = $1`, anchorFixtureAyahKey)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_ayahs WHERE surah_id = 110 AND ayah_number BETWEEN 10000 AND 14999`)
	seedAnchorQuranAyah(t, ctx, tx)
	// A Quran-sized distribution prevents EXPLAIN from legitimately preferring
	// a one-page sequential scan. The queried ayah remains the legacy fixture;
	// these deterministic rows only make the unique ayah_key index choice
	// representative and are removed by cleanup.
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple, search_text, updated_at
)
SELECT 110,
       ayah_number,
       '110:' || ayah_number::text,
       'Performance ayah ' || ayah_number::text,
       'Performance ayah ' || ayah_number::text,
       'performance ayah ' || ayah_number::text,
       '2026-07-10T00:00:00Z'
FROM generate_series(10000, 14999) AS ayah_number`)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO categories (id, name, display_order) VALUES ($1, 'B-2 Performance Fixture', 992020)
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, is_deleted = FALSE`, anchorPerformanceCategoryID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO authors (id, name) VALUES ($1, 'B-2 Performance Fixture Author')
ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, is_deleted = FALSE`, anchorPerformanceAuthorID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO books (id, name, category_id, author_id, has_content, is_deleted, updated_at)
VALUES ($1, 'B-2 Performance 20.5k Units', $2, $3, TRUE, FALSE, '2026-07-10T00:00:00Z')`,
		anchorPerformanceBookID, anchorPerformanceCategoryID, anchorPerformanceAuthorID)
	// Keep dimension-table plans representative too: without a catalog-sized
	// distribution PostgreSQL correctly scans a handful of Work/publication
	// rows, which would make index assertions environment-dependent.
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO books (id, name, category_id, author_id, has_content, is_deleted, updated_at)
SELECT $1 + shadow_offset,
	       'B-2 Shadow Work ' || shadow_offset::text,
       $3,
       $4,
       FALSE,
       FALSE,
       '2026-07-10T00:00:00Z'
FROM generate_series(0, $2 - 1) AS shadow_offset`,
		anchorPerformanceShadowStart,
		anchorPerformanceShadowCount,
		anchorPerformanceCategoryID,
		anchorPerformanceAuthorID,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_publications (book_id, status, featured, published_at, updated_at)
VALUES ($1, 'published', FALSE, '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z')`, anchorPerformanceBookID)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_publications (book_id, status, featured, published_at, updated_at)
SELECT $1 + shadow_offset, 'published', FALSE, '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z'
FROM generate_series(0, $2 - 1) AS shadow_offset`, anchorPerformanceShadowStart, anchorPerformanceShadowCount)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text, updated_at)
SELECT $1 + shadow_offset, 1, '<p>Shadow page</p>', 'Shadow page', '2026-07-10T00:00:00Z'
FROM generate_series(0, $2 - 1) AS shadow_offset`, anchorPerformanceShadowStart, anchorPerformanceShadowCount)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content, updated_at)
SELECT $1 + shadow_offset, 1, 1, 0, 1, 'Shadow heading', '2026-07-10T00:00:00Z'
FROM generate_series(0, $2 - 1) AS shadow_offset`, anchorPerformanceShadowStart, anchorPerformanceShadowCount)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_pages (book_id, page_id, content_html, content_text, updated_at)
SELECT $1, $2 + page_offset, '<p>Performance page</p>', 'Performance page', '2026-07-10T00:00:00Z'
FROM generate_series(0, 512) AS page_offset`, anchorPerformanceBookID, anchorPerformancePageStart)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO book_headings (book_id, heading_id, page_id, depth, ordinal, content, updated_at)
SELECT $1,
       $2 + heading_offset,
       $3 + (heading_offset * 5 / 2),
       0,
       heading_offset + 1,
       'Performance heading ' || heading_offset::text,
       '2026-07-10T00:00:00Z'
FROM generate_series(0, 204) AS heading_offset`,
		anchorPerformanceBookID, anchorPerformanceHeadingStart, anchorPerformancePageStart)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO citable_units (
    id, corpus, book_id, heading_id, page_id, kind, ordinal, position, anchor,
    text, text_normalized, normalization_version, content_hash, occurrence,
    language, provenance_class, lifecycle, updated_at
)
SELECT md5('b2-performance-unit-' || unit_number::text)::uuid,
       'kitab',
	       $1::integer,
	       $2::integer + ((unit_number - 1) / 100),
	       $3::integer + ((unit_number - 1) / 40),
       'paragraph',
       ((unit_number - 1) % 100) + 1,
       (unit_number - 1) % 100,
	       'kitab/' || $1::integer::text || '/h/' || ($2::integer + ((unit_number - 1) / 100))::text || '/u/' || (((unit_number - 1) % 100) + 1)::text,
       'Performance unit ' || unit_number::text,
       'performance unit ' || unit_number::text,
       1,
       decode(md5('b2-performance-content-' || unit_number::text), 'hex'),
       1,
       'ar',
       'source',
       'active',
       '2026-07-10T00:00:00Z'
FROM generate_series(1, $4::integer) AS unit_number`,
		anchorPerformanceBookID,
		anchorPerformanceHeadingStart,
		anchorPerformancePageStart,
		anchorPerformanceUnitCount,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO citable_units (
    id, corpus, book_id, heading_id, page_id, kind, ordinal, position, anchor,
    text, text_normalized, normalization_version, content_hash, occurrence,
    language, provenance_class, lifecycle, retired_at, updated_at
) VALUES
	    ('00000000-0000-0000-0000-00000000b2f1', 'kitab', $1::integer, $2::integer, $3::integer, 'paragraph', 101, 100,
	     'kitab/' || $1::integer::text || '/h/' || $2::integer::text || '/u/101', 'Performance lineage root', 'performance lineage root',
	     1, decode(md5('b2-performance-lineage-root'), 'hex'), 1, 'ar', 'source', 'superseded',
	     '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z'),
	    ('00000000-0000-0000-0000-00000000b2f2', 'kitab', $1::integer, $2::integer, $3::integer, 'paragraph', 102, 101,
	     'kitab/' || $1::integer::text || '/h/' || $2::integer::text || '/u/102', 'Performance lineage middle', 'performance lineage middle',
     1, decode(md5('b2-performance-lineage-middle'), 'hex'), 1, 'ar', 'source', 'superseded',
     '2026-07-10T00:00:00Z', '2026-07-10T00:00:00Z')`,
		anchorPerformanceBookID, anchorPerformanceHeadingStart, anchorPerformancePageStart)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO citable_unit_lineage (predecessor_id, successor_id, reason) VALUES
    ('00000000-0000-0000-0000-00000000b2f1', '00000000-0000-0000-0000-00000000b2f2', 'edit'),
    ('00000000-0000-0000-0000-00000000b2f2', md5('b2-performance-unit-1')::uuid, 'edit'),
    ('00000000-0000-0000-0000-00000000b2f2', md5('b2-performance-unit-2')::uuid, 'edit')`)
	// Historical predecessors make the lineage distribution realistic enough
	// for PostgreSQL to choose and prove the predecessor side of the lineage
	// primary key. Every predecessor has an active successor, so the fixture
	// remains valid under the Citable Unit audit rather than gaming EXPLAIN
	// with dangling or active-outgoing edges.
	execFixtureSQL(t, ctx, tx, `
INSERT INTO citable_units (
    id, corpus, book_id, heading_id, page_id, kind, ordinal, position, anchor,
    text, text_normalized, normalization_version, content_hash, occurrence,
    language, provenance_class, lifecycle, retired_at, updated_at
)
SELECT md5('b2-performance-history-' || heading_offset::text || '-' || historical_ordinal::text)::uuid,
       'kitab',
       $1::integer,
       $2::integer + heading_offset,
       $3::integer + (heading_offset * 5 / 2),
       'paragraph',
       historical_ordinal,
       historical_ordinal - 1,
       'kitab/' || $1::integer::text || '/h/' || ($2::integer + heading_offset)::text || '/u/' || historical_ordinal::text,
       'Historical performance unit ' || heading_offset::text || '-' || historical_ordinal::text,
       'historical performance unit ' || heading_offset::text || '-' || historical_ordinal::text,
       1,
       decode(md5('b2-performance-history-content-' || heading_offset::text || '-' || historical_ordinal::text), 'hex'),
       1,
       'ar',
       'source',
       'superseded',
       '2026-07-10T00:00:00Z',
       '2026-07-10T00:00:00Z'
FROM generate_series(0, 204) AS heading_offset
CROSS JOIN generate_series(103, 202) AS historical_ordinal`,
		anchorPerformanceBookID, anchorPerformanceHeadingStart, anchorPerformancePageStart)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO citable_unit_lineage (predecessor_id, successor_id, reason)
SELECT md5('b2-performance-history-' || heading_offset::text || '-' || historical_ordinal::text)::uuid,
       md5('b2-performance-unit-' || (heading_offset * 100 + 1)::text)::uuid,
       'edit'
FROM generate_series(0, 204) AS heading_offset
CROSS JOIN generate_series(103, 202) AS historical_ordinal`)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit Anchor performance fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE citable_units`); err != nil {
		t.Fatalf("analyze Anchor performance fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE quran_ayahs`); err != nil {
		t.Fatalf("analyze Quran Anchor performance fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE books, book_publications, book_pages, book_headings, citable_unit_lineage`); err != nil {
		t.Fatalf("analyze Anchor performance dimensions: %v", err)
	}
}

//nolint:revive // test helpers conventionally keep testing.T before context
func seedAnchorQuranAyah(t *testing.T, ctx context.Context, tx pgx.Tx) {
	t.Helper()

	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_surahs (surah_id, name_arabic, name_latin, name_translation, revelation_type, ayah_count, metadata)
VALUES (110, 'النصر', 'An-Nasr', 'Pertolongan', 'madaniyah', 3, '{"integration_fixture":"b2-anchor"}'::jsonb)
ON CONFLICT (surah_id) DO NOTHING`)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple, search_text, updated_at
) VALUES (110, 999, $1, 'Anchor fixture ayah', 'Anchor fixture ayah', 'anchor fixture ayah', '2026-07-10T00:00:00Z')`,
		anchorFixtureAyahKey)
}

func cleanupAnchorFixture(t *testing.T) {
	t.Helper()
	cleanupAnchorBooksAndAyah(t, []int{anchorFixtureBookID, anchorHiddenBookID, anchorFallbackBookID})
}

func cleanupAnchorPerformanceFixture(t *testing.T) {
	t.Helper()
	cleanupAnchorBooksAndAyah(t, []int{anchorPerformanceBookID})
}

func cleanupAnchorBooksAndAyah(t *testing.T, bookIDs []int) {
	t.Helper()

	pool := integrationDB(t)

	defer pool.Close()
	// testing cancels t.Context before running Cleanup callbacks, so cleanup
	// needs its own bounded background context or an otherwise passing test
	// would fail while removing its deterministic fixture.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin Anchor cleanup: %v", err)
	}

	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	execFixtureSQL(t, ctx, tx, `DELETE FROM books WHERE id = ANY($1)`, bookIDs)
	execFixtureSQL(t, ctx, tx, `DELETE FROM books WHERE id >= $1 AND id < $1 + $2`, anchorPerformanceShadowStart, anchorPerformanceShadowCount)
	execFixtureSQL(t, ctx, tx, `DELETE FROM categories WHERE id = ANY($1)`, []int{anchorFixtureCategoryID, anchorPerformanceCategoryID})
	execFixtureSQL(t, ctx, tx, `DELETE FROM authors WHERE id = ANY($1)`, []int{anchorFixtureAuthorID, anchorPerformanceAuthorID})
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_ayahs WHERE ayah_key = $1`, anchorFixtureAyahKey)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_ayahs WHERE surah_id = 110 AND ayah_number BETWEEN 10000 AND 14999`)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_surahs WHERE surah_id = 110 AND metadata->>'integration_fixture' = 'b2-anchor'`)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit Anchor cleanup: %v", err)
	}
}

func getAnchorResolution(t *testing.T, query string) entity.AnchorResolution {
	t.Helper()

	target := baseURL() + "/v1/anchors/resolve"
	if query != "" {
		target += "?" + query
	}

	resp := doJSON(t, http.MethodGet, target, nil, "")

	var resolved entity.AnchorResolution
	decodeAndClose(t, resp, &resolved)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("resolve Anchor %q status = %d, want 200", query, resp.StatusCode)
	}

	return resolved
}

func assertAnchorError(t *testing.T, query string, status int, message, code string) {
	t.Helper()

	target := baseURL() + "/v1/anchors/resolve"
	if query != "" {
		target += "?" + query
	}

	resp := doJSON(t, http.MethodGet, target, nil, "")

	var body struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}
	decodeAndClose(t, resp, &body)
	if resp.StatusCode != status || body.Error != message || body.Message != message || body.Code != code || body.RequestID == "" {
		t.Fatalf("Anchor error %q = status %d body %+v, want %d/%q/%q with request_id", query, resp.StatusCode, body, status, message, code)
	}
}

//nolint:gocritic // value-based test helper keeps call sites readable
func assertCanonicalAnchor(t *testing.T, resolved entity.AnchorResolution, want string) {
	t.Helper()
	if resolved.CanonicalAnchor == nil || *resolved.CanonicalAnchor != want {
		t.Fatalf("canonical_anchor = %v, want %q", resolved.CanonicalAnchor, want)
	}
}

//nolint:gocritic // value-based test helper keeps call sites readable
func onlyAnchorBoundary(t *testing.T, resolved entity.AnchorResolution) entity.AnchorBoundary {
	t.Helper()
	if len(resolved.Boundaries) != 1 {
		t.Fatalf("Anchor boundaries = %d, want 1", len(resolved.Boundaries))
	}

	return resolved.Boundaries[0]
}

//nolint:gocritic // value-based test helper keeps call sites readable
func onlyAnchorTarget(t *testing.T, resolved entity.AnchorResolution) entity.AnchorTarget {
	t.Helper()
	boundary := onlyAnchorBoundary(t, resolved)
	if len(boundary.ActiveTargets) != 1 {
		t.Fatalf("Anchor active targets = %d, want 1: %+v", len(boundary.ActiveTargets), boundary.ActiveTargets)
	}

	return boundary.ActiveTargets[0]
}

//nolint:gocritic // value-based test helper keeps call sites readable
func onlyAnchorRedirect(t *testing.T, resolved entity.AnchorResolution) entity.AnchorRedirect {
	t.Helper()
	boundary := onlyAnchorBoundary(t, resolved)
	if len(boundary.RedirectChain) != 1 {
		t.Fatalf("Anchor redirects = %d, want 1: %+v", len(boundary.RedirectChain), boundary.RedirectChain)
	}

	return boundary.RedirectChain[0]
}

func assertAnchorUnitOrder(t *testing.T, targets []entity.AnchorTarget, wantIDs []string) {
	t.Helper()
	if len(targets) != len(wantIDs) {
		t.Fatalf("Anchor target count = %d, want %d", len(targets), len(wantIDs))
	}
	for index, want := range wantIDs {
		if targets[index].UnitID == nil || *targets[index].UnitID != want {
			t.Fatalf("Anchor target %d unit_id = %v, want %q", index, targets[index].UnitID, want)
		}
	}
}

func performTimedAnchorRequest(t *testing.T, client *http.Client, path string) time.Duration {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL()+path, http.NoBody)
	if err != nil {
		t.Fatalf("new timed Anchor request: %v", err)
	}

	started := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("timed Anchor request %s: %v", path, err)
	}

	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	duration := time.Since(started)

	if readErr != nil {
		t.Fatalf("read timed Anchor response %s: %v", path, readErr)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("timed Anchor request %s = %d: %.500s", path, resp.StatusCode, body)
	}

	return duration
}

func nearestRankDuration(sortedDurations []time.Duration, percentile float64) time.Duration {
	rank := max(int(math.Ceil(percentile*float64(len(sortedDurations))))-1, 0)
	if rank >= len(sortedDurations) {
		rank = len(sortedDurations) - 1
	}

	return sortedDurations[rank]
}

func assertAnchorLookupPlans(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tests := []struct {
		name           string
		query          string
		args           []any
		wantIndexes    []string
		wantAnyIndexes []string
		forbidPlan     []string
	}{
		{
			name: "canonical unit with public visibility",
			query: `
SELECT u.id::text, u.anchor, u.book_id, u.heading_id, u.page_id,
       u.lifecycle, u.position, u.updated_at, COALESCE(h.ordinal, -1)
FROM citable_units u
JOIN books b ON b.id = u.book_id AND b.is_deleted = FALSE
JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
LEFT JOIN book_headings h ON h.book_id = u.book_id AND h.heading_id = u.heading_id
WHERE u.corpus = 'kitab' AND u.anchor = $1`,
			args: []any{fmt.Sprintf("kitab/%d/h/%d/u/1", anchorPerformanceBookID, anchorPerformanceHeadingStart)},
			wantIndexes: []string{
				"uq_citable_units_anchor",
				"books_pkey",
				"book_publications_pkey",
			},
			forbidPlan: []string{
				"Seq Scan on citable_units",
				"Seq Scan on books",
				"Seq Scan on book_publications",
			},
		},
		{
			name: "Work with public visibility",
			query: `
SELECT GREATEST(b.updated_at, p.updated_at)
FROM books b
JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
WHERE b.id = $1 AND b.is_deleted = FALSE`,
			args: []any{anchorPerformanceBookID},
			wantIndexes: []string{
				"books_pkey",
				"book_publications_pkey",
			},
			forbidPlan: []string{"Seq Scan on books", "Seq Scan on book_publications"},
		},
		{
			name:        "Quran ayah_key",
			query:       `SELECT ayah_key, updated_at FROM quran_ayahs WHERE ayah_key = $1`,
			args:        []any{anchorFixtureAyahKey},
			wantIndexes: []string{"quran_ayahs_ayah_key_key"},
			forbidPlan:  []string{"Seq Scan on quran_ayahs"},
		},
		{
			name: "heading candidate and active units",
			query: `
WITH candidate AS (
    SELECT h.book_id, h.heading_id, h.page_id, h.is_deleted,
           GREATEST(b.updated_at, p.updated_at, h.updated_at) AS updated_at
    FROM books b
    JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
    JOIN book_headings h ON h.book_id = b.id AND h.heading_id = $2
    WHERE b.id = $1 AND b.is_deleted = FALSE
)
SELECT c.page_id, c.is_deleted, c.updated_at, u.id, u.anchor, u.heading_id, u.page_id, u.updated_at
FROM candidate c
LEFT JOIN citable_units u
  ON NOT c.is_deleted
 AND u.corpus = 'kitab'
 AND u.book_id = c.book_id
 AND u.heading_id = c.heading_id
 AND u.lifecycle = 'active'
ORDER BY u.position NULLS LAST, u.anchor NULLS LAST`,
			args: []any{anchorPerformanceBookID, anchorPerformanceHeadingStart},
			wantIndexes: []string{
				"book_headings_pkey",
				"idx_citable_units_scope_position",
			},
			forbidPlan: []string{
				"Seq Scan on citable_units",
				"Seq Scan on books",
				"Seq Scan on book_publications",
			},
		},
		{
			name: "page candidate and active units",
			query: `
WITH candidate AS (
    SELECT bp.book_id, bp.page_id, bp.is_deleted,
           GREATEST(b.updated_at, p.updated_at, bp.updated_at) AS updated_at
    FROM books b
    JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
    JOIN book_pages bp ON bp.book_id = b.id AND bp.page_id = $2
    WHERE b.id = $1 AND b.is_deleted = FALSE
)
SELECT c.is_deleted, c.updated_at, u.id, u.anchor, u.heading_id, u.page_id, u.updated_at
FROM candidate c
LEFT JOIN citable_units u
  ON NOT c.is_deleted
 AND u.corpus = 'kitab'
 AND u.book_id = c.book_id
 AND u.page_id = c.page_id
 AND u.lifecycle = 'active'
LEFT JOIN book_headings h ON h.book_id = u.book_id AND h.heading_id = u.heading_id
ORDER BY h.ordinal NULLS FIRST, u.position NULLS LAST, u.anchor NULLS LAST`,
			args: []any{anchorPerformanceBookID, anchorPerformancePageStart},
			wantIndexes: []string{
				"idx_citable_units_book_page",
			},
			wantAnyIndexes: []string{"book_pages_pkey", "idx_book_pages_book_page"},
			forbidPlan: []string{
				"Seq Scan on citable_units",
				"Seq Scan on book_pages",
				"Seq Scan on books",
				"Seq Scan on book_publications",
			},
		},
		{
			name: "lineage predecessor traversal",
			query: `
WITH RECURSIVE reachable(id) AS (
	SELECT unnest($1::uuid[])
	UNION
	SELECT lineage.successor_id
	FROM reachable
	JOIN citable_unit_lineage lineage ON lineage.predecessor_id = reachable.id
)
SELECT predecessor.id::text, predecessor.anchor, lineage.reason,
       successor.id::text, successor.anchor, successor.corpus, successor.book_id,
       successor.heading_id, successor.page_id, successor.lifecycle,
       successor.position, successor.updated_at, COALESCE(heading.ordinal, -1),
       visible_book.id IS NOT NULL AND publication.book_id IS NOT NULL
FROM citable_unit_lineage lineage
JOIN citable_units predecessor ON predecessor.id = lineage.predecessor_id
JOIN citable_units successor ON successor.id = lineage.successor_id
LEFT JOIN books visible_book
  ON visible_book.id = successor.book_id AND visible_book.is_deleted = FALSE
LEFT JOIN book_publications publication
  ON publication.book_id = visible_book.id AND publication.status = 'published'
LEFT JOIN book_headings heading
  ON heading.book_id = successor.book_id AND heading.heading_id = successor.heading_id
WHERE lineage.predecessor_id = ANY(ARRAY(SELECT id FROM reachable))
ORDER BY predecessor.anchor, successor.anchor, lineage.reason`,
			args:        []any{[]string{"00000000-0000-0000-0000-00000000b2f1"}},
			wantIndexes: []string{"citable_unit_lineage_pkey"},
			forbidPlan:  []string{"Seq Scan on citable_unit_lineage"},
		},
	}

	for testIndex := range tests {
		test := &tests[testIndex]
		rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+test.query, test.args...)
		if err != nil {
			t.Fatalf("EXPLAIN %s: %v", test.name, err)
		}

		var lines []string

		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatalf("scan EXPLAIN %s: %v", test.name, err)
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("iterate EXPLAIN %s: %v", test.name, err)
		}
		rows.Close()
		plan := strings.Join(lines, "\n")
		t.Logf("Anchor lookup plan (%s):\n%s", test.name, plan)

		for _, index := range test.wantIndexes {
			if !strings.Contains(plan, index) {
				t.Fatalf("Anchor lookup plan %s did not use %s:\n%s", test.name, index, plan)
			}
		}
		if len(test.wantAnyIndexes) > 0 {
			found := false
			for _, index := range test.wantAnyIndexes {
				found = found || strings.Contains(plan, index)
			}
			if !found {
				t.Fatalf("Anchor lookup plan %s did not use any of %v:\n%s", test.name, test.wantAnyIndexes, plan)
			}
		}

		for _, forbidden := range test.forbidPlan {
			if strings.Contains(plan, forbidden) {
				t.Fatalf("Anchor lookup plan %s contains forbidden operation %q:\n%s", test.name, forbidden, plan)
			}
		}
	}
}
