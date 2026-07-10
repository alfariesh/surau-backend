package persistent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveAnchorRepoResolution is the database-level B-2 contract: indexed
// canonical/legacy reads, public visibility, coarse fallbacks, and the full
// edit-lineage graph including split, merge, tombstone, and cycle handling.
//
//nolint:paralleltest // one serial, self-cleaning graph fixture against the shared live Postgres
func TestLiveAnchorRepoResolution(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	fixture := seedLiveAnchorFixture(t, pg)
	repo := NewAnchorRepo(pg)
	unitRepo := NewCitableUnitRepo(pg)

	t.Run("quran canonical and legacy indexed row", func(t *testing.T) {
		got, err := repo.ResolveQuran(ctx, fixture.ayahKey)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleActive, got.Status)
		require.Len(t, got.ActiveRecords, 1)
		assert.Equal(t, "quran/"+fixture.ayahKey, *got.CanonicalAnchor)
		assert.Equal(t, fixture.ayahKey, *got.ActiveRecords[0].AyahKey)
	})

	t.Run("work visibility", func(t *testing.T) {
		got, err := repo.ResolveWork(ctx, fixture.bookID)
		require.NoError(t, err)
		require.Len(t, got.ActiveRecords, 1)
		assert.Equal(t, entity.AnchorTargetBook, got.ActiveRecords[0].TargetType)

		_, err = repo.ResolveWork(ctx, fixture.hiddenBookID)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
	})

	t.Run("active unit", func(t *testing.T) {
		got, err := repo.ResolveCanonicalUnit(ctx, fixture.direct.anchor)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleActive, got.Status)
		require.Len(t, got.ActiveRecords, 1)
		assert.Equal(t, fixture.direct.id, *got.ActiveRecords[0].UnitID)
		assert.Empty(t, got.RedirectChain)
	})

	t.Run("multi hop split is complete and ordered", func(t *testing.T) {
		got, err := repo.ResolveCanonicalUnit(ctx, fixture.root.anchor)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleSuperseded, got.Status)
		require.Len(t, got.RedirectChain, 3)
		assert.Equal(t, 1, got.RedirectChain[0].Depth)
		assert.Equal(t, fixture.root.anchor, got.RedirectChain[0].From)
		assert.Equal(t, fixture.mid.anchor, got.RedirectChain[0].To)
		assert.Equal(t, 2, got.RedirectChain[1].Depth)
		assert.Equal(t, 2, got.RedirectChain[2].Depth)
		require.Len(t, got.ActiveRecords, 2)
		assert.Equal(t, fixture.splitB.id, *got.ActiveRecords[0].UnitID, "position orders the split targets")
		assert.Equal(t, fixture.splitA.id, *got.ActiveRecords[1].UnitID)
	})

	t.Run("merge endpoint is deduplicated", func(t *testing.T) {
		got, err := repo.ResolveCanonicalUnit(ctx, fixture.mergeRoot.anchor)
		require.NoError(t, err)
		require.Len(t, got.ActiveRecords, 1)
		assert.Equal(t, fixture.splitA.id, *got.ActiveRecords[0].UnitID)
		require.Len(t, got.RedirectChain, 1)
	})

	t.Run("valid lineage longer than former cap reaches its endpoint", func(t *testing.T) {
		got, err := repo.ResolveCanonicalUnit(ctx, fixture.longRoot.anchor)
		require.NoError(t, err)
		assert.False(t, got.CycleDetected)
		require.Len(t, got.RedirectChain, 35)
		require.Len(t, got.ActiveRecords, 1)
		assert.Equal(t, fixture.longTarget.id, *got.ActiveRecords[0].UnitID)
		assert.Equal(t, 35, got.RedirectChain[len(got.RedirectChain)-1].Depth)
	})

	t.Run("repeated diamond scales by unique edges not exponential paths", func(t *testing.T) {
		stressCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		got, err := repo.ResolveCanonicalUnit(stressCtx, fixture.diamondRoot.anchor)
		require.NoError(t, err)
		assert.False(t, got.CycleDetected)
		require.Len(t, got.RedirectChain, len(fixture.diamondEdges))
		require.Len(t, got.ActiveRecords, 1)
		assert.Equal(t, fixture.diamondEnd.id, *got.ActiveRecords[0].UnitID)
	})

	t.Run("targets use heading ordinal before local position", func(t *testing.T) {
		got, err := repo.ResolveCanonicalUnit(ctx, fixture.orderRoot.anchor)
		require.NoError(t, err)
		require.Len(t, got.ActiveRecords, 2)
		assert.Equal(t, fixture.orderFirst.id, *got.ActiveRecords[0].UnitID,
			"earlier heading wins even when its local position is larger")
		assert.Equal(t, fixture.orderSecond.id, *got.ActiveRecords[1].UnitID)
	})

	t.Run("known tombstone does not become not found", func(t *testing.T) {
		got, err := repo.ResolveCanonicalUnit(ctx, fixture.tombstone.anchor)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleTombstoned, got.Status)
		assert.Empty(t, got.ActiveRecords)
		assert.Empty(t, got.RedirectChain)
	})

	t.Run("active heading and page return deterministic units", func(t *testing.T) {
		heading, err := repo.ResolveHeading(ctx, fixture.bookID, 11)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleActive, heading.Status)
		require.NotEmpty(t, heading.ActiveRecords)
		assert.Equal(t, fixture.splitB.anchor, *heading.ActiveRecords[0].CanonicalAnchor)

		page, err := repo.ResolvePage(ctx, fixture.bookID, 1)
		require.NoError(t, err)
		assert.Nil(t, page.CanonicalAnchor, "physical pages never become canonical")
		require.NotEmpty(t, page.ActiveRecords)
		assert.Equal(t, fixture.splitB.anchor, *page.ActiveRecords[0].CanonicalAnchor)
	})

	t.Run("non derived heading and page fall back to source rows", func(t *testing.T) {
		heading, err := repo.ResolveHeading(ctx, fixture.bookID, 13)
		require.NoError(t, err)
		require.Len(t, heading.ActiveRecords, 1)
		assert.Equal(t, entity.AnchorTargetBookHeading, heading.ActiveRecords[0].TargetType)

		page, err := repo.ResolvePage(ctx, fixture.bookID, 3)
		require.NoError(t, err)
		require.Len(t, page.ActiveRecords, 1)
		assert.Equal(t, entity.AnchorTargetBookPage, page.ActiveRecords[0].TargetType)
		assert.Nil(t, page.ActiveRecords[0].CanonicalAnchor)
	})

	t.Run("soft tombstoned legacy locators follow moved unit lineage", func(t *testing.T) {
		heading, err := repo.ResolveHeading(ctx, fixture.bookID, 12)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleTombstoned, heading.Status)
		require.Len(t, heading.ActiveRecords, 1)
		assert.Equal(t, fixture.moved.id, *heading.ActiveRecords[0].UnitID)
		require.Len(t, heading.RedirectChain, 1)
		assert.Equal(t, fixture.deletedRoot.anchor, heading.RedirectChain[0].From)
		assert.Equal(t, fixture.moved.anchor, heading.RedirectChain[0].To)

		page, err := repo.ResolvePage(ctx, fixture.bookID, 2)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleTombstoned, page.Status)
		require.Len(t, page.ActiveRecords, 1)
		assert.Equal(t, fixture.moved.id, *page.ActiveRecords[0].UnitID)
		require.Len(t, page.RedirectChain, 1)
	})

	t.Run("soft tombstoned source without registry is known but empty", func(t *testing.T) {
		heading, err := repo.ResolveHeading(ctx, fixture.bookID, 14)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleTombstoned, heading.Status)
		assert.Empty(t, heading.ActiveRecords)

		page, err := repo.ResolvePage(ctx, fixture.bookID, 4)
		require.NoError(t, err)
		assert.Equal(t, entity.UnitLifecycleTombstoned, page.Status)
		assert.Empty(t, page.ActiveRecords)
	})

	t.Run("unknown and unpublished never leak", func(t *testing.T) {
		_, err := repo.ResolveCanonicalUnit(ctx, "kitab/2147480000/h/1/u/1")
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
		_, err = repo.ResolveHeading(ctx, fixture.hiddenBookID, 11)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
		_, err = repo.ResolvePage(ctx, fixture.hiddenBookID, 1)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
		_, err = repo.ResolveCanonicalUnit(ctx, fixture.hiddenUnit.anchor)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
	})

	t.Run("hidden and cross Work successors fail closed", func(t *testing.T) {
		_, err := repo.ResolveCanonicalUnit(ctx, fixture.hiddenRoot.anchor)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
		require.ErrorIs(t, err, errUnsafeAnchorLineage)

		_, err = repo.ResolveCanonicalUnit(ctx, fixture.crossRoot.anchor)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
		require.ErrorIs(t, err, errUnsafeAnchorLineage)
	})

	t.Run("active root with outgoing edge fails closed", func(t *testing.T) {
		_, err := repo.ResolveCanonicalUnit(ctx, fixture.activeBad.anchor)
		require.ErrorIs(t, err, errUnsafeAnchorLineage)

		_, err = unitRepo.ResolveUnit(ctx, fixture.activeBad.id)
		require.ErrorIs(t, err, errUnsafeAnchorLineage)
	})

	t.Run("orphan superseded root fails closed", func(t *testing.T) {
		_, err := repo.ResolveCanonicalUnit(ctx, fixture.orphan.anchor)
		require.ErrorIs(t, err, errUnsafeAnchorLineage)

		_, err = unitRepo.ResolveUnit(ctx, fixture.orphan.id)
		require.ErrorIs(t, err, errUnsafeAnchorLineage)
	})

	t.Run("unpublished Work invalidates every public lookup", func(t *testing.T) {
		_, err := pg.Pool.Exec(ctx, `UPDATE book_publications SET status = 'hidden' WHERE book_id = $1`, fixture.bookID)
		require.NoError(t, err)

		_, err = repo.ResolveCanonicalUnit(ctx, fixture.direct.anchor)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
		_, err = repo.ResolveHeading(ctx, fixture.bookID, 11)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)
		_, err = repo.ResolvePage(ctx, fixture.bookID, 1)
		require.ErrorIs(t, err, entity.ErrAnchorNotFound)

		_, err = pg.Pool.Exec(ctx, `UPDATE book_publications SET status = 'published' WHERE book_id = $1`, fixture.bookID)
		require.NoError(t, err)
	})

	t.Run("cycle is explicit and also blocks internal B1 resolution", func(t *testing.T) {
		got, err := repo.ResolveCanonicalUnit(ctx, fixture.cycleA.anchor)
		require.NoError(t, err)
		assert.True(t, got.CycleDetected)
		require.Len(t, got.RedirectChain, 2)

		_, err = unitRepo.ResolveUnit(ctx, fixture.cycleA.id)
		require.ErrorIs(t, err, entity.ErrAnchorLineageCycle)

		audit, err := unitRepo.AuditCounts(ctx)
		require.NoError(t, err)
		assert.Positive(t, audit.Violations.LineageCycle)
	})
}

