package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MFA persistence (A-3): TOTP enrollment state, one-time recovery codes,
// short-lived challenges, and the session step-up stamp. Implemented on
// UserRepo so the auth domain shares one pool/builder.

// UpsertPendingMFA writes a not-yet-confirmed enrollment; re-enrolling while
// pending rotates the secret in place. A confirmed enrollment is never
// overwritten (disable first).
func (r *UserRepo) UpsertPendingMFA(ctx context.Context, userID, secretEnc string) error {
	const query = `
INSERT INTO user_mfa (user_id, totp_secret_enc, last_used_totp_step, confirmed_at, created_at, updated_at)
VALUES ($1, $2, 0, NULL, now(), now())
ON CONFLICT (user_id) DO UPDATE
SET totp_secret_enc = EXCLUDED.totp_secret_enc,
    last_used_totp_step = 0,
    updated_at = now()
WHERE user_mfa.confirmed_at IS NULL`

	tag, err := r.Pool.Exec(ctx, query, userID, secretEnc)
	if err != nil {
		return fmt.Errorf("UserRepo - UpsertPendingMFA - r.Pool.Exec: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrMFAAlreadyEnabled
	}

	return nil
}

// GetMFA returns one user's MFA row.
func (r *UserRepo) GetMFA(ctx context.Context, userID string) (entity.UserMFA, error) {
	const query = `
SELECT user_id, totp_secret_enc, last_used_totp_step, confirmed_at, created_at, updated_at
FROM user_mfa
WHERE user_id = $1`

	var mfa entity.UserMFA

	err := r.Pool.QueryRow(ctx, query, userID).Scan(
		&mfa.UserID,
		&mfa.TOTPSecretEnc,
		&mfa.LastUsedTOTPStep,
		&mfa.ConfirmedAt,
		&mfa.CreatedAt,
		&mfa.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.UserMFA{}, entity.ErrMFANotEnabled
		}

		return entity.UserMFA{}, fmt.Errorf("UserRepo - GetMFA - QueryRow: %w", err)
	}

	return mfa, nil
}

