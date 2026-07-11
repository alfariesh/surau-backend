package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

// AnchorRepo is the compact, public-read projection for B-2. It deliberately
// shares the same Postgres pool as the registry repo, but exposes no write
// methods and never selects corpus text/HTML.
type AnchorRepo struct {
	*postgres.Postgres
}

const (
	maxAnchorLineageNodes = 4096
	maxAnchorLineageEdges = 16384

	canonicalUnitRootSQL = `
		SELECT u.id::text, u.anchor, u.book_id, u.heading_id, u.page_id,
		       u.lifecycle, u.position, u.updated_at, COALESCE(h.ordinal, -1),
		       (u.license_status IS NULL OR u.license_status = 'permitted')
		FROM citable_units u
		JOIN books b ON b.id = u.book_id AND b.is_deleted = FALSE
		JOIN public_book_publications p ON p.book_id = b.id
		LEFT JOIN book_headings h ON h.book_id = u.book_id AND h.heading_id = u.heading_id
		WHERE u.corpus = 'kitab' AND u.anchor = $1`
)

var (
	errAnchorLineageSafetyBudget = errors.New("anchor lineage safety budget exceeded")
	errUnsafeAnchorLineage       = errors.New("unsafe anchor lineage graph")
)

// NewAnchorRepo -.
func NewAnchorRepo(pg *postgres.Postgres) *AnchorRepo {
	return &AnchorRepo{pg}
}

// ResolveQuranSurah resolves quran/{surah} through the quran_surahs primary
// key. A surah is public content in the existing Quran reader; licensed
// language-specific editorial fields remain gated by that reader separately.
//
//nolint:dupl // simple point lookup intentionally mirrors the Work lookup over a different corpus table
func (r *AnchorRepo) ResolveQuranSurah(ctx context.Context, surahID int) (entity.AnchorLookupResult, error) {
	var updatedAt sql.NullTime

	err := r.Pool.QueryRow(ctx, `
		SELECT updated_at
		FROM quran_surahs
		WHERE surah_id = $1`, surahID).Scan(&updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveQuranSurah: %w", err)
	}

	canonical := "quran/" + strconv.Itoa(surahID)
	record := entity.AnchorRecord{
		TargetType:      entity.AnchorTargetQuranSurah,
		Corpus:          entity.UnitCorpusQuran,
		CanonicalAnchor: &canonical,
		SurahID:         new(surahID),
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt.Time,
	}

	return entity.AnchorLookupResult{
		CanonicalAnchor: &canonical,
		Status:          entity.UnitLifecycleActive,
		ActiveRecords:   []entity.AnchorRecord{record},
	}, nil
}

// ResolveQuran resolves both quran/{surah}:{ayah} and the permanent legacy
// ayah_key alias through the unique quran_ayahs.ayah_key index.
func (r *AnchorRepo) ResolveQuran(ctx context.Context, ayahKey string) (entity.AnchorLookupResult, error) {
	var (
		storedKey string
		updatedAt sql.NullTime
	)

	err := r.Pool.QueryRow(ctx, `
		SELECT ayah_key, updated_at
		FROM quran_ayahs
		WHERE ayah_key = $1`, ayahKey).Scan(&storedKey, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveQuran: %w", err)
	}

	canonical := "quran/" + storedKey
	record := entity.AnchorRecord{
		TargetType:      entity.AnchorTargetQuranAyah,
		Corpus:          entity.UnitCorpusQuran,
		CanonicalAnchor: &canonical,
		AyahKey:         &storedKey,
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt.Time,
	}

	return entity.AnchorLookupResult{
		CanonicalAnchor: &canonical,
		Status:          entity.UnitLifecycleActive,
		ActiveRecords:   []entity.AnchorRecord{record},
	}, nil
}

