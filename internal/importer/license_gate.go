package importer

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const licensePublishConstraint = "book_license_publish_permitted_check"

// mapLicenseGateError turns the database's last-line race guard into the same
// domain error returned by importer preflight checks. Operators therefore see
// a stable, useful failure instead of an opaque PostgreSQL constraint error.
func mapLicenseGateError(err error) error {
	if err == nil || errors.Is(err, entity.ErrLicenseNotPermitted) {
		return err
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.ConstraintName == licensePublishConstraint {
		return fmt.Errorf("%s: %w", pgErr.Message, entity.ErrLicenseNotPermitted)
	}

	return err
}

// ensureMaterialBookImportPermitted runs after the staged diff is known but
// before any source row is written. Locking the Edition row serializes this
// decision with license changes; migration triggers remain the final guard for
// callers that bypass this importer.
func ensureMaterialBookImportPermitted(
	ctx context.Context,
	tx pgx.Tx,
	bookID int,
	materialChange bool,
) error {
	if !materialChange {
		return nil
	}

	var (
		licenseStatus string
		published     bool
	)

	err := tx.QueryRow(ctx, `
SELECT b.license_status,
       EXISTS (
           SELECT 1
           FROM book_publications bp
           WHERE bp.book_id = b.id
             AND bp.status = 'published'
       )
FROM books b
WHERE b.id = $1
FOR UPDATE`, bookID).Scan(&licenseStatus, &published)
	if err != nil {
		return fmt.Errorf("check book %d license before source import: %w", bookID, err)
	}

	// restricted publications are excluded from public reads. Their hidden
	// source may still be prepared, but it cannot become public again until the
	// Edition is explicitly audited as permitted.
	if published && licenseStatus != entity.LicenseStatusPermitted && licenseStatus != entity.LicenseStatusRestricted {
		return fmt.Errorf(
			"source import would change public book %d with license_status=%s: %w",
			bookID,
			licenseStatus,
			entity.ErrLicenseNotPermitted,
		)
	}

	return nil
}

// ensureBookMetadataImportsPermitted compares the incoming Shamela master
// snapshot with only the grandfathered public Editions that need protection.
// This is one set query rather than one query per source book; the migration's
// row trigger repeats the decision atomically at write time to close races.
//
//nolint:cyclop,funlen,gocognit,gocyclo,wsl_v5 // Linear load/classify/gate stages mirror the three master UPSERT batches.
func ensureBookMetadataImportsPermitted(
	ctx context.Context,
	tx pgx.Tx,
	incoming []masterBook,
) error {
	byID := make(map[int]*masterBook, len(incoming))
	ids := make([]int, 0, len(incoming))
	for i := range incoming {
		byID[incoming[i].ID] = &incoming[i]
		ids = append(ids, incoming[i].ID)
	}
	sort.Ints(ids)

	rows, err := tx.Query(ctx, `
SELECT b.id,
       b.name,
       b.is_deleted,
       b.category_id::text,
       b.type::text,
       b.source_date,
       b.author_id::text,
       b.printed::text,
       b.minor_release::text,
       b.major_release::text,
       b.bibliography,
       b.hint,
       coalesce(b.pdf_links::text, ''),
       coalesce(b.metadata::text, '')
FROM books b
WHERE b.id = ANY($1::INTEGER[])
ORDER BY b.id`, ids)
	if err != nil {
		return fmt.Errorf("load current book metadata: %w", err)
	}
	defer rows.Close()

	current := make(map[int]masterBook, len(incoming))

	for rows.Next() {
		book, scanErr := scanProtectedBookMetadata(rows)
		if scanErr != nil {
			return scanErr
		}

		current[book.ID] = book
	}

	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate current book metadata: %w", err)
	}

	protectedRows, err := tx.Query(ctx, `
SELECT b.id
FROM books b
JOIN book_publications p
  ON p.book_id = b.id
 AND p.status = 'published'
 AND p.license_grandfathered_at IS NOT NULL
WHERE b.id = ANY($1::INTEGER[])
  AND b.is_deleted = FALSE
  AND b.license_status NOT IN ('permitted', 'restricted')`, ids)
	if err != nil {
		return fmt.Errorf("load protected public book ids: %w", err)
	}
	defer protectedRows.Close()

	protected := make(map[int]bool)
	for protectedRows.Next() {
		var id int
		if err = protectedRows.Scan(&id); err != nil {
			return fmt.Errorf("scan protected public book id: %w", err)
		}
		protected[id] = true
	}
	if err = protectedRows.Err(); err != nil {
		return fmt.Errorf("iterate protected public book ids: %w", err)
	}

	for _, id := range ids {
		stored, exists := current[id]
		if !exists {
			continue
		}

		candidate := byID[id]
		if sameMasterBookMetadata(&stored, candidate) {
			candidate.skipWrite = true

			continue
		}

		if protected[id] {
			return fmt.Errorf(
				"metadata import would change public book %d with a non-permitted license: %w",
				id,
				entity.ErrLicenseNotPermitted,
			)
		}
	}

	return nil
}

