package persistent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	anchorgrammar "github.com/alfariesh/surau-backend/internal/anchor"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/alfariesh/surau-backend/internal/usecase/crossreference"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveCrossReferenceRegistry is the database-level B-3 contract: two-way
// approved visibility, Quran containment/distinct-Work counts, unit lineage,
// optimistic review, guarded triple-write/rollback, and the legacy freeze.
//
//nolint:maintidx,paralleltest,wsl_v5 // one serial end-to-end invariant fixture with explicit transaction stages
func TestLiveCrossReferenceRegistry(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	fixture := seedLiveAnchorFixture(t, pg)
	registryRepo := NewCrossReferenceRepo(pg)
	uc := crossreference.New(registryRepo)
	actorID := uuid.NewString()
	username := "crossref-live-" + actorID
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO users (id, username, email, password_hash)
VALUES ($1, $2::text, $2::text || '@example.test', 'hash')`, actorID, username)
	require.NoError(t, err)

	ayahParts := strings.Split(fixture.ayahKey, ":")
	require.Len(t, ayahParts, 2)
	ayahNumber, err := strconv.Atoi(ayahParts[1])
	require.NoError(t, err)
	outsideAyah := ayahNumber + 2
	for _, number := range []int{ayahNumber + 1, outsideAyah} {
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key)
VALUES (114, $1, $2)`, number, fmt.Sprintf("114:%d", number))
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		tx, cleanupErr := pg.Pool.Begin(cleanupCtx)
		if !assert.NoError(t, cleanupErr) {
			return
		}
		defer func() { _ = tx.Rollback(cleanupCtx) }()
		_, cleanupErr = tx.Exec(cleanupCtx, crossReferenceWriterGUC)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = tx.Exec(cleanupCtx, `
UPDATE cross_reference_registry_state
SET quran_legacy_frozen = FALSE, updated_at = now()
WHERE id = TRUE`)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = tx.Exec(cleanupCtx, `
DELETE FROM cross_references
WHERE source_work_id = ANY($1::int[]) OR target_work_id = ANY($1::int[])`,
			[]int{fixture.bookID, fixture.hiddenBookID, fixture.publicOtherBookID})
		assert.NoError(t, cleanupErr)
		_, cleanupErr = tx.Exec(cleanupCtx, `
DELETE FROM quran_book_references WHERE book_id = ANY($1::int[])`,
			[]int{fixture.bookID, fixture.hiddenBookID, fixture.publicOtherBookID})
		assert.NoError(t, cleanupErr)
		_, cleanupErr = tx.Exec(cleanupCtx, `
DELETE FROM quran_ayahs WHERE surah_id = 114 AND ayah_number = ANY($1::int[])`,
			[]int{ayahNumber + 1, outsideAyah})
		assert.NoError(t, cleanupErr)
		_, cleanupErr = tx.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, actorID)
		assert.NoError(t, cleanupErr)
		assert.NoError(t, tx.Commit(cleanupCtx))
	})

	work := fmt.Sprintf("kitab/%d", fixture.bookID)
	otherWork := fmt.Sprintf("kitab/%d", fixture.publicOtherBookID)
	hiddenWork := fmt.Sprintf("kitab/%d", fixture.hiddenBookID)
	heading := fmt.Sprintf("kitab/%d/h/11", fixture.bookID)
	ayah := "quran/" + fixture.ayahKey
	rangeAnchor := fmt.Sprintf("%s..quran/114:%d", ayah, ayahNumber+1)
	surah := "quran/114"

	t.Run("non Quran edge is pending then visible in both directions", func(t *testing.T) {
		created, err := uc.CreateHuman(ctx, entity.CrossReferenceCreateInput{
			SourceAnchor: work,
			TargetAnchor: otherWork,
			Kind:         entity.CrossReferenceKindQuotes,
			Confidence:   0.8,
			EvidenceText: "نص مقتبس",
		}, actorID)
		require.NoError(t, err)

		outgoing, err := uc.ListPublic(ctx, work, entity.CrossReferenceDirectionOutgoing,
			entity.CrossReferenceKindQuotes, 50, 0)
		require.NoError(t, err)
		assert.Empty(t, outgoing.Items)

		approved, err := uc.Review(ctx, created.ID, entity.CrossReferenceStatusApproved, actorID, &created.UpdatedAt)
		require.NoError(t, err)
		require.NotNil(t, approved.ReviewedBy)
		stored, err := uc.Get(ctx, created.ID)
		require.NoError(t, err)
		assert.Equal(t, approved.ID, stored.ID)
		assert.Equal(t, entity.CrossReferenceStatusApproved, stored.ReviewStatus)

		outgoing, err = uc.ListPublic(ctx, work, entity.CrossReferenceDirectionOutgoing,
			entity.CrossReferenceKindQuotes, 50, 0)
		require.NoError(t, err)
		require.Len(t, outgoing.Items, 1)
		assert.Equal(t, 1, outgoing.Total)
		assert.Equal(t, 1, outgoing.WorkTotal)

		incoming, err := uc.ListPublic(ctx, otherWork, entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindQuotes, 50, 0)
		require.NoError(t, err)
		require.Len(t, incoming.Items, 1)
		assert.Equal(t, created.ID, incoming.Items[0].ID)

		_, err = uc.Review(ctx, created.ID, entity.CrossReferenceStatusRejected, actorID, &created.UpdatedAt)
		require.ErrorIs(t, err, entity.ErrPreconditionFailed)

		reopened, err := uc.Review(ctx, created.ID, entity.CrossReferenceStatusPending, actorID, nil)
		require.NoError(t, err)
		assert.Nil(t, reopened.ReviewedBy)
		assert.Nil(t, reopened.ReviewedAt)
		_, err = uc.Review(ctx, created.ID, entity.CrossReferenceStatusApproved, actorID, &reopened.UpdatedAt)
		require.NoError(t, err)
	})

	t.Run("concurrent review accepts exactly one decision", func(t *testing.T) {
		created, err := uc.CreateHuman(ctx, entity.CrossReferenceCreateInput{
			SourceAnchor: work,
			TargetAnchor: otherWork,
			Kind:         entity.CrossReferenceKindExplains,
			Confidence:   1,
			EvidenceText: "قرار متزامن",
		}, actorID)
		require.NoError(t, err)

		errs := make(chan error, 2)
		for _, status := range []string{
			entity.CrossReferenceStatusApproved,
			entity.CrossReferenceStatusRejected,
		} {
			go func(reviewStatus string) {
				_, reviewErr := uc.Review(ctx, created.ID, reviewStatus, actorID, &created.UpdatedAt)
				errs <- reviewErr
			}(status)
		}

		successes, stale := 0, 0
		for range 2 {
			reviewErr := <-errs
			switch {
			case reviewErr == nil:
				successes++
			case errors.Is(reviewErr, entity.ErrPreconditionFailed):
				stale++
			default:
				require.NoError(t, reviewErr)
			}
		}
		assert.Equal(t, 1, successes)
		assert.Equal(t, 1, stale)
	})

	t.Run("derived origin retry returns the canonical row", func(t *testing.T) {
		confidence := 0.9
		originKey := "live-derived-" + uuid.NewString()
		ref := entity.CrossReference{
			ID:                   uuid.NewString(),
			SourceAnchor:         work,
			TargetAnchor:         otherWork,
			Kind:                 entity.CrossReferenceKindExplains,
			Method:               entity.CrossReferenceMethodResolver,
			MethodDetail:         entity.CrossReferenceMethodDetail{Strategy: "live_test"},
			Confidence:           &confidence,
			ReviewStatus:         entity.CrossReferenceStatusNeedsReview,
			EvidenceText:         "دليل ثابت",
			EvidenceNormalized:   searchtext.Normalize("دليل ثابت"),
			NormalizationVersion: searchtext.ProfileVersion,
			Origin:               entity.CrossReferenceOriginResolver,
			OriginKey:            originKey,
		}

		first, err := uc.UpsertDerived(ctx, ref)
		require.NoError(t, err)

		ref.ID = uuid.NewString()
		second, err := uc.UpsertDerived(ctx, ref)
		require.NoError(t, err)
		assert.Equal(t, first.ID, second.ID)
		assert.Equal(t, entity.CrossReferenceStatusNeedsReview, second.ReviewStatus)
	})

	t.Run("Quran point and range count distinct visible Works", func(t *testing.T) {
		createApprovedHuman(ctx, t, uc, actorID, work, ayah, entity.CrossReferenceKindCites, "ذكر صريح")
		createApprovedHuman(ctx, t, uc, actorID, heading, rangeAnchor, entity.CrossReferenceKindCites, "مدى الآيات")
		createApprovedHuman(ctx, t, uc, actorID, otherWork, ayah, entity.CrossReferenceKindCites, "ذكر آخر")
		createApprovedHuman(ctx, t, uc, actorID, hiddenWork, ayah, entity.CrossReferenceKindCites, "كتاب مخفي")
		createApprovedHuman(ctx, t, uc, actorID, otherWork, surah, entity.CrossReferenceKindCites, "السورة")

		got, err := uc.ListPublic(ctx, ayah, entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindCites, 50, 0)
		require.NoError(t, err)
		assert.Len(t, got.Items, 3, "hidden Work and surah-only edge must not match the ayah")
		assert.Equal(t, 3, got.Total)
		assert.Equal(t, 2, got.WorkTotal)

		emptyPage, err := uc.ListPublic(ctx, ayah, entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindCites, 1, 100)
		require.NoError(t, err)
		assert.Empty(t, emptyPage.Items)
		assert.NotNil(t, emptyPage.Items)
		assert.Equal(t, 3, emptyPage.Total)
		assert.Equal(t, 2, emptyPage.WorkTotal)

		outside, err := uc.ListPublic(ctx, fmt.Sprintf("quran/114:%d", outsideAyah),
			entity.CrossReferenceDirectionIncoming, entity.CrossReferenceKindCites, 50, 0)
		require.NoError(t, err)
		assert.Empty(t, outside.Items)
	})

	t.Run("unit successor and old Anchor queries preserve edges without sibling bleed", func(t *testing.T) {
		old := createApprovedHuman(ctx, t, uc, actorID, otherWork, fixture.root.anchor,
			entity.CrossReferenceKindParallel, "مرجع قديم")
		createApprovedHuman(ctx, t, uc, actorID, otherWork, fixture.splitA.anchor,
			entity.CrossReferenceKindParallel, "مرجع فرع")

		fromA, err := uc.ListPublic(ctx, fixture.splitA.anchor, entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindParallel, 50, 0)
		require.NoError(t, err)
		assert.Len(t, fromA.Items, 2)

		fromB, err := uc.ListPublic(ctx, fixture.splitB.anchor, entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindParallel, 50, 0)
		require.NoError(t, err)
		require.Len(t, fromB.Items, 1)
		assert.Equal(t, old.ID, fromB.Items[0].ID)

		_, err = uc.CreateHuman(ctx, entity.CrossReferenceCreateInput{
			SourceAnchor: otherWork,
			TargetAnchor: fixture.tombstone.anchor,
			Kind:         entity.CrossReferenceKindParallel,
			Confidence:   1,
			EvidenceText: "gone",
		}, actorID)
		require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
	})

	t.Run("knowledge mention race adopts the canonical legacy UUID and review", func(t *testing.T) {
		runID, mentionID := uuid.NewString(), uuid.NewString()
		canonicalID := uuid.NewString()
		_, err := pg.Pool.Exec(ctx, `
INSERT INTO generation_runs (id, task_name, prompt_version, model_id, provider, metadata)
VALUES ($1, 'mentions', 'live-v1', 'live-model', 'openai', '{"source":"cross-reference-live-test"}')`, runID)
		require.NoError(t, err)
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO knowledge_extraction_runs (id, task_name, prompt_version, model_id)
VALUES ($1, 'mentions', 'live-v1', 'live-model')`, runID)
		require.NoError(t, err)
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO knowledge_mentions (
    id, run_id, book_id, page_id, document_id, extraction_class,
    extraction_text, exact_quote, char_start, char_end, alignment_status,
    normalized_text, normalization_version, review_status
) VALUES ($1, $2, $3, 1, 'live-race', 'citation', 'سورة', 'سورة', 0, 4,
          'aligned', $4, $5, 'needs_review')`,
			mentionID, runID, fixture.bookID, searchtext.Normalize("سورة"), searchtext.ProfileVersion)
		require.NoError(t, err)
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO quran_book_references (
    id, book_id, page_id, knowledge_mention_id, source_text, normalized_text,
    normalization_version, reference_kind, surah_id, match_strategy, confidence, review_status, metadata
) VALUES ($1, $2, 1, $3, 'سورة', $4, $5, 'surah', 114, 'explicit_surah', 0.8,
          'needs_review', '{"winner":"legacy"}')`,
			canonicalID, fixture.bookID, mentionID, searchtext.Normalize("سورة"), searchtext.ProfileVersion)
		require.NoError(t, err)

		ref, bridge := liveLegacyBridge(fixture.bookID, 1, ayahNumber, "surah", entity.CrossReferenceStatusApproved)
		candidateID := ref.ID
		bridge.KnowledgeMentionID = &mentionID
		ref.Origin = entity.CrossReferenceOriginResolver
		ref.OriginKey = mentionID
		got, err := uc.BridgeLegacy(ctx, ref, bridge)
		require.NoError(t, err)
		assert.Equal(t, canonicalID, got.ID)
		assert.Equal(t, entity.CrossReferenceStatusNeedsReview, got.ReviewStatus,
			"resolver race must preserve the winning legacy review")

		var canonicalBridge, candidateRows int
		require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT
  (SELECT COUNT(*) FROM quran_cross_reference_bridge WHERE cross_reference_id = $1),
  (SELECT COUNT(*) FROM cross_references WHERE id = $2)
`, canonicalID, candidateID).Scan(&canonicalBridge, &candidateRows))
		assert.Equal(t, 1, canonicalBridge)
		assert.Zero(t, candidateRows)

		cleanupTx, err := pg.Pool.Begin(ctx)
		require.NoError(t, err)
		defer func() { _ = cleanupTx.Rollback(ctx) }()
		_, err = cleanupTx.Exec(ctx, crossReferenceWriterGUC)
		require.NoError(t, err)
		_, err = cleanupTx.Exec(ctx, `DELETE FROM cross_references WHERE id = $1`, canonicalID)
		require.NoError(t, err)
		_, err = cleanupTx.Exec(ctx, `DELETE FROM quran_book_references WHERE id = $1`, canonicalID)
		require.NoError(t, err)
		_, err = cleanupTx.Exec(ctx, `DELETE FROM knowledge_extraction_runs WHERE id = $1`, runID)
		require.NoError(t, err)
		require.NoError(t, cleanupTx.Commit(ctx))
	})

	t.Run("ambiguous legacy cannot be approved in place", func(t *testing.T) {
		ref, bridge := liveLegacyBridge(fixture.bookID, 1, ayahNumber, "ambiguous", entity.CrossReferenceStatusAmbiguous)
		_, err := uc.BridgeLegacy(ctx, ref, bridge)
		require.NoError(t, err)
		_, err = uc.Review(ctx, ref.ID, entity.CrossReferenceStatusApproved, actorID, nil)
		require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
	})

	t.Run("triple write rolls back on bridge failure", func(t *testing.T) {
		ref, bridge := liveLegacyBridge(fixture.bookID, 9_999_999, ayahNumber, "surah", entity.CrossReferenceStatusApproved)
		_, err := uc.BridgeLegacy(ctx, ref, bridge)
		require.ErrorIs(t, err, entity.ErrInvalidCrossReference)

		var qbr, registry, bridgeCount int
		require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT
  (SELECT COUNT(*) FROM quran_book_references WHERE id = $1),
  (SELECT COUNT(*) FROM cross_references WHERE id = $1),
  (SELECT COUNT(*) FROM quran_cross_reference_bridge WHERE cross_reference_id = $1)`, ref.ID).
			Scan(&qbr, &registry, &bridgeCount))
		assert.Zero(t, qbr)
		assert.Zero(t, registry)
		assert.Zero(t, bridgeCount)
	})

	t.Run("freeze blocks direct legacy DML but service remains writable", func(t *testing.T) {
		require.NoError(t, uc.FreezeLegacyQuranWrites(ctx))

		_, err := pg.Pool.Exec(ctx, `
INSERT INTO quran_book_references (
    id, book_id, page_id, source_text, normalized_text, reference_kind,
    surah_id, match_strategy, review_status
) VALUES ($1, $2, 1, 'direct', 'direct', 'surah', 114, 'direct', 'pending')`,
			uuid.NewString(), fixture.bookID)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, "42501", pgErr.Code)

		ref, bridge := liveLegacyBridge(fixture.bookID, 1, ayahNumber, "surah", entity.CrossReferenceStatusApproved)
		ref.Origin = entity.CrossReferenceOriginResolver
		ref.OriginKey = "live-resolver-" + ref.ID
		got, err := uc.BridgeLegacy(ctx, ref, bridge)
		require.NoError(t, err)
		assert.Equal(t, ref.ID, got.ID)

		require.NoError(t, uc.UnfreezeLegacyQuranWrites(ctx))
		var frozen bool
		require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT quran_legacy_frozen FROM cross_reference_registry_state WHERE id = TRUE`).Scan(&frozen))
		assert.False(t, frozen)
	})

	t.Run("direct registry DML is always guarded", func(t *testing.T) {
		one, err := uc.ListEditorial(ctx, repoFilterForLive(work))
		require.NoError(t, err)
		require.NotEmpty(t, one.Items)

		_, err = pg.Pool.Exec(ctx, `DELETE FROM cross_references WHERE id = $1`, one.Items[0].ID)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, "42501", pgErr.Code)
	})

	t.Run("actor evidence prevents hard deletion", func(t *testing.T) {
		_, err := pg.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, actorID)
		var pgErr *pgconn.PgError
		require.ErrorAs(t, err, &pgErr)
		assert.Equal(t, "23001", pgErr.Code)
	})
}

