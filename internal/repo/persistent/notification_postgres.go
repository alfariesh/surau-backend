package persistent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
)

const notificationDeliveryColumns = `
    id::text,
    user_id::text,
    notification_type,
    COALESCE(to_char(local_date, 'YYYY-MM-DD'), ''),
    payload,
    idempotency_key::text,
    status,
    COALESCE(provider_notification_id, ''),
    COALESCE(last_reason_code, ''),
    COALESCE(last_reason_detail, ''),
    attempt_count,
    COALESCE(lease_token::text, ''),
    lease_expires_at,
    next_attempt_at,
    delivery_deadline_at,
    created_at,
    updated_at`

const claimPendingReminderDeliveriesSQL = `
WITH due AS (
    SELECT nd.id AS delivery_id
    FROM notification_deliveries nd
    JOIN users u ON u.id = nd.user_id AND u.deleted_at IS NULL
    JOIN user_preferences pref
      ON pref.user_id = nd.user_id
     AND COALESCE(pref.notify_streak_reminders, TRUE)
    JOIN user_profiles p ON p.user_id = nd.user_id
    JOIN pg_timezone_names tz ON tz.name = btrim(p.timezone)
    WHERE nd.notification_type = 'streak_reminder'
      AND nd.status IN ('pending', 'retrying')
      AND nd.next_attempt_at <= $1
      AND nd.attempt_count < 8
      AND nd.local_date = timezone(tz.name, $1::timestamptz)::date
      AND nd.delivery_deadline_at > $1
      AND (nd.lease_expires_at IS NULL OR nd.lease_expires_at <= $1)
      AND timezone(tz.name, $1::timestamptz)::time >= TIME '19:00'
      AND timezone(tz.name, $1::timestamptz)::time < TIME '21:00'
      AND NOT CASE
          WHEN $2::time < $3::time
              THEN timezone(tz.name, $1::timestamptz)::time >= $2::time
               AND timezone(tz.name, $1::timestamptz)::time < $3::time
          ELSE timezone(tz.name, $1::timestamptz)::time >= $2::time
            OR timezone(tz.name, $1::timestamptz)::time < $3::time
      END
      AND EXISTS (
          SELECT 1
          FROM reading_activity ra
          WHERE ra.user_id = nd.user_id
            AND ra.activity_date = timezone(tz.name, $1::timestamptz)::date - 1
      )
      AND NOT EXISTS (
          SELECT 1
          FROM reading_activity ra
          WHERE ra.user_id = nd.user_id
            AND ra.activity_date = timezone(tz.name, $1::timestamptz)::date
      )
    ORDER BY nd.next_attempt_at, nd.created_at, nd.id
    FOR UPDATE OF nd SKIP LOCKED
    LIMIT $4
)
UPDATE notification_deliveries nd
SET lease_token = $5,
    lease_expires_at = $6,
    updated_at = $1
FROM due
WHERE nd.id = due.delivery_id
RETURNING ` + notificationDeliveryColumns

const maxStoredRetryAfter = 24 * time.Hour

var errStaleNotificationLease = errors.New("stale notification delivery lease")

