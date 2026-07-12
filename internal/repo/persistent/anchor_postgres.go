package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

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
		storedKey     string
		surahID       int
		updatedAt     sql.NullTime
		primaryUnitID sql.NullString
		primaryAnchor sql.NullString
	)

	err := r.Pool.QueryRow(ctx, `
			SELECT a.ayah_key, a.surah_id, a.updated_at, primary_unit.id::text, primary_unit.anchor
			FROM quran_ayahs a
			LEFT JOIN quran_surahs s ON s.surah_id = a.surah_id
			LEFT JOIN quran_citable_unit_bindings binding
		  ON binding.surah_id = a.surah_id
		 AND binding.ayah_number = a.ayah_number
		 AND binding.role = 'primary_text'
		LEFT JOIN citable_units primary_unit
			  ON primary_unit.id = binding.unit_id
			 AND primary_unit.lifecycle = 'active'
			 AND s.units_stale_at IS NULL
		 AND primary_unit.text = a.text_qpc_hafs
		 AND binding.source_updated_at = a.updated_at
		 AND EXISTS (
		     SELECT 1 FROM citable_units_with_effective_license license
		     WHERE license.id = primary_unit.id
		       AND license.effective_license_status = 'permitted'
		 )
		WHERE a.ayah_key = $1`, ayahKey).Scan(
		&storedKey, &surahID, &updatedAt, &primaryUnitID, &primaryAnchor,
	)
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
		SurahID:         &surahID,
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt.Time,
	}
	if primaryUnitID.Valid && primaryAnchor.Valid {
		record.PrimaryUnitID = new(primaryUnitID.String)
		record.PrimaryUnitAnchor = new(primaryAnchor.String)
	}

	return entity.AnchorLookupResult{
		CanonicalAnchor: &canonical,
		Status:          entity.UnitLifecycleActive,
		ActiveRecords:   []entity.AnchorRecord{record},
	}, nil
}