// ConfirmMFA activates a pending enrollment.
func (r *UserRepo) ConfirmMFA(ctx context.Context, userID string) error {
	const query = `
UPDATE user_mfa
SET confirmed_at = now(),
    updated_at = now()
WHERE user_id = $1
  AND confirmed_at IS NULL`

	tag, err := r.Pool.Exec(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("UserRepo - ConfirmMFA - r.Pool.Exec: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrMFAEnrollmentNotStarted
	}

	return nil
}

// AdvanceMFATOTPStep records the accepted TOTP step, strictly monotonic: a
// repeat or older step means replay (or a concurrent double-submit losing the
// race) and is rejected.
func (r *UserRepo) AdvanceMFATOTPStep(ctx context.Context, userID string, step int64) error {
	const query = `
UPDATE user_mfa
SET last_used_totp_step = $2,
    updated_at = now()
WHERE user_id = $1
  AND last_used_totp_step < $2`

	tag, err := r.Pool.Exec(ctx, query, userID, step)
	if err != nil {
		return fmt.Errorf("UserRepo - AdvanceMFATOTPStep - r.Pool.Exec: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrInvalidMFACode
	}

	return nil
}

// DeleteMFA removes the enrollment and every recovery code.
func (r *UserRepo) DeleteMFA(ctx context.Context, userID string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - DeleteMFA - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, "DELETE FROM user_mfa_recovery_codes WHERE user_id = $1", userID); err != nil {
		return fmt.Errorf("UserRepo - DeleteMFA - codes Exec: %w", err)
	}

	if _, err = tx.Exec(ctx, "DELETE FROM user_mfa WHERE user_id = $1", userID); err != nil {
		return fmt.Errorf("UserRepo - DeleteMFA - mfa Exec: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - DeleteMFA - tx.Commit: %w", err)
	}

	return nil
}

// ReplaceRecoveryCodes swaps the full recovery-code set atomically.
func (r *UserRepo) ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - ReplaceRecoveryCodes - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err = tx.Exec(ctx, "DELETE FROM user_mfa_recovery_codes WHERE user_id = $1", userID); err != nil {
		return fmt.Errorf("UserRepo - ReplaceRecoveryCodes - delete Exec: %w", err)
	}

	const insertQuery = `
INSERT INTO user_mfa_recovery_codes (id, user_id, code_hash, created_at)
VALUES ($1, $2, $3, now())`

	for _, codeHash := range codeHashes {
		if _, err = tx.Exec(ctx, insertQuery, uuid.NewString(), userID, codeHash); err != nil {
			return fmt.Errorf("UserRepo - ReplaceRecoveryCodes - insert Exec: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - ReplaceRecoveryCodes - tx.Commit: %w", err)
	}

	return nil
}

// ConsumeRecoveryCode spends one unused code, atomically (AC-3: the guarded
// UPDATE makes a second use — or a concurrent double-spend — match zero rows).
func (r *UserRepo) ConsumeRecoveryCode(ctx context.Context, userID, codeHash string) error {
	const query = `
UPDATE user_mfa_recovery_codes
SET used_at = now()
WHERE user_id = $1
  AND code_hash = $2
  AND used_at IS NULL`

	tag, err := r.Pool.Exec(ctx, query, userID, codeHash)
	if err != nil {
		return fmt.Errorf("UserRepo - ConsumeRecoveryCode - r.Pool.Exec: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrInvalidMFACode
	}

	return nil
}

// CountUnusedRecoveryCodes returns how many codes remain spendable.
func (r *UserRepo) CountUnusedRecoveryCodes(ctx context.Context, userID string) (int, error) {
	const query = `
SELECT count(*)
FROM user_mfa_recovery_codes
WHERE user_id = $1
  AND used_at IS NULL`

	var count int
	if err := r.Pool.QueryRow(ctx, query, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("UserRepo - CountUnusedRecoveryCodes - QueryRow: %w", err)
	}

	return count, nil
}

// CreateMFAChallenge stores one short-lived second-factor challenge.
func (r *UserRepo) CreateMFAChallenge(ctx context.Context, challenge entity.MFAChallenge) error { //nolint:gocritic // value param fixed by the repo interface contract
	const query = `
INSERT INTO mfa_challenges (
    id, user_id, purpose, token_hash, otp_hash, otp_expires_at,
    expires_at, consumed_at, client_ip, user_agent, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, NULL, $8, $9, now())`

	_, err := r.Pool.Exec(ctx, query,
		challenge.ID,
		challenge.UserID,
		challenge.Purpose,
		challenge.TokenHash,
		challenge.OTPHash,
		challenge.OTPExpiresAt,
		challenge.ExpiresAt,
		nullableStringArg(challenge.ClientIP),
		nullableStringArg(challenge.UserAgent),
	)
	if err != nil {
		return fmt.Errorf("UserRepo - CreateMFAChallenge - r.Pool.Exec: %w", err)
	}

	return nil
}

// GetMFAChallengeByTokenHash returns the live challenge for one token hash.
func (r *UserRepo) GetMFAChallengeByTokenHash(
	ctx context.Context,
	tokenHash, purpose string,
) (entity.MFAChallenge, error) {
	const query = `
SELECT id, user_id, purpose, token_hash, otp_hash, otp_expires_at,
       expires_at, consumed_at, client_ip, user_agent, created_at
FROM mfa_challenges
WHERE token_hash = $1
  AND purpose = $2
  AND consumed_at IS NULL
  AND expires_at > now()`

	var (
		challenge entity.MFAChallenge
		clientIP  *string
		userAgent *string
	)

	err := r.Pool.QueryRow(ctx, query, tokenHash, purpose).Scan(
		&challenge.ID,
		&challenge.UserID,
		&challenge.Purpose,
		&challenge.TokenHash,
		&challenge.OTPHash,
		&challenge.OTPExpiresAt,
		&challenge.ExpiresAt,
		&challenge.ConsumedAt,
		&clientIP,
		&userAgent,
		&challenge.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.MFAChallenge{}, entity.ErrInvalidMFAChallenge
		}

		return entity.MFAChallenge{}, fmt.Errorf("UserRepo - GetMFAChallengeByTokenHash - QueryRow: %w", err)
	}

	if clientIP != nil {
		challenge.ClientIP = *clientIP
	}

	if userAgent != nil {
		challenge.UserAgent = *userAgent
	}

	return challenge, nil
}

// ConsumeMFAChallenge spends a challenge exactly once; losing a concurrent
// race (or racing expiry) reads as an invalid challenge.
func (r *UserRepo) ConsumeMFAChallenge(ctx context.Context, id string) error {
	const query = `
UPDATE mfa_challenges
SET consumed_at = now()
WHERE id = $1
  AND consumed_at IS NULL
  AND expires_at > now()`

	tag, err := r.Pool.Exec(ctx, query, id)
	if err != nil {
		return fmt.Errorf("UserRepo - ConsumeMFAChallenge - r.Pool.Exec: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrInvalidMFAChallenge
	}

	return nil
}

// SetSessionMFAVerified stamps the active session row of one family.
func (r *UserRepo) SetSessionMFAVerified(ctx context.Context, userID, familyID string, at time.Time) error {
	const query = `
UPDATE auth_sessions
SET mfa_verified_at = $3
WHERE user_id = $1
  AND family_id = $2
  AND revoked_at IS NULL`

	tag, err := r.Pool.Exec(ctx, query, userID, familyID, at)
	if err != nil {
		return fmt.Errorf("UserRepo - SetSessionMFAVerified - r.Pool.Exec: %w", err)
	}

	if tag.RowsAffected() == 0 {
		return entity.ErrAuthSessionNotFound
	}

	return nil
}

// GetMFAGateData is the one read behind the step-up gate: grace anchor,
// enrollment state, and the active session's freshness stamp.
func (r *UserRepo) GetMFAGateData(ctx context.Context, userID, familyID string) (entity.MFAGateData, error) {
	const query = `
SELECT u.mfa_enforced_from,
       m.user_id IS NOT NULL AND m.confirmed_at IS NOT NULL AS confirmed,
       m.user_id IS NOT NULL AND m.confirmed_at IS NULL AS pending,
       s.mfa_verified_at
FROM users u
LEFT JOIN user_mfa m ON m.user_id = u.id
LEFT JOIN LATERAL (
    SELECT mfa_verified_at
    FROM auth_sessions
    WHERE user_id = u.id
      AND family_id = $2
      AND revoked_at IS NULL
    ORDER BY created_at DESC
    LIMIT 1
) s ON true
WHERE u.id = $1
  AND u.deleted_at IS NULL`

	var data entity.MFAGateData

	err := r.Pool.QueryRow(ctx, query, userID, familyID).Scan(
		&data.EnforcedFrom,
		&data.Confirmed,
		&data.Pending,
		&data.MFAVerifiedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.MFAGateData{}, entity.ErrUserNotFound
		}

		return entity.MFAGateData{}, fmt.Errorf("UserRepo - GetMFAGateData - QueryRow: %w", err)
	}

	return data, nil
}