// ensureMasterMetadataImportsPermitted completes every publication preflight
// before importMasterMetadata queues its first PostgreSQL write. It covers both
// Edition-owned book fields and the shared Arabic author/category records that
// fan out into public catalog responses.
func ensureMasterMetadataImportsPermitted(
	ctx context.Context,
	tx pgx.Tx,
	categories []masterCategory,
	authors []masterAuthor,
	books []masterBook,
) error {
	changedCategoryIDs, err := changedMasterCategoryIDs(ctx, tx, categories)
	if err != nil {
		return err
	}

	changedAuthorIDs, err := changedMasterAuthorIDs(ctx, tx, authors)
	if err != nil {
		return err
	}

	if err := lockMasterMetadataBooks(ctx, tx, changedCategoryIDs, changedAuthorIDs, books); err != nil {
		return err
	}

	if err := ensureSharedMasterOwnersPermitted(ctx, tx, changedCategoryIDs, changedAuthorIDs); err != nil {
		return err
	}

	return ensureBookMetadataImportsPermitted(ctx, tx, books)
}

// lockMasterMetadataBooks takes every Edition lock needed by this master run
// in one canonical id order: incoming book rows plus the public fanout of any
// changed shared author/category. Later preflights are read-only under these
// locks, so license updates cannot race between approval and commit.
//
//nolint:wsl_v5 // Lock id collection and the single ordered lock query intentionally stay adjacent.
func lockMasterMetadataBooks(
	ctx context.Context,
	tx pgx.Tx,
	changedCategoryIDs, changedAuthorIDs []int,
	books []masterBook,
) error {
	bookIDs := make([]int, 0, len(books))
	for i := range books {
		bookIDs = append(bookIDs, books[i].ID)
	}
	sort.Ints(bookIDs)

	rows, err := tx.Query(ctx, `
SELECT b.id
FROM books b
LEFT JOIN book_publications p
  ON p.book_id = b.id
LEFT JOIN book_metadata_edits me
  ON me.book_id = b.id
 AND me.status = 'published'
WHERE b.id = ANY($1::INTEGER[])
   OR (
       b.is_deleted = FALSE
       AND p.status = 'published'
       AND (
           b.license_status = 'permitted'
           OR (
               p.license_grandfathered_at IS NOT NULL
               AND b.license_status <> 'restricted'
           )
       )
       AND (
           b.author_id = ANY($3::INTEGER[])
           OR COALESCE(me.category_id, b.category_id) = ANY($2::INTEGER[])
       )
   )
ORDER BY b.id
FOR UPDATE OF b`, bookIDs, changedCategoryIDs, changedAuthorIDs)
	if err != nil {
		return fmt.Errorf("lock books for master metadata preflight: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var ignored int
		if err = rows.Scan(&ignored); err != nil {
			return fmt.Errorf("scan locked master metadata book: %w", err)
		}
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate locked master metadata books: %w", err)
	}

	return nil
}

//nolint:wsl_v5 // Snapshot comparison is intentionally kept as one auditable pipeline.
func changedMasterCategoryIDs(
	ctx context.Context,
	tx pgx.Tx,
	incoming []masterCategory,
) ([]int, error) {
	byID := make(map[int]*masterCategory, len(incoming))
	ids := make([]int, 0, len(incoming))
	for i := range incoming {
		byID[incoming[i].ID] = &incoming[i]
		ids = append(ids, incoming[i].ID)
	}
	sort.Ints(ids)

	rows, err := tx.Query(ctx, `
SELECT id, name, display_order, is_deleted
FROM categories
WHERE id = ANY($1::INTEGER[])
ORDER BY id
FOR UPDATE`, ids)
	if err != nil {
		return nil, fmt.Errorf("load current master categories: %w", err)
	}
	defer rows.Close()

	current := make(map[int]masterCategory, len(incoming))
	for rows.Next() {
		var category masterCategory
		if err = rows.Scan(&category.ID, &category.Name, &category.DisplayOrder, &category.IsDeleted); err != nil {
			return nil, fmt.Errorf("scan current master category: %w", err)
		}
		current[category.ID] = category
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current master categories: %w", err)
	}

	changed := make([]int, 0)
	for _, id := range ids {
		stored, exists := current[id]
		if !exists || !sameMasterCategory(stored, *byID[id]) {
			changed = append(changed, id)
		} else {
			byID[id].skipWrite = true
		}
	}

	return changed, nil
}

//nolint:wsl_v5 // Snapshot comparison is intentionally kept as one auditable pipeline.
func changedMasterAuthorIDs(
	ctx context.Context,
	tx pgx.Tx,
	incoming []masterAuthor,
) ([]int, error) {
	byID := make(map[int]*masterAuthor, len(incoming))
	ids := make([]int, 0, len(incoming))
	for i := range incoming {
		byID[incoming[i].ID] = &incoming[i]
		ids = append(ids, incoming[i].ID)
	}
	sort.Ints(ids)

	rows, err := tx.Query(ctx, `
SELECT id, name, biography, death_text, death_number, is_deleted,
       name_search, name_search_normalization_version
FROM authors
WHERE id = ANY($1::INTEGER[])
ORDER BY id
FOR UPDATE`, ids)
	if err != nil {
		return nil, fmt.Errorf("load current master authors: %w", err)
	}
	defer rows.Close()

	current := make(map[int]masterAuthor, len(incoming))
	for rows.Next() {
		var author masterAuthor
		if err = rows.Scan(
			&author.ID,
			&author.Name,
			&author.Biography,
			&author.DeathText,
			&author.DeathNumber,
			&author.IsDeleted,
			&author.NameSearch,
			&author.NameSearchNormalizationVersion,
		); err != nil {
			return nil, fmt.Errorf("scan current master author: %w", err)
		}
		current[author.ID] = author
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current master authors: %w", err)
	}

	changed := make([]int, 0)
	for _, id := range ids {
		stored, exists := current[id]
		if !exists || !sameMasterAuthor(&stored, byID[id]) {
			changed = append(changed, id)
		} else {
			byID[id].skipWrite = true
		}
	}

	return changed, nil
}

//nolint:funlen,wsl_v5 // One fanout query plus one clear operator-facing rejection contract.
func ensureSharedMasterOwnersPermitted(
	ctx context.Context,
	tx pgx.Tx,
	changedCategoryIDs, changedAuthorIDs []int,
) error {
	if len(changedCategoryIDs) == 0 && len(changedAuthorIDs) == 0 {
		return nil
	}

	var (
		blockedBookID int
		licenseStatus string
		ownerKind     string
		ownerID       int
	)

	rows, err := tx.Query(ctx, `
SELECT b.id,
       b.license_status,
       CASE
           WHEN b.author_id = ANY($2::INTEGER[]) THEN 'author'
           ELSE 'category'
       END AS owner_kind,
       CASE
           WHEN b.author_id = ANY($2::INTEGER[]) THEN b.author_id
           ELSE COALESCE(me.category_id, b.category_id)
       END AS owner_id
FROM books b
JOIN book_publications p
  ON p.book_id = b.id
 AND p.status = 'published'
LEFT JOIN book_metadata_edits me
  ON me.book_id = b.id
 AND me.status = 'published'
WHERE b.is_deleted = FALSE
  AND (
      b.license_status = 'permitted'
      OR (
          p.license_grandfathered_at IS NOT NULL
          AND b.license_status <> 'restricted'
      )
  )
  AND (
      b.author_id = ANY($2::INTEGER[])
      OR COALESCE(me.category_id, b.category_id) = ANY($1::INTEGER[])
  )
ORDER BY b.id`, changedCategoryIDs, changedAuthorIDs)
	if err != nil {
		return fmt.Errorf("check shared master metadata license fanout: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		if err = rows.Scan(&blockedBookID, &licenseStatus, &ownerKind, &ownerID); err != nil {
			return fmt.Errorf("scan shared master metadata license fanout: %w", err)
		}
		if licenseStatus == entity.LicenseStatusPermitted {
			continue
		}

		return fmt.Errorf(
			"master %s %d would change public book %d with license_status=%s: %w",
			ownerKind,
			ownerID,
			blockedBookID,
			licenseStatus,
			entity.ErrLicenseNotPermitted,
		)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate shared master metadata license fanout: %w", err)
	}

	return nil
}

func sameMasterCategory(first, second masterCategory) bool {
	return first.ID == second.ID &&
		first.Name == second.Name &&
		optionalIntEqual(first.DisplayOrder, second.DisplayOrder) &&
		first.IsDeleted == second.IsDeleted
}

func sameMasterAuthor(first, second *masterAuthor) bool {
	return first.ID == second.ID &&
		first.Name == second.Name &&
		optionalStringEqual(first.Biography, second.Biography) &&
		optionalStringEqual(first.DeathText, second.DeathText) &&
		optionalIntEqual(first.DeathNumber, second.DeathNumber) &&
		first.IsDeleted == second.IsDeleted &&
		first.NameSearch == second.NameSearch &&
		first.NameSearchNormalizationVersion == second.NameSearchNormalizationVersion
}

type protectedBookMetadataScanner interface {
	Scan(...any) error
}

func scanProtectedBookMetadata(row protectedBookMetadataScanner) (masterBook, error) {
	var (
		book         masterBook
		category     stdsql.NullString
		bookType     stdsql.NullString
		sourceDate   stdsql.NullString
		author       stdsql.NullString
		printed      stdsql.NullString
		minorRelease stdsql.NullString
		majorRelease stdsql.NullString
		bibliography stdsql.NullString
		hint         stdsql.NullString
	)

	err := row.Scan(
		&book.ID,
		&book.Name,
		&book.IsDeleted,
		&category,
		&bookType,
		&sourceDate,
		&author,
		&printed,
		&minorRelease,
		&majorRelease,
		&bibliography,
		&hint,
		&book.PDFLinks,
		&book.Metadata,
	)
	if err != nil {
		return masterBook{}, fmt.Errorf("scan protected public book metadata: %w", err)
	}

	book.CategoryID = nullStringToInt(category)
	book.Type = nullStringToInt(bookType)
	book.SourceDate = nullStringPtr(sourceDate)
	book.AuthorID = nullStringToInt(author)
	book.Printed = nullStringToInt(printed)
	book.MinorRelease = nullStringToInt(minorRelease)
	book.MajorRelease = nullStringToInt(majorRelease)
	book.Bibliography = nullStringPtr(bibliography)
	book.Hint = nullStringPtr(hint)

	return book, nil
}

//nolint:cyclop,gocyclo // Flat field-by-field equality keeps the public metadata contract auditable.
func sameMasterBookMetadata(first, second *masterBook) bool {
	return first.Name == second.Name &&
		first.IsDeleted == second.IsDeleted &&
		optionalIntEqual(first.CategoryID, second.CategoryID) &&
		optionalIntEqual(first.Type, second.Type) &&
		optionalStringEqual(first.SourceDate, second.SourceDate) &&
		optionalIntEqual(first.AuthorID, second.AuthorID) &&
		optionalIntEqual(first.Printed, second.Printed) &&
		optionalIntEqual(first.MinorRelease, second.MinorRelease) &&
		optionalIntEqual(first.MajorRelease, second.MajorRelease) &&
		optionalStringEqual(first.Bibliography, second.Bibliography) &&
		optionalStringEqual(first.Hint, second.Hint) &&
		jsonEqual(first.PDFLinks, second.PDFLinks) &&
		jsonEqual(first.Metadata, second.Metadata)
}

func optionalIntEqual(first, second *int) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}

	return *first == *second
}