type liveAnchorUnit struct {
	id        string
	anchor    string
	bookID    int
	headingID int
	pageID    int
	ordinal   int
	position  int
	lifecycle string
}

type liveAnchorFixture struct {
	bookID            int
	hiddenBookID      int
	publicOtherBookID int
	ayahKey           string
	direct            liveAnchorUnit
	root              liveAnchorUnit
	mid               liveAnchorUnit
	splitA            liveAnchorUnit
	splitB            liveAnchorUnit
	mergeRoot         liveAnchorUnit
	tombstone         liveAnchorUnit
	deletedRoot       liveAnchorUnit
	moved             liveAnchorUnit
	cycleA            liveAnchorUnit
	cycleB            liveAnchorUnit
	hiddenUnit        liveAnchorUnit
	otherUnit         liveAnchorUnit
	hiddenRoot        liveAnchorUnit
	crossRoot         liveAnchorUnit
	activeBad         liveAnchorUnit
	activeBadEnd      liveAnchorUnit
	orphan            liveAnchorUnit
	orderRoot         liveAnchorUnit
	orderFirst        liveAnchorUnit
	orderSecond       liveAnchorUnit
	longRoot          liveAnchorUnit
	longTarget        liveAnchorUnit
	longUnits         []liveAnchorUnit
	diamondRoot       liveAnchorUnit
	diamondEnd        liveAnchorUnit
	diamondUnits      []liveAnchorUnit
	diamondEdges      [][3]string
}

