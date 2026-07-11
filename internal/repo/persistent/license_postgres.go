package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/jackc/pgx/v5"
)

const bookLicenseSelect = `
SELECT b.id,
       COALESCE(me.display_title, b.name),
       b.license_status,
       b.license_reason,
       b.license_evidence_url,
       b.license_updated_by::text,
       b.license_updated_at,
       p.status,
       (
           public_p.book_id IS NOT NULL
           AND
           b.license_status NOT IN ('permitted', 'restricted')
           AND (
               (p.status = 'published' AND p.license_grandfathered_at IS NOT NULL)
               OR COALESCE(pg.active, false)
           )
       ) AS grandfathered,
       CASE WHEN public_p.book_id IS NOT NULL THEN
           GREATEST(
               CASE WHEN p.status = 'published' THEN p.license_grandfathered_at END,
               pg.grandfathered_at
           )
       END AS grandfathered_at
FROM books b
LEFT JOIN book_metadata_edits me
       ON me.book_id = b.id AND me.status = 'published'
LEFT JOIN book_publications p ON p.book_id = b.id
LEFT JOIN public_book_publications public_p ON public_p.book_id = b.id
LEFT JOIN LATERAL (
    SELECT bool_or(
               project.publication_status = 'published'
               AND project.license_grandfathered_at IS NOT NULL
           ) AS active,
           max(project.license_grandfathered_at) FILTER (
               WHERE project.publication_status = 'published'
           ) AS grandfathered_at
    FROM book_production_projects project
    WHERE project.book_id = b.id
) pg ON true
WHERE b.id = $1 AND b.is_deleted = false`

const licenseAuditFilterSQL = `
WHERE is_deleted = false
  AND ($1 = 'all'
       OR ($1 = 'unresolved' AND license_status IN ('unknown', 'needs_review'))
       OR license_status = $1)`