// ResolveWork resolves the logical kitab Work anchor. The publication join is
// intentionally inside the lookup: an unpublished/deleted book is
// indistinguishable from an unknown Anchor on the public surface.
func (r *AnchorRepo) ResolveWork(ctx context.Context, bookID int) (entity.AnchorLookupResult, error) {
	var updatedAt sql.NullTime

	err := r.Pool.QueryRow(ctx, `
		SELECT GREATEST(b.updated_at, p.updated_at)
		FROM books b
		JOIN public_book_publications p ON p.book_id = b.id
		WHERE b.id = $1 AND b.is_deleted = FALSE`, bookID).Scan(&updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveWork: %w", err)
	}

	canonical := "kitab/" + strconv.Itoa(bookID)
	record := entity.AnchorRecord{
		TargetType:      entity.AnchorTargetBook,
		Corpus:          entity.UnitCorpusKitab,
		CanonicalAnchor: &canonical,
		BookID:          new(bookID),
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt.Time,
	}

	return entity.AnchorLookupResult{
		CanonicalAnchor: &canonical,
		Status:          entity.UnitLifecycleActive,
		ActiveRecords:   []entity.AnchorRecord{record},
	}, nil
}

// ResolveHeading resolves a canonical kitab heading or legacy toc-N within a
// visible book. A known soft-tombstoned heading returns a successful
// tombstoned result. When B-1 units exist, they replace the coarse source-row
// fallback and are returned in current document order.
//
//nolint:gocyclo,cyclop,funlen // one guarded scan loop plus explicit active/tombstone/fallback outcomes
func (r *AnchorRepo) ResolveHeading(
	ctx context.Context,
	bookID, headingID int,
) (entity.AnchorLookupResult, error) {
	canonical := "kitab/" + strconv.Itoa(bookID) + "/h/" + strconv.Itoa(headingID)

	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveHeading snapshot: %w", err)
	}
	defer rollbackTx(ctx, tx)

	rows, err := tx.Query(ctx, `
		WITH candidate AS (
			SELECT h.book_id, h.heading_id, h.page_id, h.is_deleted,
			       b.units_derived_at IS NOT NULL AS units_derived,
			       GREATEST(b.updated_at, p.updated_at, h.updated_at) AS updated_at
			FROM books b
			JOIN public_book_publications p ON p.book_id = b.id
			JOIN book_headings h ON h.book_id = b.id AND h.heading_id = $2
			WHERE b.id = $1 AND b.is_deleted = FALSE
		)
		SELECT c.page_id, c.is_deleted, c.units_derived, c.updated_at,
		       u.id::text, u.anchor, u.heading_id, u.page_id, u.updated_at
		FROM candidate c
		LEFT JOIN citable_units u
		  ON NOT c.is_deleted
		 AND u.corpus = 'kitab'
		 AND u.book_id = c.book_id
		 AND u.heading_id = c.heading_id
		 AND u.lifecycle = 'active'
		 AND (u.license_status IS NULL OR u.license_status = 'permitted')
		ORDER BY u.position NULLS LAST, u.anchor NULLS LAST`, bookID, headingID)
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveHeading: %w", err)
	}
	defer rows.Close()

	result := entity.AnchorLookupResult{CanonicalAnchor: &canonical}
	found := false
	deleted := false
	derived := false

	var (
		fallbackPageID    int
		fallbackUpdatedAt sql.NullTime
	)

	for rows.Next() {
		found = true

		var (
			rowDeleted    bool
			rowUpdatedAt  sql.NullTime
			unitID        sql.NullString
			unitAnchor    sql.NullString
			unitHeadingID sql.NullInt64
			unitPageID    sql.NullInt64
			unitUpdatedAt sql.NullTime
		)
		if err := rows.Scan(&fallbackPageID, &rowDeleted, &derived, &rowUpdatedAt, &unitID, &unitAnchor,
			&unitHeadingID, &unitPageID, &unitUpdatedAt); err != nil {
			return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveHeading scan: %w", err)
		}

		deleted = rowDeleted
		fallbackUpdatedAt = rowUpdatedAt

		if !unitID.Valid {
			continue
		}

		result.ActiveRecords = append(result.ActiveRecords, compactUnitRecord(
			unitID.String,
			unitAnchor.String,
			bookID,
			anchorNullableInt(unitHeadingID),
			anchorNullableInt(unitPageID),
			unitUpdatedAt,
		))
	}

	if err := rows.Err(); err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveHeading rows: %w", err)
	}

	if !found {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	if deleted {
		result.Status = entity.UnitLifecycleTombstoned

		walk, err := r.resolveHistoricalHeadingUnits(ctx, tx, bookID, headingID)
		if err != nil {
			return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveHeading tombstone lineage: %w", err)
		}

		result.RedirectChain = walk.Redirects
		result.CycleDetected = walk.CycleDetected

		for i := range walk.ActiveUnits {
			result.ActiveRecords = append(result.ActiveRecords, lineageUnitRecord(&walk.ActiveUnits[i]))
		}

		return result, nil
	}

	result.Status = entity.UnitLifecycleActive
	if len(result.ActiveRecords) == 0 && !derived {
		result.ActiveRecords = []entity.AnchorRecord{{
			TargetType:      entity.AnchorTargetBookHeading,
			Corpus:          entity.UnitCorpusKitab,
			CanonicalAnchor: &canonical,
			BookID:          new(bookID),
			HeadingID:       new(headingID),
			PageID:          new(fallbackPageID),
			Lifecycle:       entity.UnitLifecycleActive,
			UpdatedAt:       fallbackUpdatedAt.Time,
		}}
	}

	return result, nil
}

