package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const quranSourceLicenseSelect = `
SELECT source_kind, source_id, lang, name, translator, responsible_name,
       responsible_role, source_url, license_status, license_reason,
       license_evidence_url, license_updated_by::text, license_updated_at,
       coverage_count, license_grandfathered_at
FROM quran_source_license_inventory`

const quranSourceLicenseFilter = `
WHERE ($1 = '' OR source_kind = $1)
  AND ($2 = 'all'
       OR ($2 = 'unresolved' AND license_status IN ('unknown', 'needs_review'))
       OR license_status = $2)`

// ListQuranSourceLicenses returns a stable protected inventory snapshot.
//
//nolint:wsl_v5 // repeatable-read total and ordered rows form one compact inventory transaction
func (r *EditorialRepo) ListQuranSourceLicenses(
	ctx context.Context,
	sourceKind, status string,
	limit, offset uint64,
) ([]entity.QuranSourceLicense, int, error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo.ListQuranSourceLicenses begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM quran_source_license_inventory `+
		quranSourceLicenseFilter, sourceKind, status).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo.ListQuranSourceLicenses total: %w", err)
	}

	rows, err := tx.Query(ctx, quranSourceLicenseSelect+` `+quranSourceLicenseFilter+`
ORDER BY (license_status IN ('unknown', 'needs_review')) DESC,
         coverage_count DESC, source_kind, source_id
LIMIT $3 OFFSET $4`, sourceKind, status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo.ListQuranSourceLicenses query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.QuranSourceLicense, 0, limit)
	for rows.Next() {
		item, err := scanQuranSourceLicense(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("EditorialRepo.ListQuranSourceLicenses scan: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo.ListQuranSourceLicenses rows: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("EditorialRepo.ListQuranSourceLicenses commit: %w", err)
	}

	return items, total, nil
}

// GetQuranSourceLicense returns one source and its immutable audit history.
func (r *EditorialRepo) GetQuranSourceLicense(
	ctx context.Context,
	sourceKind, sourceID string,
) (entity.QuranSourceLicense, error) {
	return getQuranSourceLicense(ctx, r.Pool, sourceKind, sourceID)
}

//nolint:wsl_v5 // source row and immutable history are hydrated as one read model
func getQuranSourceLicense(
	ctx context.Context,
	queryer interface {
		QueryRow(context.Context, string, ...any) pgx.Row
		Query(context.Context, string, ...any) (pgx.Rows, error)
	},
	sourceKind, sourceID string,
) (entity.QuranSourceLicense, error) {
	value, err := scanQuranSourceLicense(queryer.QueryRow(ctx,
		quranSourceLicenseSelect+` WHERE source_kind = $1 AND source_id = $2`, sourceKind, sourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.QuranSourceLicense{}, entity.ErrQuranSourceNotFound
	}
	if err != nil {
		return entity.QuranSourceLicense{}, fmt.Errorf("EditorialRepo.GetQuranSourceLicense: %w", err)
	}

	rows, err := queryer.Query(ctx, `
SELECT id, old_status, new_status, reason, evidence_url, old_attribution,
       new_attribution, actor_id::text, created_at
FROM quran_source_license_audits
WHERE source_kind = $1 AND source_id = $2
ORDER BY created_at DESC, id DESC`, sourceKind, sourceID)
	if err != nil {
		return entity.QuranSourceLicense{}, fmt.Errorf("EditorialRepo Quran source history: %w", err)
	}
	defer rows.Close()

	value.History = make([]entity.QuranSourceLicenseAudit, 0)
	for rows.Next() {
		var audit entity.QuranSourceLicenseAudit
		if err := rows.Scan(&audit.ID, &audit.OldStatus, &audit.NewStatus, &audit.Reason,
			&audit.EvidenceURL, &audit.OldAttribution, &audit.NewAttribution,
			&audit.ActorID, &audit.CreatedAt); err != nil {
			return entity.QuranSourceLicense{}, fmt.Errorf("EditorialRepo Quran source history scan: %w", err)
		}
		value.History = append(value.History, audit)
	}
	if err := rows.Err(); err != nil {
		return entity.QuranSourceLicense{}, fmt.Errorf("EditorialRepo Quran source history rows: %w", err)
	}

	return value, nil
}

// UpdateQuranSourceLicense performs the ETag check and guarded update in one
// transaction. The database trigger appends quran_source_license_audits.
//
//nolint:funlen,gocritic,gocyclo,cyclop,wsl_v5 // value parameter is fixed by the repo contract; three source tables share one guarded transaction
func (r *EditorialRepo) UpdateQuranSourceLicense(
	ctx context.Context,
	actorID string,
	update entity.QuranSourceLicenseUpdate,
	expectedUpdatedAt *time.Time,
) (entity.QuranSourceLicense, error) {
	table, err := quranSourceLicenseTable(update.SourceKind)
	if err != nil {
		return entity.QuranSourceLicense{}, err
	}
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.QuranSourceLicense{}, fmt.Errorf("EditorialRepo.UpdateQuranSourceLicense begin: %w", err)
	}
	defer rollbackTx(ctx, tx)

	var currentUpdatedAt time.Time
	lockSQL := fmt.Sprintf("SELECT license_updated_at FROM %s WHERE id = $1 FOR UPDATE", table)
	if err := tx.QueryRow(ctx, lockSQL, update.SourceID).Scan(&currentUpdatedAt); errors.Is(err, pgx.ErrNoRows) {
		return entity.QuranSourceLicense{}, entity.ErrQuranSourceNotFound
	} else if err != nil {
		return entity.QuranSourceLicense{}, fmt.Errorf("EditorialRepo.UpdateQuranSourceLicense lock: %w", err)
	}
	if expectedUpdatedAt != nil && !currentUpdatedAt.Equal(*expectedUpdatedAt) {
		return entity.QuranSourceLicense{}, entity.ErrPreconditionFailed
	}

	var (
		updateSQL  string
		updateArgs []any
	)
	switch update.SourceKind {
	case entity.QuranSourceKindTranslation:
		updateSQL = `
UPDATE quran_translation_sources
SET license_status = $2, license_reason = $3, license_evidence_url = $4,
    license_updated_by = NULLIF($5, '')::uuid, license_updated_at = clock_timestamp(),
    translator = COALESCE($6, translator),
    responsible_name = COALESCE($7, responsible_name),
    responsible_role = COALESCE($8, responsible_role)
WHERE id = $1`
		updateArgs = []any{
			update.SourceID, update.LicenseStatus, update.Reason, update.EvidenceURL,
			actorID, update.Translator, update.ResponsibleName, update.ResponsibleRole,
		}
	case entity.QuranSourceKindScript:
		updateSQL = `
UPDATE quran_script_sources
SET license_status = $2, license_reason = $3, license_evidence_url = $4,
    license_updated_by = NULLIF($5, '')::uuid, license_updated_at = clock_timestamp(),
	responsible_name = COALESCE($6, responsible_name),
	responsible_role = COALESCE($7, responsible_role),
	license_grandfathered_at = CASE WHEN $2 = 'restricted' THEN NULL ELSE license_grandfathered_at END,
	license_grandfathered_checksum = CASE WHEN $2 = 'restricted' THEN NULL ELSE license_grandfathered_checksum END
WHERE id = $1`
		updateArgs = []any{
			update.SourceID, update.LicenseStatus, update.Reason, update.EvidenceURL,
			actorID, update.ResponsibleName, update.ResponsibleRole,
		}
	case entity.QuranSourceKindTransliteration:
		updateSQL = `
UPDATE quran_transliteration_sources
SET license_status = $2, license_reason = $3, license_evidence_url = $4,
    license_updated_by = NULLIF($5, '')::uuid, license_updated_at = clock_timestamp(),
	responsible_name = COALESCE($6, responsible_name),
	responsible_role = COALESCE($7, responsible_role)
WHERE id = $1`
		updateArgs = []any{
			update.SourceID, update.LicenseStatus, update.Reason, update.EvidenceURL,
			actorID, update.ResponsibleName, update.ResponsibleRole,
		}
	}
	if _, err := tx.Exec(ctx, updateSQL, updateArgs...); err != nil {
		return entity.QuranSourceLicense{}, mapQuranSourceLicenseWriteError(err)
	}

	value, err := getQuranSourceLicense(ctx, tx, update.SourceKind, update.SourceID)
	if err != nil {
		return entity.QuranSourceLicense{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return entity.QuranSourceLicense{}, fmt.Errorf("EditorialRepo.UpdateQuranSourceLicense commit: %w", err)
	}

	return value, nil
}

func quranSourceLicenseTable(sourceKind string) (string, error) {
	switch sourceKind {
	case entity.QuranSourceKindScript:
		return "quran_script_sources", nil
	case entity.QuranSourceKindTranslation:
		return "quran_translation_sources", nil
	case entity.QuranSourceKindTransliteration:
		return "quran_transliteration_sources", nil
	default:
		return "", entity.ErrQuranSourceNotFound
	}
}

//nolint:wsl_v5 // flat row scan followed by explicit nullable-field projection
func scanQuranSourceLicense(row pgx.Row) (entity.QuranSourceLicense, error) {
	var (
		value           entity.QuranSourceLicense
		lang            sql.NullString
		translator      sql.NullString
		responsibleName sql.NullString
		responsibleRole sql.NullString
		sourceURL       sql.NullString
		reason          sql.NullString
		evidenceURL     sql.NullString
		updatedBy       sql.NullString
		grandfatheredAt sql.NullTime
	)
	if err := row.Scan(&value.SourceKind, &value.SourceID, &lang, &value.Name,
		&translator, &responsibleName, &responsibleRole, &sourceURL,
		&value.LicenseStatus, &reason, &evidenceURL, &updatedBy, &value.UpdatedAt,
		&value.CoverageCount, &grandfatheredAt); err != nil {
		return entity.QuranSourceLicense{}, err
	}
	value.Lang = nullableString(lang)
	value.Translator = nullableString(translator)
	value.ResponsibleName = nullableString(responsibleName)
	value.ResponsibleRole = nullableString(responsibleRole)
	value.SourceURL = nullableString(sourceURL)
	value.Reason = nullableString(reason)
	value.EvidenceURL = nullableString(evidenceURL)
	value.UpdatedBy = nullableString(updatedBy)
	value.GrandfatheredAt = nullableTime(grandfatheredAt)

	return value, nil
}

//nolint:wsl_v5 // PostgreSQL constraint mapping stays directly adjacent to the type assertion
func mapQuranSourceLicenseWriteError(err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("EditorialRepo.UpdateQuranSourceLicense update: %w", err)
	}
	switch pgErr.ConstraintName {
	case "quran_source_license_attribution_check":
		return entity.ErrInvalidQuranSourceAttribution
	case "quran_source_license_audit_reason_check":
		return entity.ErrInvalidLicenseReason
	case "quran_source_license_audit_actor_check":
		return entity.ErrForbidden
	default:
		return fmt.Errorf("EditorialRepo.UpdateQuranSourceLicense update: %w", err)
	}
}