// ListBookLicenseAudit returns page, filtered total, and whole-corpus coverage
// from one repeatable-read snapshot. The queue uses only registered reader
// signals that exist in this backend; anonymous traffic is not fabricated.
func (r *EditorialRepo) ListBookLicenseAudit(
	ctx context.Context,
	filter repo.LicenseAuditFilter,
) ([]entity.BookLicenseAuditItem, int, entity.BookLicenseAuditCounts, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, entity.BookLicenseAuditCounts{}, fmt.Errorf("EditorialRepo.ListBookLicenseAudit begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	counts, err := queryBookLicenseCounts(ctx, tx)
	if err != nil {
		return nil, 0, entity.BookLicenseAuditCounts{}, err
	}

	total, err := queryBookLicenseTotal(ctx, tx, filter.Status)
	if err != nil {
		return nil, 0, entity.BookLicenseAuditCounts{}, fmt.Errorf("EditorialRepo.ListBookLicenseAudit total: %w", err)
	}

	items, err := queryBookLicenseItems(ctx, tx, filter)
	if err != nil {
		return nil, 0, entity.BookLicenseAuditCounts{}, err
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, 0, entity.BookLicenseAuditCounts{}, fmt.Errorf("EditorialRepo.ListBookLicenseAudit commit: %w", err)
	}

	return items, total, counts, nil
}

func queryBookLicenseTotal(ctx context.Context, tx pgx.Tx, status string) (int, error) {
	var total int

	err := tx.QueryRow(ctx, `
SELECT count(*)
FROM book_license_audit_queue
`+licenseAuditFilterSQL, status).Scan(&total)

	return total, err
}

func queryBookLicenseItems(
	ctx context.Context,
	tx pgx.Tx,
	filter repo.LicenseAuditFilter,
) ([]entity.BookLicenseAuditItem, error) {
	rows, err := tx.Query(ctx, `
SELECT book_id, book_name, license_status, license_reason,
       license_evidence_url, license_updated_by::text, license_updated_at,
       publication_status,
       (
           license_status NOT IN ('permitted', 'restricted')
           AND (catalog_grandfathered OR grandfathered_language_count > 0)
       ) AS grandfathered,
       registered_reader_count, saved_item_count, last_reader_activity_at
FROM book_license_audit_queue
`+licenseAuditFilterSQL+`
ORDER BY grandfathered DESC,
         registered_reader_count DESC,
         saved_item_count DESC,
         last_reader_activity_at DESC NULLS LAST,
         book_id ASC
LIMIT $2 OFFSET $3`, filter.Status, filter.Limit, filter.Offset)
	if err != nil {
		return nil, fmt.Errorf("EditorialRepo.ListBookLicenseAudit query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.BookLicenseAuditItem, 0, filter.Limit)

	for rows.Next() {
		var item entity.BookLicenseAuditItem

		if err = rows.Scan(
			&item.BookID,
			&item.BookTitle,
			&item.LicenseStatus,
			&item.Reason,
			&item.EvidenceURL,
			&item.UpdatedBy,
			&item.UpdatedAt,
			&item.PublicationStatus,
			&item.Grandfathered,
			&item.RegisteredReaderCount,
			&item.SavedItemCount,
			&item.LastActivityAt,
		); err != nil {
			return nil, fmt.Errorf("EditorialRepo.ListBookLicenseAudit scan: %w", err)
		}

		items = append(items, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("EditorialRepo.ListBookLicenseAudit rows: %w", err)
	}

	return items, nil
}

// GetBookLicense reads the current Edition-level decision used as the ETag
// source for subsequent evidence-backed transitions.
func (r *EditorialRepo) GetBookLicense(ctx context.Context, bookID int) (entity.BookLicense, error) {
	license, err := scanBookLicense(r.Pool.QueryRow(ctx, bookLicenseSelect, bookID))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.BookLicense{}, entity.ErrBookNotFound
	}

	if err != nil {
		return entity.BookLicense{}, fmt.Errorf("EditorialRepo.GetBookLicense: %w", err)
	}

	return license, nil
}

// UpdateBookLicense serializes optimistic-lock checking and the books update
// in one transaction. The migration trigger writes book_license_audits in the
// same transaction, avoiding a second history row from this adapter.
func (r *EditorialRepo) UpdateBookLicense(
	ctx context.Context,
	actorID string,
	update entity.BookLicenseUpdate,
	expectedUpdatedAt *time.Time,
) (entity.BookLicense, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.BookLicense{}, fmt.Errorf("EditorialRepo.UpdateBookLicense begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	var currentUpdatedAt time.Time

	err = tx.QueryRow(ctx, `
SELECT license_updated_at
FROM books
WHERE id = $1 AND is_deleted = false
FOR UPDATE`, update.BookID).Scan(&currentUpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.BookLicense{}, entity.ErrBookNotFound
	}

	if err != nil {
		return entity.BookLicense{}, fmt.Errorf("EditorialRepo.UpdateBookLicense lock: %w", err)
	}

	if expectedUpdatedAt != nil && !currentUpdatedAt.Equal(*expectedUpdatedAt) {
		return entity.BookLicense{}, entity.ErrPreconditionFailed
	}

	if _, err = tx.Exec(ctx, `
UPDATE books
SET license_status = $2,
    license_reason = $3,
    license_evidence_url = $4,
    license_updated_by = NULLIF($5, '')::uuid,
    license_updated_at = clock_timestamp()
WHERE id = $1`, update.BookID, update.LicenseStatus, update.Reason, update.EvidenceURL, actorID); err != nil {
		return entity.BookLicense{}, fmt.Errorf("EditorialRepo.UpdateBookLicense update: %w", err)
	}

	license, err := scanBookLicense(tx.QueryRow(ctx, bookLicenseSelect, update.BookID))
	if err != nil {
		return entity.BookLicense{}, fmt.Errorf("EditorialRepo.UpdateBookLicense return: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.BookLicense{}, fmt.Errorf("EditorialRepo.UpdateBookLicense commit: %w", err)
	}

	return license, nil
}

func queryBookLicenseCounts(ctx context.Context, tx pgx.Tx) (entity.BookLicenseAuditCounts, error) {
	var counts entity.BookLicenseAuditCounts

	err := tx.QueryRow(ctx, `
SELECT count(*)::int,
       count(*) FILTER (WHERE license_status IN ('unknown', 'needs_review'))::int,
       count(*) FILTER (WHERE license_status = 'unknown')::int,
       count(*) FILTER (WHERE license_status = 'needs_review')::int,
       count(*) FILTER (WHERE license_status = 'permitted')::int,
       count(*) FILTER (WHERE license_status = 'restricted')::int,
       count(*) FILTER (WHERE license_status = 'public_domain')::int,
       count(*) FILTER (
           WHERE license_status NOT IN ('permitted', 'restricted')
             AND (catalog_grandfathered OR grandfathered_language_count > 0)
       )::int
FROM book_license_audit_queue
WHERE is_deleted = false`).Scan(
		&counts.Total,
		&counts.Unresolved,
		&counts.Unknown,
		&counts.NeedsReview,
		&counts.Permitted,
		&counts.Restricted,
		&counts.PublicDomain,
		&counts.Grandfathered,
	)
	if err != nil {
		return entity.BookLicenseAuditCounts{}, fmt.Errorf("EditorialRepo.ListBookLicenseAudit counts: %w", err)
	}

	return counts, nil
}

func scanBookLicense(row pgx.Row) (entity.BookLicense, error) {
	var license entity.BookLicense

	err := row.Scan(
		&license.BookID,
		&license.BookTitle,
		&license.LicenseStatus,
		&license.Reason,
		&license.EvidenceURL,
		&license.UpdatedBy,
		&license.UpdatedAt,
		&license.PublicationStatus,
		&license.Grandfathered,
		&license.GrandfatheredAt,
	)

	return license, err
}