// ResolvePage keeps the physical legacy locator resolvable without promoting
// it into the canonical grammar. It returns all active units currently located
// on the page; a non-derived book falls back to the source page row.
//
//nolint:gocyclo,cyclop,funlen // one guarded scan loop plus explicit active/tombstone/fallback outcomes
func (r *AnchorRepo) ResolvePage(ctx context.Context, bookID, pageID int) (entity.AnchorLookupResult, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolvePage snapshot: %w", err)
	}
	defer rollbackTx(ctx, tx)

	rows, err := tx.Query(ctx, `
		WITH candidate AS (
			SELECT bp.book_id, bp.page_id, bp.is_deleted,
			       b.units_derived_at IS NOT NULL AS units_derived,
			       GREATEST(b.updated_at, p.updated_at, bp.updated_at) AS updated_at
			FROM books b
			JOIN public_book_publications p ON p.book_id = b.id
			JOIN book_pages bp ON bp.book_id = b.id AND bp.page_id = $2
			WHERE b.id = $1 AND b.is_deleted = FALSE
		)
		SELECT c.is_deleted, c.units_derived, c.updated_at,
		       u.id::text, u.anchor, u.heading_id, u.page_id, u.updated_at
		FROM candidate c
		LEFT JOIN citable_units u
		  ON NOT c.is_deleted
		 AND u.corpus = 'kitab'
		 AND u.book_id = c.book_id
		 AND u.page_id = c.page_id
		 AND u.lifecycle = 'active'
		 AND (u.license_status IS NULL OR u.license_status = 'permitted')
		LEFT JOIN book_headings h
		  ON h.book_id = u.book_id AND h.heading_id = u.heading_id
		ORDER BY h.ordinal NULLS FIRST, u.position NULLS LAST, u.anchor NULLS LAST`, bookID, pageID)
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolvePage: %w", err)
	}
	defer rows.Close()

	result := entity.AnchorLookupResult{}
	found := false
	deleted := false
	derived := false

	var fallbackUpdatedAt sql.NullTime

	for rows.Next() {
		found = true

		var (
			rowDeleted    bool
			rowUpdatedAt  sql.NullTime
			unitID        sql.NullString
			unitAnchor    sql.NullString
			unitHeadingID sql.NullInt64
			unitPageID    sql.NullInt64
			unitUpdatedAt sql.NullTime
		)
		if err := rows.Scan(&rowDeleted, &derived, &rowUpdatedAt, &unitID, &unitAnchor, &unitHeadingID,
			&unitPageID, &unitUpdatedAt); err != nil {
			return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolvePage scan: %w", err)
		}

		deleted = rowDeleted
		fallbackUpdatedAt = rowUpdatedAt

		if !unitID.Valid {
			continue
		}

		result.ActiveRecords = append(result.ActiveRecords, compactUnitRecord(
			unitID.String,
			unitAnchor.String,
			bookID,
			anchorNullableInt(unitHeadingID),
			anchorNullableInt(unitPageID),
			unitUpdatedAt,
		))
	}

	if err := rows.Err(); err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolvePage rows: %w", err)
	}

	if !found {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	if deleted {
		result.Status = entity.UnitLifecycleTombstoned

		walk, err := r.resolveHistoricalPageUnits(ctx, tx, bookID, pageID)
		if err != nil {
			return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolvePage tombstone lineage: %w", err)
		}

		result.RedirectChain = walk.Redirects
		result.CycleDetected = walk.CycleDetected

		for i := range walk.ActiveUnits {
			result.ActiveRecords = append(result.ActiveRecords, lineageUnitRecord(&walk.ActiveUnits[i]))
		}

		return result, nil
	}

	result.Status = entity.UnitLifecycleActive
	if len(result.ActiveRecords) == 0 && !derived {
		result.ActiveRecords = []entity.AnchorRecord{{
			TargetType: entity.AnchorTargetBookPage,
			Corpus:     entity.UnitCorpusKitab,
			BookID:     new(bookID),
			PageID:     new(pageID),
			Lifecycle:  entity.UnitLifecycleActive,
			UpdatedAt:  fallbackUpdatedAt.Time,
		}}
	}

	return result, nil
}