func seedLiveAnchorFixture(t *testing.T, pg *postgres.Postgres) liveAnchorFixture {
	t.Helper()

	ctx := context.Background()
	bookID := 1_700_000_000 + int(time.Now().UnixNano()%10_000_000)
	hiddenBookID := bookID + 1
	publicOtherBookID := bookID + 2
	ayahNumber := 1_000_000 + bookID%10_000_000
	ayahKey := fmt.Sprintf("114:%d", ayahNumber)

	_, err := pg.Pool.Exec(ctx, `
		INSERT INTO quran_surahs (surah_id, ayah_count)
		VALUES (114, 6)
		ON CONFLICT (surah_id) DO NOTHING`)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
		INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key)
		VALUES (114, $1, $2)`, ayahNumber, ayahKey)
	require.NoError(t, err)

	for _, id := range []int{bookID, hiddenBookID, publicOtherBookID} {
		_, err = pg.Pool.Exec(ctx, `INSERT INTO books (id, name) VALUES ($1, $2)`, id, fmt.Sprintf("anchor-live-%d", id))
		require.NoError(t, err)

		for pageID := 1; pageID <= 5; pageID++ {
			_, err = pg.Pool.Exec(ctx, `
				INSERT INTO book_pages (book_id, page_id, content_html, content_text, is_deleted)
				VALUES ($1, $2, '<p>fixture</p>', 'fixture', $3)`,
				id, pageID, pageID == 2 || pageID == 4)
			require.NoError(t, err)
		}

		for headingID, pageID := range map[int]int{11: 1, 12: 2, 13: 3, 14: 4, 15: 1, 16: 5} {
			_, err = pg.Pool.Exec(ctx, `
				INSERT INTO book_headings
					(book_id, heading_id, page_id, depth, ordinal, content, is_deleted)
				VALUES ($1, $2, $3, 0, $2, 'fixture', $4)`,
				id, headingID, pageID, headingID == 12 || headingID == 14)
			require.NoError(t, err)
		}
	}

	_, err = pg.Pool.Exec(ctx, `
		INSERT INTO book_publications (book_id, status)
		VALUES ($1, 'published'), ($2, 'hidden'), ($3, 'published')`,
		bookID, hiddenBookID, publicOtherBookID)
	require.NoError(t, err)

	unit := func(book, heading, page, ordinal, position int, lifecycle string) liveAnchorUnit {
		return liveAnchorUnit{
			id:        uuid.NewString(),
			anchor:    fmt.Sprintf("kitab/%d/h/%d/u/%d", book, heading, ordinal),
			bookID:    book,
			headingID: heading,
			pageID:    page,
			ordinal:   ordinal,
			position:  position,
			lifecycle: lifecycle,
		}
	}

	fixture := liveAnchorFixture{
		bookID:            bookID,
		hiddenBookID:      hiddenBookID,
		publicOtherBookID: publicOtherBookID,
		ayahKey:           ayahKey,
		direct:            unit(bookID, 11, 1, 1, 30, entity.UnitLifecycleActive),
		root:              unit(bookID, 11, 1, 2, 10, entity.UnitLifecycleSuperseded),
		mid:               unit(bookID, 11, 1, 3, 10, entity.UnitLifecycleSuperseded),
		splitA:            unit(bookID, 11, 1, 4, 20, entity.UnitLifecycleActive),
		splitB:            unit(bookID, 11, 1, 5, 10, entity.UnitLifecycleActive),
		mergeRoot:         unit(bookID, 11, 1, 6, 40, entity.UnitLifecycleSuperseded),
		tombstone:         unit(bookID, 11, 1, 7, 50, entity.UnitLifecycleTombstoned),
		deletedRoot:       unit(bookID, 12, 2, 1, 0, entity.UnitLifecycleSuperseded),
		moved:             unit(bookID, 11, 1, 8, 25, entity.UnitLifecycleActive),
		cycleA:            unit(bookID, 11, 1, 9, 60, entity.UnitLifecycleSuperseded),
		cycleB:            unit(bookID, 11, 1, 10, 60, entity.UnitLifecycleSuperseded),
		hiddenUnit:        unit(hiddenBookID, 11, 1, 1, 0, entity.UnitLifecycleActive),
		otherUnit:         unit(publicOtherBookID, 11, 1, 1, 0, entity.UnitLifecycleActive),
		hiddenRoot:        unit(bookID, 11, 1, 20, 70, entity.UnitLifecycleSuperseded),
		crossRoot:         unit(bookID, 11, 1, 21, 71, entity.UnitLifecycleSuperseded),
		activeBad:         unit(bookID, 16, 5, 1, 72, entity.UnitLifecycleActive),
		activeBadEnd:      unit(bookID, 16, 5, 2, 73, entity.UnitLifecycleActive),
		orphan:            unit(bookID, 16, 5, 3, 74, entity.UnitLifecycleSuperseded),
		orderRoot:         unit(bookID, 11, 1, 24, 80, entity.UnitLifecycleSuperseded),
		orderFirst:        unit(bookID, 11, 1, 25, 90, entity.UnitLifecycleActive),
		orderSecond:       unit(bookID, 15, 1, 1, 0, entity.UnitLifecycleActive),
	}
	for ordinal := 100; ordinal <= 135; ordinal++ {
		lifecycle := entity.UnitLifecycleSuperseded
		if ordinal == 135 {
			lifecycle = entity.UnitLifecycleActive
		}

		fixture.longUnits = append(fixture.longUnits, unit(bookID, 11, 1, ordinal, ordinal, lifecycle))
	}

	fixture.longRoot = fixture.longUnits[0]
	fixture.longTarget = fixture.longUnits[len(fixture.longUnits)-1]

	fixture.diamondRoot = unit(bookID, 11, 1, 200, 500, entity.UnitLifecycleSuperseded)
	fixture.diamondUnits = append(fixture.diamondUnits, fixture.diamondRoot)
	previous := []liveAnchorUnit{fixture.diamondRoot}
	nextOrdinal := 201

	for range 24 {
		left := unit(bookID, 11, 1, nextOrdinal, 500+nextOrdinal, entity.UnitLifecycleSuperseded)
		right := unit(bookID, 11, 1, nextOrdinal+1, 500+nextOrdinal+1, entity.UnitLifecycleSuperseded)
		nextOrdinal += 2

		fixture.diamondUnits = append(fixture.diamondUnits, left, right)
		for _, predecessor := range previous {
			fixture.diamondEdges = append(
				fixture.diamondEdges,
				[3]string{predecessor.id, left.id, entity.UnitLineageReasonEdit},
				[3]string{predecessor.id, right.id, entity.UnitLineageReasonEdit},
			)
		}

		previous = []liveAnchorUnit{left, right}
	}

	fixture.diamondEnd = unit(bookID, 11, 1, nextOrdinal, 900, entity.UnitLifecycleActive)

	fixture.diamondUnits = append(fixture.diamondUnits, fixture.diamondEnd)
	for _, predecessor := range previous {
		fixture.diamondEdges = append(fixture.diamondEdges,
			[3]string{predecessor.id, fixture.diamondEnd.id, entity.UnitLineageReasonEdit})
	}

	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	_, err = tx.Exec(ctx, registryWriterGUC)
	require.NoError(t, err)

	units := []liveAnchorUnit{
		fixture.direct, fixture.root, fixture.mid, fixture.splitA, fixture.splitB,
		fixture.mergeRoot, fixture.tombstone, fixture.deletedRoot, fixture.moved,
		fixture.cycleA, fixture.cycleB, fixture.hiddenUnit, fixture.otherUnit,
		fixture.hiddenRoot, fixture.crossRoot, fixture.activeBad, fixture.activeBadEnd, fixture.orphan,
		fixture.orderRoot, fixture.orderFirst, fixture.orderSecond,
	}
	units = append(units, fixture.longUnits...)
	units = append(units, fixture.diamondUnits...)

	for _, item := range units {
		var retiredAt *time.Time

		if item.lifecycle != entity.UnitLifecycleActive {
			now := time.Now().UTC()
			retiredAt = &now
		}

		_, err = tx.Exec(ctx, `
			INSERT INTO citable_units (
				id, corpus, book_id, heading_id, page_id, kind, ordinal, position,
				anchor, text, text_normalized, normalization_version, content_hash,
				occurrence, language, provenance_class, provenance_detail, lifecycle, retired_at
			) VALUES (
				$1, 'kitab', $2, $3, $4, 'paragraph', $5, $6,
				$7, $8, $8, 1, $9, 1, 'ar', 'source', '{}'::jsonb, $10, $11
			)`,
			item.id, item.bookID, item.headingID, item.pageID, item.ordinal, item.position,
			item.anchor, "text-"+item.id, []byte("hash-"+item.id), item.lifecycle, retiredAt)
		require.NoError(t, err)
	}

	edges := [][3]string{
		{fixture.root.id, fixture.mid.id, entity.UnitLineageReasonEdit},
		{fixture.mid.id, fixture.splitA.id, entity.UnitLineageReasonEdit},
		{fixture.mid.id, fixture.splitB.id, entity.UnitLineageReasonEdit},
		{fixture.mergeRoot.id, fixture.splitA.id, entity.UnitLineageReasonEdit},
		{fixture.deletedRoot.id, fixture.moved.id, entity.UnitLineageReasonContentMove},
		{fixture.cycleA.id, fixture.cycleB.id, entity.UnitLineageReasonEdit},
		{fixture.cycleB.id, fixture.cycleA.id, entity.UnitLineageReasonEdit},
		{fixture.hiddenRoot.id, fixture.hiddenUnit.id, entity.UnitLineageReasonEdit},
		{fixture.crossRoot.id, fixture.otherUnit.id, entity.UnitLineageReasonEdit},
		{fixture.activeBad.id, fixture.activeBadEnd.id, entity.UnitLineageReasonEdit},
		{fixture.orderRoot.id, fixture.orderFirst.id, entity.UnitLineageReasonEdit},
		{fixture.orderRoot.id, fixture.orderSecond.id, entity.UnitLineageReasonEdit},
	}
	for i := 0; i < len(fixture.longUnits)-1; i++ {
		edges = append(edges, [3]string{
			fixture.longUnits[i].id,
			fixture.longUnits[i+1].id,
			entity.UnitLineageReasonEdit,
		})
	}

	edges = append(edges, fixture.diamondEdges...)

	for _, edge := range edges {
		_, err = tx.Exec(ctx, `
			INSERT INTO citable_unit_lineage (predecessor_id, successor_id, reason)
			VALUES ($1, $2, $3)`, edge[0], edge[1], edge[2])
		require.NoError(t, err)
	}

	require.NoError(t, tx.Commit(ctx))

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, cleanupErr := pg.Pool.Exec(cleanupCtx, `DELETE FROM quran_ayahs WHERE ayah_key = $1`, ayahKey)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool.Exec(cleanupCtx, `DELETE FROM books WHERE id = ANY($1::int[])`,
			[]int{bookID, hiddenBookID, publicOtherBookID})
		assert.NoError(t, cleanupErr)
	})

	return fixture
}

func TestAnchorLineageSQLHasNoSilentDepthCap(t *testing.T) {
	t.Parallel()

	source := readOwnSourceForContractTest(t, "anchor_postgres.go")
	assert.Contains(t, source, "loadLineageClosure(ctx, q, rootIDs)")
	assert.Contains(t, source, "WITH RECURSIVE reachable(id)")
	assert.Contains(t, source, "UNION\n")
	assert.NotContains(t, source, "UNION ALL")
	assert.Contains(t, source, "lineage.predecessor_id = ANY(ARRAY(SELECT id FROM reachable))")
	assert.NotContains(t, source, "JOIN reachable ON reachable.id = lineage.predecessor_id")
	assert.Contains(t, source, "enforceLineageBudget(len(units), len(edges))")
	assert.NotContains(t, canonicalUnitRootSQL, "u.text")
	assert.NotContains(t, canonicalUnitRootSQL, "u.html")
}

func TestAnchorLineageSafetyBudgetIsExplicit(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, enforceLineageBudget(maxAnchorLineageNodes+1, 0), errAnchorLineageSafetyBudget)
	require.ErrorIs(t, enforceLineageBudget(1, maxAnchorLineageEdges+1), errAnchorLineageSafetyBudget)
	require.NoError(t, enforceLineageBudget(maxAnchorLineageNodes, maxAnchorLineageEdges))
}

func TestPublicLineageVisibilityCorruptionKeeps404AndIntegritySignal(t *testing.T) {
	t.Parallel()

	edge := loadedLineageEdge{Successor: lineageUnit{
		Corpus:  entity.UnitCorpusKitab,
		BookID:  797,
		HasBook: true,
	}}
	err := validateLineageSuccessor(&edge, publicLineagePolicy(797))
	require.ErrorIs(t, err, entity.ErrAnchorNotFound)
	require.ErrorIs(t, err, errUnsafeAnchorLineage)
}

func readOwnSourceForContractTest(t *testing.T, name string) string {
	t.Helper()

	content, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		content, err = os.ReadFile("internal/repo/persistent/" + name)
	}

	require.NoError(t, err)

	return string(content)
}