func optionalStringEqual(first, second *string) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}

	return *first == *second
}

type assetBookVisibility struct {
	licenseStatus string
	published     bool
}

// ensureVisibleAssetWritesPermitted closes the direct JSONL-import bypass.
// Hidden candidate assets remain importable; an exact no-op remains allowed.
// A changed asset that readers would see immediately requires literal
// license_status=permitted on its Edition.
func ensureVisibleAssetWritesPermitted(ctx context.Context, tx pgx.Tx, assets []ReaderAsset) error {
	books, sharedFanout, err := lockAssetBooks(ctx, tx, assets)
	if err != nil {
		return err
	}

	for i := range assets {
		asset := &assets[i]
		if isSharedAsset(asset) {
			if err := ensureSharedVisibleAssetWritePermitted(
				ctx,
				tx,
				asset,
				books,
				sharedFanout[i],
			); err != nil {
				return err
			}

			continue
		}

		if err := ensureVisibleAssetWritePermitted(ctx, tx, asset, books[asset.BookID]); err != nil {
			return err
		}
	}

	return nil
}

//nolint:gocognit,gocritic,wsl_v5 // Fanout discovery and canonical locking form one race-free preflight stage.
func lockAssetBooks(
	ctx context.Context,
	tx pgx.Tx,
	assets []ReaderAsset,
) (map[int]assetBookVisibility, map[int][]int, error) {
	books := make(map[int]assetBookVisibility)
	bookIDs := make([]int, 0)
	seen := make(map[int]bool)
	sharedFanout := make(map[int][]int)

	for i := range assets {
		asset := &assets[i]
		if isSharedAsset(asset) {
			impacted, err := sharedAssetImpactedBookIDs(ctx, tx, asset)
			if err != nil {
				return nil, nil, fmt.Errorf("line %d: determine shared asset fanout: %w", asset.lineNumber, err)
			}
			sharedFanout[i] = impacted
			for _, bookID := range impacted {
				if !seen[bookID] {
					seen[bookID] = true
					bookIDs = append(bookIDs, bookID)
				}
			}

			continue
		}

		bookID := asset.BookID

		if bookID > 0 && !seen[bookID] {
			seen[bookID] = true
			bookIDs = append(bookIDs, bookID)
		}
	}

	sort.Ints(bookIDs)

	// Take every Edition lock in canonical order before comparing any asset.
	// This keeps concurrent multi-book imports from deadlocking each other.
	for _, bookID := range bookIDs {
		visibility, err := lockAssetBookVisibility(ctx, tx, bookID)
		if err != nil {
			return nil, nil, err
		}

		books[bookID] = visibility
	}

	return books, sharedFanout, nil
}