// ResolveCanonicalUnit resolves one B-1 unit Anchor through the complete
// lineage graph. The unique anchor index identifies the starting unit; the
// book visibility join prevents the resolver from becoming a publication
// bypass.
func (r *AnchorRepo) ResolveCanonicalUnit(
	ctx context.Context,
	canonicalAnchor string,
) (entity.AnchorLookupResult, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveCanonicalUnit snapshot: %w", err)
	}
	defer rollbackTx(ctx, tx)

	unit, err := scanLineageUnit(tx.QueryRow(ctx, canonicalUnitRootSQL, canonicalAnchor))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveCanonicalUnit: %w", err)
	}

	if unit.Lifecycle == entity.UnitLifecycleActive && !unit.PublicEligible {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	walk, err := walkLineageUnits(ctx, tx, []lineageUnit{unit}, publicLineagePolicy(unit.BookID))
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo.ResolveCanonicalUnit lineage: %w", err)
	}

	result := entity.AnchorLookupResult{
		CanonicalAnchor: new(unit.Anchor),
		Status:          unit.Lifecycle,
		RedirectChain:   walk.Redirects,
		CycleDetected:   walk.CycleDetected,
	}
	for i := range walk.ActiveUnits {
		result.ActiveRecords = append(result.ActiveRecords, lineageUnitRecord(&walk.ActiveUnits[i]))
	}

	return result, nil
}

func (r *AnchorRepo) resolveHistoricalHeadingUnits(
	ctx context.Context,
	q lineageQuerier,
	bookID, headingID int,
) (lineageWalkResult, error) {
	return r.resolveHistoricalUnitRoots(ctx, q, bookID, `
		SELECT u.id::text, u.anchor, u.book_id, u.heading_id, u.page_id,
		       u.lifecycle, u.position, u.updated_at, COALESCE(h.ordinal, -1),
		       (u.license_status IS NULL OR u.license_status = 'permitted')
		FROM citable_units u
		JOIN books b ON b.id = u.book_id AND b.is_deleted = FALSE
		JOIN public_book_publications p ON p.book_id = b.id
		LEFT JOIN book_headings h ON h.book_id = u.book_id AND h.heading_id = u.heading_id
		WHERE u.corpus = 'kitab' AND u.book_id = $1 AND u.heading_id = $2
		ORDER BY u.position, u.anchor`, bookID, headingID)
}