// ResolveQuranLocator maps the grandfathered juz/hizb/page tuple to its first
// and last canonical ayah. It delegates the actual point resolution back to
// ResolveQuran, preserving one B-2 resolver/read model.
//
//nolint:gocritic,gocyclo,cyclop,wsl_v5 // bounded switch mirrors the three legacy locator families; unnamed results match the repo interface
func (r *AnchorRepo) ResolveQuranLocator(
	ctx context.Context,
	kind string,
	number int,
) (entity.AnchorLookupResult, entity.AnchorLookupResult, error) {
	column := ""
	switch kind {
	case "juz":
		if number < 1 || number > 30 {
			return entity.AnchorLookupResult{}, entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
		}
		column = "juz_number"
	case "hizb":
		if number < 1 || number > 60 {
			return entity.AnchorLookupResult{}, entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
		}
		column = "hizb_number"
	case "page":
		if number < 1 {
			return entity.AnchorLookupResult{}, entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
		}
		column = "page_number"
	default:
		return entity.AnchorLookupResult{}, entity.AnchorLookupResult{}, entity.ErrInvalidAnchor
	}

	query := fmt.Sprintf(`
SELECT (
    SELECT ayah_key FROM quran_ayahs WHERE %s = $1
    ORDER BY surah_id, ayah_number LIMIT 1
), (
    SELECT ayah_key FROM quran_ayahs WHERE %s = $1
    ORDER BY surah_id DESC, ayah_number DESC LIMIT 1
)`, column, column)
	var startKey, endKey sql.NullString
	if err := r.Pool.QueryRow(ctx, query, number).Scan(&startKey, &endKey); err != nil {
		return entity.AnchorLookupResult{}, entity.AnchorLookupResult{},
			fmt.Errorf("AnchorRepo.ResolveQuranLocator: %w", err)
	}
	if !startKey.Valid || !endKey.Valid {
		return entity.AnchorLookupResult{}, entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	start, err := r.ResolveQuran(ctx, startKey.String)
	if err != nil {
		return entity.AnchorLookupResult{}, entity.AnchorLookupResult{}, err
	}
	end, err := r.ResolveQuran(ctx, endKey.String)
	if err != nil {
		return entity.AnchorLookupResult{}, entity.AnchorLookupResult{}, err
	}

	return start, end, nil
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
	if strings.HasPrefix(canonicalAnchor, "quran/") {
		return r.resolveCanonicalQuranUnit(ctx, canonicalAnchor)
	}

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

// resolveCanonicalQuranUnit reuses the B-1 lineage walker, then applies the
// Quran source/license/current-text gate to its active endpoints. Logical ayah
// Anchors remain resolvable even while a stale child unit is hidden.
//
//nolint:gocognit,gocyclo,cyclop,funlen,wsl_v5 // root, shared lineage walk, and set-based active hydration
func (r *AnchorRepo) resolveCanonicalQuranUnit(
	ctx context.Context,
	canonicalAnchor string,
) (entity.AnchorLookupResult, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo Quran unit snapshot: %w", err)
	}
	defer rollbackTx(ctx, tx)

	var (
		root           lineageUnit
		pageID         sql.NullInt64
		surahID        int
		ayahNumber     int
		publicEligible bool
		currentSource  bool
	)
	err = tx.QueryRow(ctx, `
SELECT u.id::text, u.anchor, u.lifecycle, u.position, u.updated_at, u.page_id,
       b.surah_id, b.ayah_number,
       license.effective_license_status = 'permitted' AS public_eligible,
	       s.units_stale_at IS NULL AND CASE b.role
           WHEN 'primary_text' THEN b.source_updated_at = a.updated_at AND u.text = a.text_qpc_hafs
           WHEN 'translation' THEN b.source_updated_at = t.updated_at AND u.text = t.text
           WHEN 'footnote' THEN b.source_updated_at = t.updated_at
           WHEN 'transliteration' THEN b.source_updated_at = x.updated_at AND u.text = x.text
           ELSE FALSE
       END AS current_source
FROM citable_units u
	JOIN quran_citable_unit_bindings b ON b.unit_id = u.id
	JOIN quran_ayahs a ON a.surah_id = b.surah_id AND a.ayah_number = b.ayah_number
	JOIN quran_surahs s ON s.surah_id = b.surah_id
JOIN citable_units_with_effective_license license ON license.id = u.id
LEFT JOIN quran_ayah_translations t
  ON t.source_id = b.translation_source_id
 AND t.surah_id = b.surah_id AND t.ayah_number = b.ayah_number
LEFT JOIN quran_ayah_transliterations x
  ON x.source_id = b.transliteration_source_id
 AND x.surah_id = b.surah_id AND x.ayah_number = b.ayah_number
WHERE u.corpus = 'quran' AND u.anchor = $1`, canonicalAnchor).Scan(
		&root.ID, &root.Anchor, &root.Lifecycle, &root.Position, &root.UpdatedAt,
		&pageID, &surahID, &ayahNumber, &publicEligible, &currentSource,
	)
	if errors.Is(err, pgx.ErrNoRows) ||
		(err == nil && (!publicEligible || (root.Lifecycle == entity.UnitLifecycleActive && !currentSource))) {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo Quran unit root: %w", err)
	}

	root.Corpus = entity.UnitCorpusQuran
	root.PageID = anchorNullableInt(pageID)
	root.PublicEligible = publicEligible
	root.HeadingOrdinal = -1

	walk, err := walkLineageUnits(ctx, tx, []lineageUnit{root}, lineagePolicy{})
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo Quran unit lineage: %w", err)
	}

	result := entity.AnchorLookupResult{
		CanonicalAnchor: new(root.Anchor),
		Status:          root.Lifecycle,
		RedirectChain:   walk.Redirects,
		CycleDetected:   walk.CycleDetected,
	}
	if len(walk.ActiveUnits) == 0 {
		return result, nil
	}

	activeIDs := make([]string, 0, len(walk.ActiveUnits))
	for i := range walk.ActiveUnits {
		activeIDs = append(activeIDs, walk.ActiveUnits[i].ID)
	}
	rows, err := tx.Query(ctx, `
SELECT u.id::text, u.anchor, u.page_id, u.updated_at, b.surah_id, b.ayah_number
FROM citable_units u
	JOIN quran_citable_unit_bindings b ON b.unit_id = u.id
	JOIN quran_ayahs a ON a.surah_id = b.surah_id AND a.ayah_number = b.ayah_number
	JOIN quran_surahs s ON s.surah_id = b.surah_id AND s.units_stale_at IS NULL
JOIN citable_units_with_effective_license license
  ON license.id = u.id AND license.effective_license_status = 'permitted'
LEFT JOIN quran_ayah_translations t
  ON t.source_id = b.translation_source_id
 AND t.surah_id = b.surah_id AND t.ayah_number = b.ayah_number
LEFT JOIN quran_ayah_transliterations x
  ON x.source_id = b.transliteration_source_id
 AND x.surah_id = b.surah_id AND x.ayah_number = b.ayah_number
WHERE u.id = ANY($1::uuid[]) AND u.lifecycle = 'active'
  AND b.surah_id = $2 AND b.ayah_number = $3
  AND CASE b.role
      WHEN 'primary_text' THEN b.source_updated_at = a.updated_at AND u.text = a.text_qpc_hafs
      WHEN 'translation' THEN b.source_updated_at = t.updated_at AND u.text = t.text
      WHEN 'footnote' THEN b.source_updated_at = t.updated_at
      WHEN 'transliteration' THEN b.source_updated_at = x.updated_at AND u.text = x.text
      ELSE FALSE
  END`, activeIDs, surahID, ayahNumber)
	if err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo Quran unit active targets: %w", err)
	}
	defer rows.Close()

	byID := make(map[string]entity.AnchorRecord, len(activeIDs))
	for rows.Next() {
		var (
			unitID         string
			anchor         string
			unitPageID     sql.NullInt64
			updatedAt      sql.NullTime
			unitSurahID    int
			unitAyahNumber int
		)
		if err := rows.Scan(&unitID, &anchor, &unitPageID, &updatedAt,
			&unitSurahID, &unitAyahNumber); err != nil {
			return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo Quran unit active scan: %w", err)
		}
		byID[unitID] = quranUnitAnchorRecord(
			unitID, anchor, unitSurahID, unitAyahNumber, anchorNullableInt(unitPageID), updatedAt,
		)
	}
	if err := rows.Err(); err != nil {
		return entity.AnchorLookupResult{}, fmt.Errorf("AnchorRepo Quran unit active rows: %w", err)
	}
	for i := range walk.ActiveUnits {
		if record, exists := byID[walk.ActiveUnits[i].ID]; exists {
			result.ActiveRecords = append(result.ActiveRecords, record)
		}
	}
	if root.Lifecycle == entity.UnitLifecycleActive && len(result.ActiveRecords) == 0 {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	return result, nil
}

func quranUnitAnchorRecord(
	unitID, canonicalAnchor string,
	surahID, ayahNumber int,
	pageID *int,
	updatedAt sql.NullTime,
) entity.AnchorRecord {
	ayahKey := fmt.Sprintf("%d:%d", surahID, ayahNumber)

	return entity.AnchorRecord{
		TargetType:      entity.AnchorTargetCitableUnit,
		Corpus:          entity.UnitCorpusQuran,
		CanonicalAnchor: new(canonicalAnchor),
		UnitID:          new(unitID),
		PageID:          pageID,
		SurahID:         new(surahID),
		AyahKey:         new(ayahKey),
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt.Time,
	}
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
	bookID := 0
	if unit.BookID != nil {
		bookID = *unit.BookID
	}

	return lineageUnit{
		ID:        unit.ID,
		Anchor:    unit.Anchor,
		Corpus:    unit.Corpus,
		BookID:    bookID,
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