func isSharedAsset(asset *ReaderAsset) bool {
	return asset.Kind == assetKindAuthorTranslation || asset.Kind == assetKindCategoryTranslation
}

//nolint:wsl_v5 // SQL row collection is intentionally compact.
func sharedAssetImpactedBookIDs(ctx context.Context, tx pgx.Tx, asset *ReaderAsset) ([]int, error) {
	rows, err := tx.Query(ctx, `
SELECT b.id
FROM books b
JOIN public_book_publications p ON p.book_id = b.id
JOIN book_production_projects pp
  ON pp.book_id = b.id
 AND pp.lang = $3
 AND pp.publication_status = 'published'
 AND pp.workflow_status <> 'archived'
LEFT JOIN book_metadata_edits me
  ON me.book_id = b.id
 AND me.status = 'published'
WHERE ($1::TEXT = 'author' AND b.author_id = $2)
   OR ($1::TEXT = 'category' AND COALESCE(me.category_id, b.category_id) = $2)
ORDER BY b.id`, sharedAssetOwnerKind(asset), sharedAssetOwnerID(asset), asset.Lang)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bookIDs := make([]int, 0)
	for rows.Next() {
		var bookID int
		if err := rows.Scan(&bookID); err != nil {
			return nil, err
		}
		bookIDs = append(bookIDs, bookID)
	}

	return bookIDs, rows.Err()
}