func (r *AnchorRepo) resolveHistoricalPageUnits(
	ctx context.Context,
	q lineageQuerier,
	bookID, pageID int,
) (lineageWalkResult, error) {
	return r.resolveHistoricalUnitRoots(ctx, q, bookID, `
		SELECT u.id::text, u.anchor, u.book_id, u.heading_id, u.page_id,
		       u.lifecycle, u.position, u.updated_at, COALESCE(h.ordinal, -1),
		       (u.license_status IS NULL OR u.license_status = 'permitted')
		FROM citable_units u
		JOIN books b ON b.id = u.book_id AND b.is_deleted = FALSE
		JOIN public_book_publications p ON p.book_id = b.id
		LEFT JOIN book_headings h ON h.book_id = u.book_id AND h.heading_id = u.heading_id
		WHERE u.corpus = 'kitab' AND u.book_id = $1 AND u.page_id = $2
		ORDER BY COALESCE(h.ordinal, -1), u.position, u.anchor`, bookID, pageID)
}

func (r *AnchorRepo) resolveHistoricalUnitRoots(
	ctx context.Context,
	q lineageQuerier,
	bookID int,
	query string,
	args ...any,
) (lineageWalkResult, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return lineageWalkResult{}, err
	}
	defer rows.Close()

	roots := make([]lineageUnit, 0)

	for rows.Next() {
		var root lineageUnit
		if err := rows.Scan(&root.ID, &root.Anchor, &root.BookID, &root.HeadingID, &root.PageID,
			&root.Lifecycle, &root.Position, &root.UpdatedAt, &root.HeadingOrdinal,
			&root.PublicEligible); err != nil {
			return lineageWalkResult{}, err
		}

		root.Corpus = entity.UnitCorpusKitab
		root.HasBook = true
		roots = append(roots, root)
	}

	if err := rows.Err(); err != nil {
		return lineageWalkResult{}, err
	}

	return walkLineageUnits(ctx, q, roots, publicLineagePolicy(bookID))
}

type lineageQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type lineageUnit struct {
	ID             string
	Anchor         string
	Corpus         string
	BookID         int
	HasBook        bool
	HeadingID      *int
	HeadingOrdinal int
	PageID         *int
	Lifecycle      string
	Position       int
	UpdatedAt      sql.NullTime
	PublicEligible bool
}

type lineageWalkResult struct {
	ActiveUnits   []lineageUnit
	Redirects     []entity.AnchorRedirect
	CycleDetected bool
}

type lineagePolicy struct {
	RequirePublic bool
	BookID        int
}

type lineageGraphEdge struct {
	PredecessorID string
	SuccessorID   string
	Redirect      entity.AnchorRedirect
}

type loadedLineageEdge struct {
	PredecessorID     string
	PredecessorAnchor string
	Reason            string
	Successor         lineageUnit
	PublicVisible     bool
}

func publicLineagePolicy(bookID int) lineagePolicy {
	return lineagePolicy{RequirePublic: true, BookID: bookID}
}

// walkUnitLineage is the single graph algorithm used by both the public
// Anchor resolver and B-1 ResolveUnit.
func walkUnitLineage(
	ctx context.Context,
	q lineageQuerier,
	root *entity.CitableUnit,
) (lineageWalkResult, error) {
	return walkLineageUnits(ctx, q, []lineageUnit{lineageUnitFromEntity(root)}, lineagePolicy{})
}

