package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
)

const oneSignalErasureColumns = `
e.id, e.app_id, e.external_id_ciphertext, e.external_id_hash, e.status, e.attempt_count,
e.next_attempt_at, e.lease_token, e.lease_expires_at, e.accepted_at, e.verified_at,
e.created_at, e.updated_at`

const maxOneSignalErasureBatchSize = 100

// ClaimDueOneSignalErasures leases due rows across all API instances.
func (r *UserRepo) ClaimDueOneSignalErasures(
	ctx context.Context,
	now time.Time,
	leaseToken string,
	leaseExpiresAt time.Time,
	limit int,
) ([]entity.OneSignalErasure, error) {
	if limit < 1 {
		limit = 1
	}

	if limit > maxOneSignalErasureBatchSize {
		limit = maxOneSignalErasureBatchSize
	}

	const query = `
WITH due AS (
    SELECT id
    FROM onesignal_user_erasures
    WHERE status <> 'verified'
      AND next_attempt_at <= $1
      AND (lease_expires_at IS NULL OR lease_expires_at <= $1)
    ORDER BY next_attempt_at, created_at
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
UPDATE onesignal_user_erasures AS e
SET lease_token = $3,
    lease_expires_at = $4,
    updated_at = $1
FROM due
WHERE e.id = due.id
RETURNING ` + oneSignalErasureColumns

	rows, err := r.Pool.Query(ctx, query, now.UTC(), limit, leaseToken, leaseExpiresAt.UTC())
	if err != nil {
		return nil, fmt.Errorf("UserRepo - ClaimDueOneSignalErasures - Query: %w", err)
	}
	defer rows.Close()

	erasures := make([]entity.OneSignalErasure, 0, limit)

	for rows.Next() {
		erasure, scanErr := scanOneSignalErasure(rows)
		if scanErr != nil {
			return nil, scanErr
		}

		erasures = append(erasures, erasure)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("UserRepo - ClaimDueOneSignalErasures - rows: %w", err)
	}

	return erasures, nil
}

// RecordOneSignalErasureAttempt appends sanitized evidence and advances the workflow atomically.
func (r *UserRepo) RecordOneSignalErasureAttempt(
	ctx context.Context,
	attempt *entity.OneSignalErasureAttempt,
) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - RecordOneSignalErasureAttempt - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := insertOneSignalErasureAttempt(ctx, tx, attempt); err != nil {
		return err
	}

	if err := advanceOneSignalErasure(ctx, tx, attempt); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - RecordOneSignalErasureAttempt - Commit: %w", err)
	}

	return nil
}

func insertOneSignalErasureAttempt(
	ctx context.Context,
	tx pgx.Tx,
	attempt *entity.OneSignalErasureAttempt,
) error {
	const query = `
INSERT INTO onesignal_user_erasure_attempts (
    id, erasure_id, operation, outcome, http_status, reason_code, reason_detail, occurred_at
) VALUES ($1, $2, $3, $4, NULLIF($5, 0), $6, $7, $8)`

	if _, err := tx.Exec(
		ctx,
		query,
		attempt.ID,
		attempt.ErasureID,
		attempt.Operation,
		attempt.ProviderCallOutcome,
		attempt.HTTPStatus,
		attempt.ReasonCode,
		attempt.ReasonDetail,
		attempt.AttemptedAt.UTC(),
	); err != nil {
		return fmt.Errorf("UserRepo - RecordOneSignalErasureAttempt - insert evidence: %w", err)
	}

	return nil
}

func advanceOneSignalErasure(
	ctx context.Context,
	tx pgx.Tx,
	attempt *entity.OneSignalErasureAttempt,
) error {
	const query = `
UPDATE onesignal_user_erasures
SET status = $3,
    attempt_count = attempt_count + 1,
    next_attempt_at = $4,
    lease_token = NULL,
    lease_expires_at = NULL,
    last_http_status = NULLIF($5, 0),
    last_reason_code = $6,
    last_reason_detail = $7,
    accepted_at = COALESCE(accepted_at, $8),
    verified_at = $9,
    external_id_ciphertext = CASE WHEN $10 THEN NULL ELSE external_id_ciphertext END,
    updated_at = $11
WHERE id = $1
  AND lease_token = $2`

	tag, err := tx.Exec(
		ctx,
		query,
		attempt.ErasureID,
		attempt.LeaseToken,
		attempt.Status,
		attempt.NextAttemptAt.UTC(),
		attempt.HTTPStatus,
		attempt.ReasonCode,
		attempt.ReasonDetail,
		attempt.AcceptedAt,
		attempt.VerifiedAt,
		attempt.ClearExternalID,
		attempt.AttemptedAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("UserRepo - RecordOneSignalErasureAttempt - update workflow: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrOneSignalErasureLeaseLost
	}

	return nil
}

// CleanupVerifiedOneSignalErasures removes completed evidence after its retention window.
func (r *UserRepo) CleanupVerifiedOneSignalErasures(
	ctx context.Context,
	verifiedBefore time.Time,
) (int64, error) {
	tag, err := r.Pool.Exec(ctx, `
DELETE FROM onesignal_user_erasures
WHERE status = 'verified'
  AND verified_at < $1`, verifiedBefore.UTC())
	if err != nil {
		return 0, fmt.Errorf("UserRepo - CleanupVerifiedOneSignalErasures - Exec: %w", err)
	}

	return tag.RowsAffected(), nil
}

func scanOneSignalErasure(row pgx.Row) (entity.OneSignalErasure, error) {
	var (
		erasure        entity.OneSignalErasure
		ciphertext     *string
		leaseToken     *string
		leaseExpiresAt *time.Time
	)

	err := row.Scan(
		&erasure.ID,
		&erasure.AppID,
		&ciphertext,
		&erasure.ExternalIDHash,
		&erasure.Status,
		&erasure.AttemptCount,
		&erasure.NextAttemptAt,
		&leaseToken,
		&leaseExpiresAt,
		&erasure.AcceptedAt,
		&erasure.VerifiedAt,
		&erasure.CreatedAt,
		&erasure.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.OneSignalErasure{}, err
		}

		return entity.OneSignalErasure{}, fmt.Errorf("scan OneSignal erasure: %w", err)
	}

	if ciphertext != nil {
		erasure.ExternalIDCiphertext = *ciphertext
	}

	if leaseToken != nil {
		erasure.LeaseToken = *leaseToken
	}

	if leaseExpiresAt != nil {
		erasure.LeaseExpiresAt = *leaseExpiresAt
	}

	return erasure, nil
}