func sharedAssetOwnerKind(asset *ReaderAsset) string {
	if asset.Kind == assetKindAuthorTranslation {
		return "author"
	}

	return "category"
}

func sharedAssetOwnerID(asset *ReaderAsset) int {
	if asset.Kind == assetKindAuthorTranslation {
		return asset.AuthorID
	}

	return asset.CategoryID
}

//nolint:wsl_v5 // Fanout classification and no-op handling are intentionally adjacent.
func ensureSharedVisibleAssetWritePermitted(
	ctx context.Context,
	tx pgx.Tx,
	asset *ReaderAsset,
	books map[int]assetBookVisibility,
	impactedBookIDs []int,
) error {
	blockedBookID := 0
	blockedStatus := ""
	for _, bookID := range impactedBookIDs {
		visibility := books[bookID]
		if visibility.licenseStatus != entity.LicenseStatusPermitted {
			blockedBookID = bookID
			blockedStatus = visibility.licenseStatus

			break
		}
	}

	if blockedBookID == 0 {
		return nil
	}

	changed, err := assetMateriallyChanged(ctx, tx, asset)
	if err != nil {
		return fmt.Errorf("line %d: compare existing shared asset: %w", asset.lineNumber, err)
	}
	if !changed {
		// Avoid even attempting the INSERT ... ON CONFLICT path: PostgreSQL
		// BEFORE INSERT guards run before conflict resolution and would otherwise
		// reject a genuine no-op for a grandfathered public Edition.
		asset.skipWrite = true

		return nil
	}

	return fmt.Errorf(
		"line %d: shared %s %d asset import would change public book %d with license_status=%s: %w",
		asset.lineNumber,
		sharedAssetOwnerKind(asset),
		sharedAssetOwnerID(asset),
		blockedBookID,
		blockedStatus,
		entity.ErrLicenseNotPermitted,
	)
}

func ensureVisibleAssetWritePermitted(
	ctx context.Context,
	tx pgx.Tx,
	asset *ReaderAsset,
	visibility assetBookVisibility,
) error {
	if !visibility.published || visibility.licenseStatus == entity.LicenseStatusPermitted {
		return nil
	}

	visible, err := assetIsImmediatelyVisible(ctx, tx, asset)
	if err != nil {
		return fmt.Errorf("line %d: determine asset visibility: %w", asset.lineNumber, err)
	}

	if !visible {
		return nil
	}

	changed, err := assetMateriallyChanged(ctx, tx, asset)
	if err != nil {
		return fmt.Errorf("line %d: compare existing asset: %w", asset.lineNumber, err)
	}

	if !changed {
		asset.skipWrite = true

		return nil
	}

	return fmt.Errorf(
		"line %d: asset import would change public book %d with license_status=%s: %w",
		asset.lineNumber,
		asset.BookID,
		visibility.licenseStatus,
		entity.ErrLicenseNotPermitted,
	)
}

func lockAssetBookVisibility(ctx context.Context, tx pgx.Tx, bookID int) (assetBookVisibility, error) {
	var visibility assetBookVisibility

	err := tx.QueryRow(ctx, `
SELECT b.license_status,
       EXISTS (
           SELECT 1
           FROM public_book_publications p
           WHERE p.book_id = b.id
       )
FROM books b
WHERE b.id = $1
FOR UPDATE`, bookID).Scan(&visibility.licenseStatus, &visibility.published)
	if err != nil {
		return assetBookVisibility{}, fmt.Errorf("check book %d license before asset import: %w", bookID, err)
	}

	return visibility, nil
}