// walkLineageUnits loads one set-based recursive closure. The CTE carries only
// node IDs and applies set-union de-duplication, so repeated diamonds and
// cycles collapse before edge hydration. Safety budgets fail
// explicitly; they never return a truncated redirect graph.
//
//nolint:gocyclo,cyclop,funlen // guarded graph hydration keeps budget and visibility checks adjacent
func walkLineageUnits(
	ctx context.Context,
	q lineageQuerier,
	roots []lineageUnit,
	policy lineagePolicy,
) (lineageWalkResult, error) {
	units := make(map[string]lineageUnit, len(roots))
	rootIDs := make([]string, 0, len(roots))

	for i := range roots {
		root := roots[i]
		if _, exists := units[root.ID]; exists {
			continue
		}

		units[root.ID] = root
		rootIDs = append(rootIDs, root.ID)
	}

	if err := enforceLineageBudget(len(units), 0); err != nil {
		return lineageWalkResult{}, err
	}

	loaded, err := loadLineageClosure(ctx, q, rootIDs)
	if err != nil {
		return lineageWalkResult{}, err
	}

	edges := make(map[[2]string]lineageGraphEdge)
	outgoing := make(map[string]int)

	for i := range loaded {
		edge := &loaded[i]
		if err := validateLineageSuccessor(edge, policy); err != nil {
			return lineageWalkResult{}, err
		}

		key := [2]string{edge.PredecessorID, edge.Successor.ID}
		if _, exists := edges[key]; !exists {
			edges[key] = lineageGraphEdge{
				PredecessorID: edge.PredecessorID,
				SuccessorID:   edge.Successor.ID,
				Redirect: entity.AnchorRedirect{
					From:   edge.PredecessorAnchor,
					To:     edge.Successor.Anchor,
					Reason: edge.Reason,
				},
			}
			outgoing[edge.PredecessorID]++
		}

		units[edge.Successor.ID] = edge.Successor
		if err := enforceLineageBudget(len(units), len(edges)); err != nil {
			return lineageWalkResult{}, err
		}
	}

	depths, err := shortestLineageDepths(rootIDs, edges)
	if err != nil {
		return lineageWalkResult{}, err
	}

	for key, edge := range edges {
		edge.Redirect.Depth = depths[edge.PredecessorID] + 1
		edges[key] = edge
	}

	return finishLineageWalk(units, edges, outgoing, policy)
}