// ClaimReminderDelivery atomically combines the permanent per-local-day key with the independent
// 20-hour cooldown. Existing retryable rows bypass the cooldown but still require an expired lease.
func (r *PersonalRepo) ClaimReminderDelivery(
	ctx context.Context,
	claim *entity.ReminderDeliveryClaim,
	asOf time.Time,
) (delivery entity.NotificationDelivery, claimed bool, reason string, err error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.NotificationDelivery{}, false, "", fmt.Errorf("PersonalRepo - ClaimReminderDelivery - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	delivery, claimed, reason, handled, err := reclaimReminderDelivery(ctx, tx, claim, asOf)
	if err != nil {
		return entity.NotificationDelivery{}, false, "", err
	}

	if handled {
		if claimed {
			if err := tx.Commit(ctx); err != nil {
				return entity.NotificationDelivery{}, false, "", fmt.Errorf("PersonalRepo - ClaimReminderDelivery - reclaim commit: %w", err)
			}
		}

		return delivery, claimed, reason, nil
	}

	acquired, err := acquireReminderCooldowns(ctx, tx, claim, asOf)
	if err != nil {
		return entity.NotificationDelivery{}, false, "", err
	}

	if !acquired {
		return entity.NotificationDelivery{}, false, "cooldown", nil
	}

	delivery, err = insertReminderDelivery(ctx, tx, claim, asOf)
	if err != nil {
		return entity.NotificationDelivery{}, false, "", err
	}

	if err := tx.Commit(ctx); err != nil {
		return entity.NotificationDelivery{}, false, "", fmt.Errorf("PersonalRepo - ClaimReminderDelivery - commit: %w", err)
	}

	return delivery, true, "", nil
}

func reclaimReminderDelivery(
	ctx context.Context,
	tx pgx.Tx,
	claim *entity.ReminderDeliveryClaim,
	asOf time.Time,
) (delivery entity.NotificationDelivery, claimed bool, reason string, handled bool, err error) {
	const existingSQL = `
SELECT ` + notificationDeliveryColumns + `
FROM notification_deliveries
WHERE user_id = $1
  AND notification_type = $2
  AND local_date = $3::date
FOR UPDATE`

	existing, scanErr := scanNotificationDelivery(tx.QueryRow(
		ctx,
		existingSQL,
		claim.Delivery.UserID,
		claim.Delivery.NotificationType,
		claim.Delivery.LocalDate,
	))
	if errors.Is(scanErr, pgx.ErrNoRows) {
		return entity.NotificationDelivery{}, false, "", false, nil
	}

	if scanErr != nil {
		return entity.NotificationDelivery{}, false, "", false,
			fmt.Errorf("PersonalRepo - ClaimReminderDelivery - existing: %w", scanErr)
	}

	switch {
	case existing.Status == entity.NotificationStatusAccepted || existing.Status == entity.NotificationStatusFailed:
		return entity.NotificationDelivery{}, false, "daily_duplicate", true, nil
	case existing.NextAttemptAt.After(asOf):
		return entity.NotificationDelivery{}, false, "retry_not_due", true, nil
	case !existing.LeaseExpiresAt.IsZero() && existing.LeaseExpiresAt.After(asOf):
		return entity.NotificationDelivery{}, false, "leased", true, nil
	}

	const reclaimSQL = `
UPDATE notification_deliveries
SET lease_token = $2,
    lease_expires_at = $3,
    updated_at = $4
WHERE id = $1
RETURNING ` + notificationDeliveryColumns

	reclaimed, reclaimErr := scanNotificationDelivery(tx.QueryRow(
		ctx,
		reclaimSQL,
		existing.ID,
		claim.Delivery.LeaseToken,
		claim.Delivery.LeaseExpiresAt,
		asOf,
	))
	if reclaimErr != nil {
		return entity.NotificationDelivery{}, false, "", false,
			fmt.Errorf("PersonalRepo - ClaimReminderDelivery - reclaim: %w", reclaimErr)
	}

	return reclaimed, true, "", true, nil
}

func acquireReminderCooldowns(
	ctx context.Context,
	tx pgx.Tx,
	claim *entity.ReminderDeliveryClaim,
	asOf time.Time,
) (bool, error) {
	keys := []string{claim.CooldownKeyHash}
	if claim.LegacyCooldownKeyHash != "" && claim.LegacyCooldownKeyHash != claim.CooldownKeyHash {
		keys = append(keys, claim.LegacyCooldownKeyHash)
	}

	for _, keyHash := range keys {
		const cooldownSQL = `
INSERT INTO auth_notification_cooldowns (event, key_hash, expires_at, created_at, updated_at)
VALUES ($1, $2, $3, $4, $4)
ON CONFLICT (event, key_hash)
DO UPDATE SET expires_at = EXCLUDED.expires_at, updated_at = EXCLUDED.updated_at
WHERE auth_notification_cooldowns.expires_at <= $4
RETURNING true`

		var acquired bool
		if err := tx.QueryRow(
			ctx,
			cooldownSQL,
			claim.Delivery.NotificationType,
			keyHash,
			claim.CooldownExpiresAt,
			asOf,
		).Scan(&acquired); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}

			return false, fmt.Errorf("PersonalRepo - ClaimReminderDelivery - cooldown: %w", err)
		}
	}

	return true, nil
}