func assetIsImmediatelyVisible(ctx context.Context, tx pgx.Tx, asset *ReaderAsset) (bool, error) {
	// Arabic reader assets are source-language companions and are read without
	// a translated production project. Shared author/category translations are
	// globally readable; book_id, when supplied, is their project/Edition gate.
	if asset.Lang == "ar" || asset.Kind == assetKindAuthorTranslation || asset.Kind == assetKindCategoryTranslation {
		return true, nil
	}

	var published bool

	err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM book_production_projects p
    WHERE p.book_id = $1
      AND p.lang = $2
      AND p.publication_status = 'published'
      AND p.workflow_status <> 'archived'
)`, asset.BookID, asset.Lang).Scan(&published)
	if err != nil {
		return false, err
	}

	return published, nil
}

// resolveAssetReviewTimestamps makes the reviewed_at default deterministic for
// both the preflight comparison and the eventual UPSERT. On an exact re-import
// with no explicit timestamp, the stored review time is retained; genuinely
// new or otherwise changed reviewed content receives one shared import time.
func resolveAssetReviewTimestamps(ctx context.Context, tx pgx.Tx, assets []ReaderAsset) error {
	importedAt := time.Now().UTC()

	for i := range assets {
		asset := &assets[i]
		if !assetNeedsResolvedReviewTimestamp(asset) {
			continue
		}

		existingAt, fieldsMatch, err := matchingExistingAssetReview(ctx, tx, asset)
		if err != nil {
			return fmt.Errorf("line %d: resolve reviewed_at default: %w", asset.lineNumber, err)
		}

		resolvedAt := &importedAt
		if fieldsMatch && existingAt != nil {
			resolvedAt = existingAt
		}

		if asset.Kind == assetKindHeadingSummary {
			asset.SummaryReviewedAt = resolvedAt
		} else {
			asset.ReviewedAt = resolvedAt
		}
	}

	return nil
}

func assetNeedsResolvedReviewTimestamp(asset *ReaderAsset) bool {
	if asset.Kind == assetKindAudio {
		return false
	}

	if asset.Kind == assetKindHeadingSummary {
		return normalizeSummaryStatus(asset.SummaryStatus, asset.Status) == reviewedAssetStatus &&
			firstNonNilTime(asset.SummaryReviewedAt, asset.ReviewedAt) == nil
	}

	return normalizeTranslationStatus(asset.Status) == reviewedAssetStatus && asset.ReviewedAt == nil
}

//nolint:funlen,cyclop,wsl_v5 // Field lists intentionally mirror the auditable UPSERT contract for each asset kind.
func matchingExistingAssetReview(
	ctx context.Context,
	tx pgx.Tx,
	asset *ReaderAsset,
) (*time.Time, bool, error) {
	metadata := ""
	if len(asset.Metadata) > 0 {
		metadata = string(asset.Metadata)
	}

	var (
		reviewedAt  stdsql.NullTime
		fieldsMatch bool
		err         error
	)

	switch asset.Kind {
	case assetKindTranslation:
		status := normalizeTranslationStatus(asset.Status)
		err = tx.QueryRow(
			ctx, `
SELECT reviewed_at,
       ROW(title, content, source, translation_status, reviewed_by, metadata,
           provenance_class, generation_run_id::text)
       IS NOT DISTINCT FROM
       ROW($4, $5, $6, $7, $8, nullif($9, '')::jsonb, 'machine', $10)
FROM section_translations
WHERE book_id = $1 AND heading_id = $2 AND lang = $3`,
			asset.BookID, asset.HeadingID, asset.Lang, asset.Title, asset.Content, asset.Source,
			status, asset.ReviewedBy, metadata, asset.Generation.RunID,
		).Scan(&reviewedAt, &fieldsMatch)

	case assetKindHeadingSummary:
		status := normalizeSummaryStatus(asset.SummaryStatus, asset.Status)
		reviewedBy := firstNonBlankPtr(asset.SummaryReviewedBy, asset.ReviewedBy)
		err = tx.QueryRow(
			ctx, `
SELECT reviewed_at,
       ROW(summary, source, summary_status, reviewed_by, metadata,
           provenance_class, generation_run_id::text)
       IS NOT DISTINCT FROM
       ROW($4, $5, $6, $7, nullif($8, '')::jsonb, 'machine', $9)
FROM book_heading_summaries
WHERE book_id = $1 AND heading_id = $2 AND lang = $3`,
			asset.BookID, asset.HeadingID, asset.Lang, asset.Summary, asset.Source,
			status, reviewedBy, metadata, asset.Generation.RunID,
		).Scan(&reviewedAt, &fieldsMatch)

	case assetKindBookMetadataTranslation:
		displayTitle := firstNonBlankPtr(asset.DisplayTitle, asset.Title, asset.Name)
		status := normalizeTranslationStatus(asset.Status)
		err = tx.QueryRow(
			ctx, `