// TestLiveCrossReferenceQueryPerformance proves the B-3 public query budget on
// 40k edges: 20k mixed plus 20k repeated-heading edges, with 50 warm-ups then
// 500 reads in each workload.
//
//nolint:gosec,paralleltest,wsl_v5 // loop indices are nonnegative bounded offsets; fixture is serial and self-cleaning
func TestLiveCrossReferenceQueryPerformance(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url, postgres.MaxPoolSize(4))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	fixture := seedLiveAnchorFixture(t, pg)
	registryRepo := NewCrossReferenceRepo(pg)
	uc := crossreference.New(registryRepo)
	anchor := "quran/" + fixture.ayahKey
	rangeAnchor := anchor + ".." + anchor
	ayahNumber := mustAyahNumber(t, fixture.ayahKey)
	now := time.Now().UTC()

	rows := make([][]any, 0, 40_000)
	for index := range 20_000 {
		bookID := fixture.bookID
		if index%2 == 1 {
			bookID = fixture.publicOtherBookID
		}
		target := anchor
		if index%3 == 0 {
			target = rangeAnchor
		}
		rows = append(rows, []any{
			uuid.NewString(), fmt.Sprintf("kitab/%d", bookID), target, "kitab", "quran",
			bookID, nil, 114, ayahNumber, ayahNumber,
			entity.CrossReferenceKindCites, entity.CrossReferenceMethodResolver,
			[]byte(`{"strategy":"performance_fixture"}`), 0.9, entity.CrossReferenceStatusApproved,
			"perf", "perf", searchtext.ProfileVersion, "performance_fixture",
			fmt.Sprintf("%d-%d", fixture.bookID, index), nil, nil, nil, []byte(`{}`), now, now,
		})
	}
	headingAnchor := fmt.Sprintf("kitab/%d/h/11", fixture.bookID)
	for index := range 20_000 {
		bookID := fixture.bookID
		if index%2 == 1 {
			bookID = fixture.publicOtherBookID
		}
		rows = append(rows, []any{
			uuid.NewString(), fmt.Sprintf("kitab/%d", bookID), headingAnchor, "kitab", "kitab",
			bookID, fixture.bookID, nil, nil, nil,
			entity.CrossReferenceKindExplains, entity.CrossReferenceMethodResolver,
			[]byte(`{"strategy":"performance_heading_fixture"}`), 0.9, entity.CrossReferenceStatusApproved,
			"perf-heading", "perf-heading", searchtext.ProfileVersion, "performance_fixture",
			fmt.Sprintf("%d-heading-%d", fixture.bookID, index), nil, nil, nil, []byte(`{}`), now, now,
		})
	}

	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, crossReferenceWriterGUC)
	require.NoError(t, err)
	inserted, err := tx.CopyFrom(ctx, pgx.Identifier{"cross_references"}, []string{
		"id", "source_anchor", "target_anchor", "source_corpus", "target_corpus",
		"source_work_id", "target_work_id", "target_quran_surah_id",
		"target_quran_from_ayah", "target_quran_to_ayah", "kind", "method", "method_detail",
		"confidence", "review_status", "evidence_text", "evidence_normalized",
		"normalization_version", "origin", "origin_key", "created_by", "reviewed_by",
		"reviewed_at", "metadata", "created_at", "updated_at",
	}, pgx.CopyFromRows(rows))
	require.NoError(t, err)
	require.EqualValues(t, 40_000, inserted)
	require.NoError(t, tx.Commit(ctx))

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cleanupTx, cleanupErr := pg.Pool.Begin(cleanupCtx)
		if !assert.NoError(t, cleanupErr) {
			return
		}
		defer func() { _ = cleanupTx.Rollback(cleanupCtx) }()
		_, cleanupErr = cleanupTx.Exec(cleanupCtx, crossReferenceWriterGUC)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = cleanupTx.Exec(cleanupCtx,
			`DELETE FROM cross_references WHERE origin = 'performance_fixture' AND origin_key LIKE $1`,
			fmt.Sprintf("%d-%%", fixture.bookID))
		assert.NoError(t, cleanupErr)
		assert.NoError(t, cleanupTx.Commit(cleanupCtx))
	})

	_, err = pg.Pool.Exec(ctx, `ANALYZE cross_references`)
	require.NoError(t, err)
	sourcePlan := explainText(ctx, t, pg, `
SELECT id FROM cross_references
WHERE source_anchor = $1 AND review_status = 'approved' AND kind = 'cites'
ORDER BY created_at, id LIMIT 50`, fmt.Sprintf("kitab/%d", fixture.bookID))
	assert.Contains(t, sourcePlan, "idx_cross_references_source_lookup")
	targetPlan := explainText(ctx, t, pg, `
SELECT id FROM cross_references
WHERE target_anchor = $1 AND review_status = 'approved' AND kind = 'cites'
ORDER BY created_at, id LIMIT 50`, anchor)
	assert.Contains(t, targetPlan, "idx_cross_references_target_lookup")
	containmentPlan := explainText(ctx, t, pg, `
SELECT id FROM cross_references
WHERE target_quran_surah_id = 114
  AND target_quran_from_ayah IS NOT NULL
  AND target_quran_to_ayah IS NOT NULL
  AND int4range(target_quran_from_ayah, target_quran_to_ayah, '[]') @> $1::integer`,
		ayahNumber+10_000)
	assert.Contains(t, containmentPlan, "idx_cross_references_target_quran_containment")
	headingValue, err := anchorgrammar.Parse(headingAnchor)
	require.NoError(t, err)
	headingPlanBuilder := pg.Builder.Select("COUNT(*)", "COUNT(DISTINCT cr.source_work_id)").
		From("cross_references cr")
	headingPlanBuilder = applyCrossReferenceListFilter(headingPlanBuilder, repo.CrossReferenceFilter{
		Anchor:       headingAnchor,
		Direction:    entity.CrossReferenceDirectionIncoming,
		Kind:         entity.CrossReferenceKindExplains,
		ReviewStatus: entity.CrossReferenceStatusApproved,
		PublicOnly:   true,
	}, &headingValue)
	headingSQL, headingArgs, err := headingPlanBuilder.ToSql()
	require.NoError(t, err)
	headingPlan := explainText(ctx, t, pg, headingSQL, headingArgs...)
	assert.Contains(t, headingPlan, "Memoize", "repeated endpoint visibility must execute once per distinct Anchor")

	query := func(index int) {
		var got entity.CrossReferenceList
		var queryErr error
		if index%2 == 0 {
			got, queryErr = uc.ListPublic(ctx, anchor, entity.CrossReferenceDirectionIncoming,
				entity.CrossReferenceKindCites, 50, uint64(index%10_001))
			require.NoError(t, queryErr)
			assert.Equal(t, 20_000, got.Total)
			assert.Equal(t, 2, got.WorkTotal)
		} else {
			got, queryErr = uc.ListPublic(ctx, fmt.Sprintf("kitab/%d", fixture.bookID),
				entity.CrossReferenceDirectionOutgoing, entity.CrossReferenceKindCites,
				50, uint64(index%10_001))
			require.NoError(t, queryErr)
			assert.Equal(t, 10_000, got.Total)
			assert.Equal(t, 1, got.WorkTotal, "Quran is one implicit opposite Work")
		}
	}

	for index := range 50 {
		query(index)
	}

	durations := make([]time.Duration, 0, 500)
	for index := range 500 {
		started := time.Now()
		query(index)
		durations = append(durations, time.Since(started))
	}
	slices.Sort(durations)
	p95 := durations[(len(durations)*95+99)/100-1]
	t.Logf("Cross-Reference 20k mixed reads: p50=%s p95=%s max=%s",
		durations[len(durations)/2], p95, durations[len(durations)-1])
	assert.Less(t, p95, 200*time.Millisecond)

	for index := range 20 {
		_, err := uc.ListPublic(ctx, headingAnchor, entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindExplains, 50, uint64(index))
		require.NoError(t, err)
	}
	headingDurations := make([]time.Duration, 0, 100)
	for index := range 100 {
		started := time.Now()
		got, err := uc.ListPublic(ctx, headingAnchor, entity.CrossReferenceDirectionIncoming,
			entity.CrossReferenceKindExplains, 50, uint64(index))
		require.NoError(t, err)
		assert.Equal(t, 20_000, got.Total)
		assert.Equal(t, 2, got.WorkTotal)
		headingDurations = append(headingDurations, time.Since(started))
	}
	slices.Sort(headingDurations)
	headingP95 := headingDurations[(len(headingDurations)*95+99)/100-1]
	t.Logf("Cross-Reference repeated-heading reads: p50=%s p95=%s max=%s",
		headingDurations[len(headingDurations)/2], headingP95, headingDurations[len(headingDurations)-1])
	assert.Less(t, headingP95, 200*time.Millisecond)
}