func insertReminderDelivery(
	ctx context.Context,
	tx pgx.Tx,
	claim *entity.ReminderDeliveryClaim,
	asOf time.Time,
) (entity.NotificationDelivery, error) {
	payload, err := json.Marshal(claim.Delivery.Payload)
	if err != nil {
		return entity.NotificationDelivery{}, fmt.Errorf("PersonalRepo - ClaimReminderDelivery - Marshal: %w", err)
	}

	const insertSQL = `
INSERT INTO notification_deliveries (
    id, user_id, notification_type, local_date, payload, idempotency_key,
    lease_token, lease_expires_at, next_attempt_at, delivery_deadline_at,
    created_at, updated_at
) VALUES ($1, $2, $3, $4::date, $5, $6, $7, $8, $9, $10, $9, $9)
RETURNING ` + notificationDeliveryColumns

	delivery, err := scanNotificationDelivery(tx.QueryRow(
		ctx,
		insertSQL,
		claim.Delivery.ID,
		claim.Delivery.UserID,
		claim.Delivery.NotificationType,
		claim.Delivery.LocalDate,
		payload,
		claim.Delivery.IdempotencyKey,
		claim.Delivery.LeaseToken,
		claim.Delivery.LeaseExpiresAt,
		asOf,
		nullableDeliveryTimeArg(claim.Delivery.DeliveryDeadlineAt),
	))
	if err != nil {
		return entity.NotificationDelivery{}, fmt.Errorf("PersonalRepo - ClaimReminderDelivery - insert: %w", err)
	}

	return delivery, nil
}

// CreateEventDelivery persists and leases a non-reminder delivery before any provider request.
func (r *PersonalRepo) CreateEventDelivery(
	ctx context.Context,
	create *entity.NotificationDeliveryCreate,
	asOf time.Time,
) (entity.NotificationDelivery, error) {
	payload, err := json.Marshal(create.Payload)
	if err != nil {
		return entity.NotificationDelivery{}, fmt.Errorf("PersonalRepo - CreateEventDelivery - Marshal: %w", err)
	}

	const query = `
INSERT INTO notification_deliveries (
    id, user_id, notification_type, local_date, payload, idempotency_key,
    lease_token, lease_expires_at, next_attempt_at, delivery_deadline_at,
    created_at, updated_at
) VALUES ($1, $2, $3, NULL, $4, $5, $6, $7, $8, $9, $8, $8)
RETURNING ` + notificationDeliveryColumns

	delivery, err := scanNotificationDelivery(r.Pool.QueryRow(
		ctx,
		query,
		create.ID,
		create.UserID,
		create.NotificationType,
		payload,
		create.IdempotencyKey,
		create.LeaseToken,
		create.LeaseExpiresAt,
		asOf,
		nullableDeliveryTimeArg(create.DeliveryDeadlineAt),
	))
	if err != nil {
		return entity.NotificationDelivery{}, fmt.Errorf("PersonalRepo - CreateEventDelivery - insert: %w", err)
	}

	return delivery, nil
}

// ClaimPendingEventDeliveries leases due event pushes left behind by a crash or retryable failure.
func (r *PersonalRepo) ClaimPendingEventDeliveries(
	ctx context.Context,
	asOf time.Time,
	leaseToken string,
	leaseExpiresAt time.Time,
	limit int,
) ([]entity.NotificationDelivery, error) {
	const query = `
WITH due AS (
    SELECT id AS delivery_id
    FROM notification_deliveries
    WHERE notification_type <> 'streak_reminder'
      AND status IN ('pending', 'retrying')
      AND next_attempt_at <= $1
      AND attempt_count < 8
      AND (delivery_deadline_at IS NULL OR delivery_deadline_at > $1)
      AND (lease_expires_at IS NULL OR lease_expires_at <= $1)
    ORDER BY next_attempt_at, created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT $2
)
UPDATE notification_deliveries nd
SET lease_token = $3,
    lease_expires_at = $4,
    updated_at = $1
FROM due
WHERE nd.id = due.delivery_id
RETURNING ` + notificationDeliveryColumns

	rows, err := r.Pool.Query(ctx, query, asOf, limit, leaseToken, leaseExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - ClaimPendingEventDeliveries - Query: %w", err)
	}
	defer rows.Close()

	deliveries := make([]entity.NotificationDelivery, 0, limit)

	for rows.Next() {
		delivery, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("PersonalRepo - ClaimPendingEventDeliveries - Scan: %w", err)
		}

		deliveries = append(deliveries, delivery)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - ClaimPendingEventDeliveries - rows: %w", err)
	}

	return deliveries, nil
}