SELECT reviewed_at,
       ROW(display_title, bibliography, hint, description, source,
           translation_status, reviewed_by, metadata, provenance_class,
           generation_run_id::text)
       IS NOT DISTINCT FROM
       ROW($3, $4, $5, $6, $7, $8, $9, nullif($10, '')::jsonb, 'machine', $11)
FROM book_metadata_translations
WHERE book_id = $1 AND lang = $2`,
			asset.BookID, asset.Lang, displayTitle, asset.Bibliography, asset.Hint,
			asset.Description, asset.Source, status, asset.ReviewedBy, metadata,
			asset.Generation.RunID,
		).Scan(&reviewedAt, &fieldsMatch)

	case assetKindAuthorTranslation:
		status := normalizeTranslationStatus(asset.Status)
		err = tx.QueryRow(
			ctx, `
SELECT reviewed_at,
       ROW(name, biography, death_text, source, translation_status, reviewed_by,
           metadata, provenance_class, generation_run_id::text)
       IS NOT DISTINCT FROM
       ROW($3, $4, $5, $6, $7, $8, nullif($9, '')::jsonb, 'machine', $10)
FROM author_translations
WHERE author_id = $1 AND lang = $2`,
			asset.AuthorID, asset.Lang, asset.Name, asset.Biography, asset.DeathText,
			asset.Source, status, asset.ReviewedBy, metadata, asset.Generation.RunID,
		).Scan(&reviewedAt, &fieldsMatch)

	case assetKindCategoryTranslation:
		status := normalizeTranslationStatus(asset.Status)
		err = tx.QueryRow(
			ctx, `
SELECT reviewed_at,
       ROW(name, source, translation_status, reviewed_by, metadata,
           provenance_class, generation_run_id::text)
       IS NOT DISTINCT FROM
       ROW($3, $4, $5, $6, nullif($7, '')::jsonb, 'machine', $8)
FROM category_translations
WHERE category_id = $1 AND lang = $2`,
			asset.CategoryID, asset.Lang, asset.Name, asset.Source, status,
			asset.ReviewedBy, metadata, asset.Generation.RunID,
		).Scan(&reviewedAt, &fieldsMatch)

	default:
		return nil, false, fmt.Errorf("unsupported reviewed asset kind %q", asset.Kind)
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !reviewedAt.Valid {
		return nil, fieldsMatch, nil
	}

	return &reviewedAt.Time, fieldsMatch, nil
}

//nolint:funlen // Each case mirrors one auditable UPSERT in assets.go.
func assetMateriallyChanged(ctx context.Context, tx pgx.Tx, asset *ReaderAsset) (bool, error) {
	metadata := ""
	if len(asset.Metadata) > 0 {
		metadata = string(asset.Metadata)
	}

	var changed bool

	switch asset.Kind {
	case assetKindTranslation:
		status := normalizeTranslationStatus(asset.Status)
		reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
		err := tx.QueryRow(ctx, `
SELECT NOT EXISTS (
    SELECT 1
    FROM section_translations
    WHERE book_id = $1 AND heading_id = $2 AND lang = $3
      AND title IS NOT DISTINCT FROM $4
      AND content IS NOT DISTINCT FROM $5
      AND source IS NOT DISTINCT FROM $6
      AND translation_status IS NOT DISTINCT FROM $7
      AND reviewed_by IS NOT DISTINCT FROM $8
      AND reviewed_at IS NOT DISTINCT FROM $9
      AND metadata IS NOT DISTINCT FROM nullif($10, '')::jsonb
      AND provenance_class = 'machine'
      AND generation_run_id::text IS NOT DISTINCT FROM $11
)`, asset.BookID, asset.HeadingID, asset.Lang, asset.Title, asset.Content, asset.Source,
			status, asset.ReviewedBy, reviewedAt, metadata, asset.Generation.RunID).Scan(&changed)

		return changed, err

	case assetKindHeadingSummary:
		status := normalizeSummaryStatus(asset.SummaryStatus, asset.Status)
		reviewedBy := firstNonBlankPtr(asset.SummaryReviewedBy, asset.ReviewedBy)
		reviewedAt := reviewedAtOrNow(status, firstNonNilTime(asset.SummaryReviewedAt, asset.ReviewedAt))
		err := tx.QueryRow(ctx, `