func createApprovedHuman(
	ctx context.Context,
	t *testing.T,
	uc *crossreference.UseCase,
	actorID, source, target, kind, evidence string,
) entity.CrossReference {
	t.Helper()

	created, err := uc.CreateHuman(ctx, entity.CrossReferenceCreateInput{
		SourceAnchor: source,
		TargetAnchor: target,
		Kind:         kind,
		Confidence:   0.9,
		EvidenceText: evidence,
	}, actorID)
	require.NoError(t, err)

	approved, err := uc.Review(ctx, created.ID, entity.CrossReferenceStatusApproved, actorID, &created.UpdatedAt)
	require.NoError(t, err)

	return approved
}

//nolint:wsl_v5 // pointer-shaped legacy fields are grouped before the kind-specific branch
func liveLegacyBridge(
	bookID, pageID, ayahNumber int,
	legacyKind, status string,
) (entity.CrossReference, entity.QuranCrossReferenceBridge) {
	id := uuid.NewString()
	surahID := 114
	confidence := 0.8
	evidence := "سورة"
	strategy := "explicit_surah"
	target := "quran/114"
	kind := entity.CrossReferenceKindCites
	var from, to *int
	var fromKey, toKey *string
	if legacyKind != "surah" {
		from = new(ayahNumber)
		to = new(ayahNumber)
		key := fmt.Sprintf("114:%d", ayahNumber)
		fromKey, toKey = new(key), new(key)
		target = "quran/" + key
		strategy = "explicit_surah_ayah"
	}
	if legacyKind == "quote" {
		kind = entity.CrossReferenceKindQuotes
	}

	ref := entity.CrossReference{
		ID: id, SourceAnchor: fmt.Sprintf("kitab/%d", bookID), TargetAnchor: target,
		Kind: kind, Method: entity.CrossReferenceMethodResolver,
		MethodDetail: entity.CrossReferenceMethodDetail{Strategy: strategy},
		Confidence:   &confidence, ReviewStatus: status,
		EvidenceText: evidence, EvidenceNormalized: searchtext.Normalize(evidence),
		NormalizationVersion: searchtext.ProfileVersion,
		Origin:               entity.CrossReferenceOriginLegacyQuran, OriginKey: id,
	}
	bridge := entity.QuranCrossReferenceBridge{
		ID: id, BookID: bookID, PageID: pageID,
		SourceText: evidence, NormalizedText: searchtext.Normalize(evidence),
		ReferenceKind: legacyKind, SurahID: &surahID,
		FromAyahNumber: from, ToAyahNumber: to, FromAyahKey: fromKey, ToAyahKey: toKey,
		MatchStrategy: strategy, Metadata: entity.RawJSON(`{}`),
	}

	return ref, bridge
}

func repoFilterForLive(anchor string) repo.CrossReferenceFilter {
	return repo.CrossReferenceFilter{
		Anchor: anchor, Direction: entity.CrossReferenceDirectionOutgoing, Limit: 1,
	}
}

func mustAyahNumber(t *testing.T, ayahKey string) int {
	t.Helper()

	parts := strings.Split(ayahKey, ":")
	require.Len(t, parts, 2)
	number, err := strconv.Atoi(parts[1])
	require.NoError(t, err)

	return number
}

//nolint:wsl_v5 // EXPLAIN row collection is kept compact for assertion readability
func explainText(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	query string,
	args ...any,
) string {
	t.Helper()
	rows, err := pg.Pool.Query(ctx, "EXPLAIN (COSTS OFF) "+query, args...)
	require.NoError(t, err)
	defer rows.Close()

	lines := make([]string, 0)
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		lines = append(lines, line)
	}
	require.NoError(t, rows.Err())

	return strings.Join(lines, "\n")
}