// ClaimPendingReminderDeliveries leases reminder retries only while the user's current timezone,
// local date, scheduler window, preference, and operator quiet-hours policy still permit a send.
// New-candidate selection excludes every existing daily row, so retries cannot consume its 5,000
// row budget and starve users who have not yet received a logical delivery.
func (r *PersonalRepo) ClaimPendingReminderDeliveries(
	ctx context.Context,
	asOf time.Time,
	quietStart,
	quietEnd,
	leaseToken string,
	leaseExpiresAt time.Time,
	limit int,
) ([]entity.NotificationDelivery, error) {
	rows, err := r.Pool.Query(
		ctx,
		claimPendingReminderDeliveriesSQL,
		asOf,
		quietStart,
		quietEnd,
		limit,
		leaseToken,
		leaseExpiresAt,
	)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - ClaimPendingReminderDeliveries - Query: %w", err)
	}
	defer rows.Close()

	deliveries := make([]entity.NotificationDelivery, 0, limit)

	for rows.Next() {
		delivery, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("PersonalRepo - ClaimPendingReminderDeliveries - Scan: %w", err)
		}

		deliveries = append(deliveries, delivery)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - ClaimPendingReminderDeliveries - rows: %w", err)
	}

	return deliveries, nil
}

// ExpireNotificationDeliveries terminally fails rows whose retry budget or delivery window ended.
func (r *PersonalRepo) ExpireNotificationDeliveries(ctx context.Context, asOf time.Time) (int64, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("PersonalRepo - ExpireNotificationDeliveries - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	groups, err := expireNotificationDeliveries(ctx, tx, asOf)
	if err != nil {
		return 0, err
	}

	var total int64

	for _, group := range groups {
		total += group.count
		if err := incrementNotificationMetric(
			ctx,
			tx,
			"delivery",
			group.notificationType,
			"failed",
			"",
			group.count,
			asOf,
		); err != nil {
			return 0, err
		}

		if group.notificationType == entity.NotificationTypeStreakReminder &&
			group.reasonCode == "delivery_window_expired" {
			if err := incrementNotificationMetric(
				ctx,
				tx,
				"reminder_skip",
				group.notificationType,
				"skipped",
				group.reasonCode,
				group.count,
				asOf,
			); err != nil {
				return 0, err
			}
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("PersonalRepo - ExpireNotificationDeliveries - commit: %w", err)
	}

	return total, nil
}

type expiredDeliveryMetric struct {
	notificationType string
	reasonCode       string
	count            int64
}

func expireNotificationDeliveries(
	ctx context.Context,
	tx pgx.Tx,
	asOf time.Time,
) ([]expiredDeliveryMetric, error) {
	const query = `
WITH expired AS (
    UPDATE notification_deliveries
    SET status = 'failed',
        last_reason_code = CASE
            WHEN attempt_count >= 8 THEN 'retry_exhausted'
            WHEN notification_type = 'streak_reminder' THEN 'delivery_window_expired'
            ELSE 'retry_deadline_exceeded'
        END,
        last_reason_detail = NULL,
        lease_token = NULL,
        lease_expires_at = NULL,
        failed_at = $1,
        updated_at = $1
    WHERE status IN ('pending', 'retrying')
      AND (lease_expires_at IS NULL OR lease_expires_at <= $1)
      AND (
          attempt_count >= 8
          OR (delivery_deadline_at IS NOT NULL AND delivery_deadline_at <= $1)
      )
    RETURNING notification_type, last_reason_code
)
SELECT notification_type, last_reason_code, count(*)
FROM expired
GROUP BY notification_type, last_reason_code`

	rows, err := tx.Query(ctx, query, asOf)
	if err != nil {
		return nil, fmt.Errorf("PersonalRepo - ExpireNotificationDeliveries - Query: %w", err)
	}
	defer rows.Close()

	groups := make([]expiredDeliveryMetric, 0)

	for rows.Next() {
		var group expiredDeliveryMetric

		if err := rows.Scan(&group.notificationType, &group.reasonCode, &group.count); err != nil {
			return nil, fmt.Errorf("PersonalRepo - ExpireNotificationDeliveries - Scan: %w", err)
		}

		groups = append(groups, group)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("PersonalRepo - ExpireNotificationDeliveries - rows: %w", err)
	}

	rows.Close()

	return groups, nil
}

// FailNotificationDelivery closes a leased logical delivery without inventing a provider attempt.
// It is used when the user's local send window closes between claim and the HTTP request.
func (r *PersonalRepo) FailNotificationDelivery(
	ctx context.Context,
	deliveryID,
	leaseToken,
	reasonCode string,
	asOf time.Time,
) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("PersonalRepo - FailNotificationDelivery - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	const query = `
UPDATE notification_deliveries
SET status = 'failed',
    last_reason_code = $3,
    last_reason_detail = NULL,
    lease_token = NULL,
    lease_expires_at = NULL,
    next_attempt_at = $4,
    failed_at = $4,
    updated_at = $4
WHERE id = $1
  AND lease_token::text = $2
  AND status IN ('pending', 'retrying')
RETURNING notification_type`

	var notificationType string
	if err := tx.QueryRow(ctx, query, deliveryID, leaseToken, reasonCode, asOf).Scan(&notificationType); err != nil {
		return fmt.Errorf("PersonalRepo - FailNotificationDelivery - update: %w", err)
	}

	if err := incrementNotificationMetric(
		ctx,
		tx,
		"delivery",
		notificationType,
		entity.NotificationStatusFailed,
		"",
		1,
		asOf,
	); err != nil {
		return err
	}

	if notificationType == entity.NotificationTypeStreakReminder {
		if err := incrementNotificationMetric(
			ctx,
			tx,
			"reminder_skip",
			notificationType,
			"skipped",
			reasonCode,
			1,
			asOf,
		); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("PersonalRepo - FailNotificationDelivery - commit: %w", err)
	}

	return nil
}

// NextNotificationRetryAt returns the earliest durable checkpoint for an unresolved delivery:
// provider backoff, lease recovery after a crash, or the terminal delivery deadline.
func (r *PersonalRepo) NextNotificationRetryAt(ctx context.Context, asOf time.Time) (time.Time, error) {
	const query = `
SELECT min(checkpoints.checkpoint)
FROM notification_deliveries nd
CROSS JOIN LATERAL (
    SELECT min(value) AS checkpoint
FROM unnest(ARRAY[nd.next_attempt_at, nd.lease_expires_at, nd.delivery_deadline_at]) AS checkpoint_values(value)
    WHERE value > $1
) checkpoints
WHERE nd.status IN ('pending', 'retrying')`

	var next sql.NullTime
	if err := r.Pool.QueryRow(ctx, query, asOf).Scan(&next); err != nil {
		return time.Time{}, fmt.Errorf("PersonalRepo - NextNotificationRetryAt - Query: %w", err)
	}

	if !next.Valid {
		return time.Time{}, nil
	}

	return next.Time, nil
}

// RecordNotificationDeliveryAttempt appends provider evidence, transitions the logical delivery,
// and increments durable metric totals in one transaction.
func (r *PersonalRepo) RecordNotificationDeliveryAttempt(
	ctx context.Context,
	attempt *entity.NotificationDeliveryAttempt,
) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("PersonalRepo - RecordNotificationDeliveryAttempt - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	state, terminal, err := prepareNotificationAttempt(ctx, tx, attempt)
	if err != nil {
		return err
	}

	if terminal {
		return nil
	}

	if err := persistNotificationAttemptEvidence(ctx, tx, state.notificationType, attempt); err != nil {
		return err
	}

	nextStatus := nextNotificationStatus(attempt)
	if err := transitionNotificationDelivery(ctx, tx, attempt, nextStatus); err != nil {
		return err
	}

	if err := incrementFinalDeliveryMetric(ctx, tx, state.notificationType, nextStatus, attempt.OccurredAt); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("PersonalRepo - RecordNotificationDeliveryAttempt - commit: %w", err)
	}

	return nil
}

func prepareNotificationAttempt(
	ctx context.Context,
	tx pgx.Tx,
	attempt *entity.NotificationDeliveryAttempt,
) (lockedNotificationDelivery, bool, error) {
	state, err := lockNotificationDelivery(ctx, tx, attempt.DeliveryID)
	if err != nil {
		return lockedNotificationDelivery{}, false, err
	}

	if isTerminalNotificationStatus(state.status) {
		return state, true, nil
	}

	if !validNotificationLease(state.leaseToken, attempt.LeaseToken) {
		return lockedNotificationDelivery{}, false,
			fmt.Errorf("PersonalRepo - RecordNotificationDeliveryAttempt: %w", errStaleNotificationLease)
	}

	attempt.AttemptNumber = state.attemptCount + 1
	if attempt.OccurredAt.IsZero() {
		attempt.OccurredAt = time.Now().UTC()
	}

	if attempt.NextAttemptAt.IsZero() {
		attempt.NextAttemptAt = attempt.OccurredAt
	}

	return state, false, nil
}

func persistNotificationAttemptEvidence(
	ctx context.Context,
	tx pgx.Tx,
	notificationType string,
	attempt *entity.NotificationDeliveryAttempt,
) error {
	if err := insertNotificationAttempt(ctx, tx, attempt); err != nil {
		return err
	}

	return incrementNotificationMetric(
		ctx,
		tx,
		"delivery_attempt",
		notificationType,
		attempt.Outcome,
		attempt.ReasonCode,
		1,
		attempt.OccurredAt,
	)
}

func isTerminalNotificationStatus(status string) bool {
	return status == entity.NotificationStatusAccepted || status == entity.NotificationStatusFailed
}

func validNotificationLease(stored, supplied string) bool {
	return stored != "" && stored == supplied
}

func incrementFinalDeliveryMetric(
	ctx context.Context,
	tx pgx.Tx,
	notificationType,
	status string,
	at time.Time,
) error {
	if !isTerminalNotificationStatus(status) {
		return nil
	}

	return incrementNotificationMetric(ctx, tx, "delivery", notificationType, status, "", 1, at)
}

type lockedNotificationDelivery struct {
	notificationType string
	status           string
	attemptCount     int
	leaseToken       string
}

func lockNotificationDelivery(
	ctx context.Context,
	tx pgx.Tx,
	deliveryID string,
) (lockedNotificationDelivery, error) {
	const lockSQL = `
SELECT notification_type, status, attempt_count, COALESCE(lease_token::text, '')
FROM notification_deliveries
WHERE id = $1
FOR UPDATE`

	var state lockedNotificationDelivery
	if err := tx.QueryRow(ctx, lockSQL, deliveryID).Scan(
		&state.notificationType,
		&state.status,
		&state.attemptCount,
		&state.leaseToken,
	); err != nil {
		return lockedNotificationDelivery{}, fmt.Errorf("PersonalRepo - RecordNotificationDeliveryAttempt - lock: %w", err)
	}

	return state, nil
}

func insertNotificationAttempt(ctx context.Context, tx pgx.Tx, attempt *entity.NotificationDeliveryAttempt) error {
	const insertAttemptSQL = `
INSERT INTO notification_delivery_attempts (
    id, delivery_id, attempt_number, outcome, retryable, systemic,
    http_status, retry_after_seconds, provider_notification_id,
    reason_code, reason_detail, occurred_at, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)`
	if _, err := tx.Exec(
		ctx,
		insertAttemptSQL,
		attempt.ID,
		attempt.DeliveryID,
		attempt.AttemptNumber,
		attempt.Outcome,
		attempt.Retryable,
		attempt.Systemic,
		nullableIntArg(attempt.HTTPStatus),
		nullableRetryAfterSeconds(attempt.RetryAfter),
		nullableStringArg(attempt.ProviderNotificationID),
		attempt.ReasonCode,
		nullableStringArg(attempt.ReasonDetail),
		attempt.OccurredAt,
	); err != nil {
		return fmt.Errorf("PersonalRepo - RecordNotificationDeliveryAttempt - insert attempt: %w", err)
	}

	return nil
}

func nextNotificationStatus(attempt *entity.NotificationDeliveryAttempt) string {
	if attempt.Outcome == entity.PushDeliveryAccepted {
		return entity.NotificationStatusAccepted
	}

	if attempt.Terminal {
		return entity.NotificationStatusFailed
	}

	return entity.NotificationStatusRetrying
}

func transitionNotificationDelivery(
	ctx context.Context,
	tx pgx.Tx,
	attempt *entity.NotificationDeliveryAttempt,
	nextStatus string,
) error {
	const updateSQL = `
UPDATE notification_deliveries
SET status = $2::varchar,
    provider_notification_id = NULLIF($3, ''),
    last_reason_code = $4,
    last_reason_detail = NULLIF($5, ''),
    attempt_count = $6,
    lease_token = NULL,
    lease_expires_at = NULL,
    next_attempt_at = $7::timestamptz,
    accepted_at = CASE WHEN $2::varchar = 'accepted' THEN $8::timestamptz ELSE NULL::timestamptz END,
    failed_at = CASE WHEN $2::varchar = 'failed' THEN $8::timestamptz ELSE NULL::timestamptz END,
    updated_at = $8::timestamptz
WHERE id = $1`
	if _, err := tx.Exec(
		ctx,
		updateSQL,
		attempt.DeliveryID,
		nextStatus,
		attempt.ProviderNotificationID,
		attempt.ReasonCode,
		attempt.ReasonDetail,
		attempt.AttemptNumber,
		attempt.NextAttemptAt,
		attempt.OccurredAt,
	); err != nil {
		return fmt.Errorf("PersonalRepo - RecordNotificationDeliveryAttempt - update delivery: %w", err)
	}

	return nil
}

// RecordReminderSkips persists bounded diagnostic counts without user IDs in metric labels.
func (r *PersonalRepo) RecordReminderSkips(ctx context.Context, skips map[string]int64, asOf time.Time) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("PersonalRepo - RecordReminderSkips - Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	for reason, count := range skips {
		if count <= 0 {
			continue
		}

		if err := incrementNotificationMetric(
			ctx,
			tx,
			"reminder_skip",
			entity.NotificationTypeStreakReminder,
			"skipped",
			reason,
			count,
			asOf,
		); err != nil {
			return err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("PersonalRepo - RecordReminderSkips - commit: %w", err)
	}

	return nil
}

type notificationDeliveryScanner interface {
	Scan(dest ...any) error
}

func scanNotificationDelivery(row notificationDeliveryScanner) (entity.NotificationDelivery, error) {
	var (
		delivery                   entity.NotificationDelivery
		payload                    []byte
		leaseExpiresAt, deadlineAt sql.NullTime
	)

	if err := row.Scan(
		&delivery.ID,
		&delivery.UserID,
		&delivery.NotificationType,
		&delivery.LocalDate,
		&payload,
		&delivery.IdempotencyKey,
		&delivery.Status,
		&delivery.ProviderNotificationID,
		&delivery.LastReasonCode,
		&delivery.LastReasonDetail,
		&delivery.AttemptCount,
		&delivery.LeaseToken,
		&leaseExpiresAt,
		&delivery.NextAttemptAt,
		&deadlineAt,
		&delivery.CreatedAt,
		&delivery.UpdatedAt,
	); err != nil {
		return entity.NotificationDelivery{}, err
	}

	if err := json.Unmarshal(payload, &delivery.Payload); err != nil {
		return entity.NotificationDelivery{}, fmt.Errorf("decode payload: %w", err)
	}

	if leaseExpiresAt.Valid {
		delivery.LeaseExpiresAt = leaseExpiresAt.Time
	}

	if deadlineAt.Valid {
		delivery.DeliveryDeadlineAt = deadlineAt.Time
	}

	return delivery, nil
}

func incrementNotificationMetric(
	ctx context.Context,
	tx pgx.Tx,
	metricKind,
	notificationType,
	result,
	reasonCode string,
	delta int64,
	at time.Time,
) error {
	const query = `
INSERT INTO notification_delivery_metric_totals (
    metric_kind, notification_type, result, reason_code, total, updated_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (metric_kind, notification_type, result, reason_code)
DO UPDATE SET total = notification_delivery_metric_totals.total + EXCLUDED.total,
              updated_at = EXCLUDED.updated_at`
	if _, err := tx.Exec(ctx, query, metricKind, notificationType, result, reasonCode, delta, at); err != nil {
		return fmt.Errorf("PersonalRepo - incrementNotificationMetric: %w", err)
	}

	return nil
}

func nullableDeliveryTimeArg(value time.Time) any {
	if value.IsZero() {
		return nil
	}

	return value
}

func nullableIntArg(value int) any {
	if value == 0 {
		return nil
	}

	return value
}

func nullableRetryAfterSeconds(value time.Duration) any {
	if value <= 0 {
		return nil
	}

	seconds := int(value.Round(time.Second) / time.Second)
	maxSeconds := int(maxStoredRetryAfter / time.Second)

	if seconds > maxSeconds {
		seconds = maxSeconds
	}

	return seconds
}