SELECT NOT EXISTS (
    SELECT 1
    FROM book_heading_summaries
    WHERE book_id = $1 AND heading_id = $2 AND lang = $3
      AND summary IS NOT DISTINCT FROM $4
      AND source IS NOT DISTINCT FROM $5
      AND summary_status IS NOT DISTINCT FROM $6
      AND reviewed_by IS NOT DISTINCT FROM $7
      AND reviewed_at IS NOT DISTINCT FROM $8
      AND metadata IS NOT DISTINCT FROM nullif($9, '')::jsonb
      AND provenance_class = 'machine'
      AND generation_run_id::text IS NOT DISTINCT FROM $10
)`, asset.BookID, asset.HeadingID, asset.Lang, asset.Summary, asset.Source,
			status, reviewedBy, reviewedAt, metadata, asset.Generation.RunID).Scan(&changed)

		return changed, err

	case assetKindAudio:
		err := tx.QueryRow(ctx, `
SELECT NOT EXISTS (
    SELECT 1
    FROM section_audio
    WHERE book_id = $1 AND heading_id = $2 AND lang = $3
      AND url IS NOT DISTINCT FROM $4
      AND narrator IS NOT DISTINCT FROM $5
      AND duration_seconds IS NOT DISTINCT FROM $6
      AND mime_type IS NOT DISTINCT FROM $7
      AND metadata IS NOT DISTINCT FROM nullif($8, '')::jsonb
)`, asset.BookID, asset.HeadingID, asset.Lang, asset.URL, asset.Narrator,
			asset.DurationSeconds, asset.MIMEType, metadata).Scan(&changed)

		return changed, err

	case assetKindBookMetadataTranslation:
		displayTitle := firstNonBlankPtr(asset.DisplayTitle, asset.Title, asset.Name)
		status := normalizeTranslationStatus(asset.Status)
		reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
		err := tx.QueryRow(ctx, `
SELECT NOT EXISTS (
    SELECT 1
    FROM book_metadata_translations
    WHERE book_id = $1 AND lang = $2
      AND display_title IS NOT DISTINCT FROM $3
      AND bibliography IS NOT DISTINCT FROM $4
      AND hint IS NOT DISTINCT FROM $5
      AND description IS NOT DISTINCT FROM $6
      AND source IS NOT DISTINCT FROM $7
      AND translation_status IS NOT DISTINCT FROM $8
      AND reviewed_by IS NOT DISTINCT FROM $9
      AND reviewed_at IS NOT DISTINCT FROM $10
      AND metadata IS NOT DISTINCT FROM nullif($11, '')::jsonb
      AND provenance_class = 'machine'
      AND generation_run_id::text IS NOT DISTINCT FROM $12
)`, asset.BookID, asset.Lang, displayTitle, asset.Bibliography, asset.Hint, asset.Description,
			asset.Source, status, asset.ReviewedBy, reviewedAt, metadata, asset.Generation.RunID).Scan(&changed)

		return changed, err

	case assetKindAuthorTranslation:
		status := normalizeTranslationStatus(asset.Status)
		reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
		err := tx.QueryRow(ctx, `
SELECT NOT EXISTS (
    SELECT 1
    FROM author_translations
    WHERE author_id = $1 AND lang = $2
      AND name IS NOT DISTINCT FROM $3
      AND biography IS NOT DISTINCT FROM $4
      AND death_text IS NOT DISTINCT FROM $5
      AND source IS NOT DISTINCT FROM $6
      AND translation_status IS NOT DISTINCT FROM $7
      AND reviewed_by IS NOT DISTINCT FROM $8
      AND reviewed_at IS NOT DISTINCT FROM $9
      AND metadata IS NOT DISTINCT FROM nullif($10, '')::jsonb
      AND provenance_class = 'machine'
      AND generation_run_id::text IS NOT DISTINCT FROM $11
)`, asset.AuthorID, asset.Lang, asset.Name, asset.Biography, asset.DeathText, asset.Source,
			status, asset.ReviewedBy, reviewedAt, metadata, asset.Generation.RunID).Scan(&changed)

		return changed, err

	case assetKindCategoryTranslation:
		status := normalizeTranslationStatus(asset.Status)
		reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
		err := tx.QueryRow(ctx, `
SELECT NOT EXISTS (
    SELECT 1
    FROM category_translations
    WHERE category_id = $1 AND lang = $2
      AND name IS NOT DISTINCT FROM $3
      AND source IS NOT DISTINCT FROM $4
      AND translation_status IS NOT DISTINCT FROM $5
      AND reviewed_by IS NOT DISTINCT FROM $6
      AND reviewed_at IS NOT DISTINCT FROM $7
      AND metadata IS NOT DISTINCT FROM nullif($8, '')::jsonb
      AND provenance_class = 'machine'
      AND generation_run_id::text IS NOT DISTINCT FROM $9
)`, asset.CategoryID, asset.Lang, asset.Name, asset.Source, status,
			asset.ReviewedBy, reviewedAt, metadata, asset.Generation.RunID).Scan(&changed)

		return changed, err
	}

	return false, fmt.Errorf("unsupported asset kind %q", asset.Kind)
}