//nolint:funlen // one compact closure query and its coupled scan contract
func loadLineageClosure(
	ctx context.Context,
	q lineageQuerier,
	rootIDs []string,
) ([]loadedLineageEdge, error) {
	rows, err := q.Query(ctx, `
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
		       visible_book.id IS NOT NULL AND publication.book_id IS NOT NULL,
		       (successor.license_status IS NULL OR successor.license_status = 'permitted')
		FROM citable_unit_lineage lineage
		JOIN citable_units predecessor ON predecessor.id = lineage.predecessor_id
		JOIN citable_units successor ON successor.id = lineage.successor_id
		LEFT JOIN books visible_book
		  ON visible_book.id = successor.book_id AND visible_book.is_deleted = FALSE
		LEFT JOIN public_book_publications publication
		  ON publication.book_id = visible_book.id
		LEFT JOIN book_headings heading
		  ON heading.book_id = successor.book_id AND heading.heading_id = successor.heading_id
		WHERE lineage.predecessor_id = ANY(ARRAY(SELECT id FROM reachable))
		ORDER BY predecessor.anchor, successor.anchor, lineage.reason`, rootIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	loaded := make([]loadedLineageEdge, 0)

	for rows.Next() {
		var (
			edge      loadedLineageEdge
			bookID    sql.NullInt64
			headingID sql.NullInt64
			pageID    sql.NullInt64
		)
		if err := rows.Scan(
			&edge.PredecessorID,
			&edge.PredecessorAnchor,
			&edge.Reason,
			&edge.Successor.ID,
			&edge.Successor.Anchor,
			&edge.Successor.Corpus,
			&bookID,
			&headingID,
			&pageID,
			&edge.Successor.Lifecycle,
			&edge.Successor.Position,
			&edge.Successor.UpdatedAt,
			&edge.Successor.HeadingOrdinal,
			&edge.PublicVisible,
			&edge.Successor.PublicEligible,
		); err != nil {
			return nil, err
		}

		edge.Successor.HasBook = bookID.Valid
		if bookID.Valid {
			edge.Successor.BookID = int(bookID.Int64)
		}

		edge.Successor.HeadingID = anchorNullableInt(headingID)
		edge.Successor.PageID = anchorNullableInt(pageID)
		loaded = append(loaded, edge)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return loaded, nil
}

func validateLineageSuccessor(edge *loadedLineageEdge, policy lineagePolicy) error {
	if !policy.RequirePublic {
		return nil
	}

	if edge.Successor.Corpus != entity.UnitCorpusKitab ||
		!edge.Successor.HasBook ||
		edge.Successor.BookID != policy.BookID ||
		!edge.PublicVisible {
		return fmt.Errorf(
			"%w: %w: successor is outside the visible Work",
			entity.ErrAnchorNotFound,
			errUnsafeAnchorLineage,
		)
	}

	return nil
}

func shortestLineageDepths(
	rootIDs []string,
	edges map[[2]string]lineageGraphEdge,
) (map[string]int, error) {
	adjacency := make(map[string][]string)
	for _, edge := range edges {
		adjacency[edge.PredecessorID] = append(adjacency[edge.PredecessorID], edge.SuccessorID)
	}

	depths := make(map[string]int, len(edges)+len(rootIDs))
	queue := make([]string, 0, len(edges)+len(rootIDs))

	for _, rootID := range rootIDs {
		if _, exists := depths[rootID]; exists {
			continue
		}

		depths[rootID] = 0
		queue = append(queue, rootID)
	}

	for len(queue) > 0 {
		predecessorID := queue[0]
		queue = queue[1:]

		for _, successorID := range adjacency[predecessorID] {
			if _, discovered := depths[successorID]; discovered {
				continue
			}

			depths[successorID] = depths[predecessorID] + 1
			queue = append(queue, successorID)
		}
	}

	for _, edge := range edges {
		if _, ok := depths[edge.PredecessorID]; !ok {
			return nil, fmt.Errorf("%w: predecessor outside reachable closure", errUnsafeAnchorLineage)
		}
	}

	return depths, nil
}

//nolint:gocognit,gocyclo,cyclop // explicit lifecycle invariants share one graph-finalization pass
func finishLineageWalk(
	units map[string]lineageUnit,
	edges map[[2]string]lineageGraphEdge,
	outgoing map[string]int,
	policy lineagePolicy,
) (lineageWalkResult, error) {
	result := lineageWalkResult{CycleDetected: lineageHasCycle(units, edges)}

	result.Redirects = make([]entity.AnchorRedirect, 0, len(edges))
	for _, edge := range edges {
		predecessor := units[edge.PredecessorID]
		successor := units[edge.SuccessorID]

		if policy.RequirePublic && (!predecessor.PublicEligible || !successor.PublicEligible) {
			continue
		}

		result.Redirects = append(result.Redirects, edge.Redirect)
	}

	sort.Slice(result.Redirects, func(i, j int) bool {
		left, right := &result.Redirects[i], &result.Redirects[j]
		if left.Depth != right.Depth {
			return left.Depth < right.Depth
		}

		if left.From != right.From {
			return left.From < right.From
		}

		if left.To != right.To {
			return left.To < right.To
		}

		return left.Reason < right.Reason
	})

	active := make(map[string]lineageUnit)

	for id := range units {
		unit := units[id]
		if unit.Lifecycle == entity.UnitLifecycleSuperseded && outgoing[id] == 0 && !result.CycleDetected {
			return lineageWalkResult{}, fmt.Errorf("%w: superseded unit %s has no successor", errUnsafeAnchorLineage, id)
		}

		if unit.Lifecycle != entity.UnitLifecycleActive {
			continue
		}

		if outgoing[id] > 0 && !result.CycleDetected {
			return lineageWalkResult{}, fmt.Errorf("%w: active unit %s has an outgoing edge", errUnsafeAnchorLineage, id)
		}

		if policy.RequirePublic && !unit.PublicEligible {
			continue
		}

		active[id] = unit
	}

	result.ActiveUnits = sortedLineageUnits(active)

	return result, nil
}

func lineageHasCycle(
	units map[string]lineageUnit,
	edges map[[2]string]lineageGraphEdge,
) bool {
	indegree := make(map[string]int, len(units))
	adjacency := make(map[string][]string, len(units))

	for id := range units {
		indegree[id] = 0
	}

	for _, edge := range edges {
		adjacency[edge.PredecessorID] = append(adjacency[edge.PredecessorID], edge.SuccessorID)
		indegree[edge.SuccessorID]++
	}

	queue := make([]string, 0, len(units))

	for id, degree := range indegree {
		if degree == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0

	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++

		for _, successorID := range adjacency[id] {
			indegree[successorID]--
			if indegree[successorID] == 0 {
				queue = append(queue, successorID)
			}
		}
	}

	return visited != len(units)
}

func enforceLineageBudget(nodes, edges int) error {
	if nodes > maxAnchorLineageNodes || edges > maxAnchorLineageEdges {
		return fmt.Errorf(
			"%w: nodes=%d/%d edges=%d/%d",
			errAnchorLineageSafetyBudget,
			nodes,
			maxAnchorLineageNodes,
			edges,
			maxAnchorLineageEdges,
		)
	}

	return nil
}

func sortedLineageUnits(activeByID map[string]lineageUnit) []lineageUnit {
	units := make([]lineageUnit, 0, len(activeByID))
	for id := range activeByID {
		units = append(units, activeByID[id])
	}

	sort.Slice(units, func(i, j int) bool {
		left, right := &units[i], &units[j]
		if left.HeadingOrdinal != right.HeadingOrdinal {
			return left.HeadingOrdinal < right.HeadingOrdinal
		}

		if left.Position != right.Position {
			return left.Position < right.Position
		}

		return left.Anchor < right.Anchor
	})

	return units
}

func lineageUnitFromEntity(unit *entity.CitableUnit) lineageUnit {
	return lineageUnit{
		ID:        unit.ID,
		Anchor:    unit.Anchor,
		Corpus:    unit.Corpus,
		BookID:    unit.BookID,
		HasBook:   unit.Corpus == entity.UnitCorpusKitab,
		HeadingID: unit.HeadingID,
		PageID:    unit.PageID,
		Lifecycle: unit.Lifecycle,
		Position:  unit.Position,
		UpdatedAt: sql.NullTime{Time: unit.UpdatedAt, Valid: true},
	}
}

func scanLineageUnit(row pgx.Row) (lineageUnit, error) {
	var unit lineageUnit
	if err := row.Scan(
		&unit.ID,
		&unit.Anchor,
		&unit.BookID,
		&unit.HeadingID,
		&unit.PageID,
		&unit.Lifecycle,
		&unit.Position,
		&unit.UpdatedAt,
		&unit.HeadingOrdinal,
		&unit.PublicEligible,
	); err != nil {
		return lineageUnit{}, err
	}

	unit.Corpus = entity.UnitCorpusKitab
	unit.HasBook = true

	return unit, nil
}

func lineageUnitRecord(unit *lineageUnit) entity.AnchorRecord {
	return compactUnitRecord(unit.ID, unit.Anchor, unit.BookID, unit.HeadingID, unit.PageID, unit.UpdatedAt)
}

func compactUnitRecord(
	unitID, canonicalAnchor string,
	bookID int,
	headingID, pageID *int,
	updatedAt sql.NullTime,
) entity.AnchorRecord {
	return entity.AnchorRecord{
		TargetType:      entity.AnchorTargetCitableUnit,
		Corpus:          entity.UnitCorpusKitab,
		CanonicalAnchor: new(canonicalAnchor),
		UnitID:          new(unitID),
		BookID:          new(bookID),
		HeadingID:       headingID,
		PageID:          pageID,
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt.Time,
	}
}

func anchorNullableInt(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}

	converted := int(value.Int64)

	return &converted
}
