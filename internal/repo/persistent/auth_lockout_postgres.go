package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/jackc/pgx/v5"
)

// GetAuthLoginLockout returns the lockout state for one login key, or the
// zero value when no row exists.
func (r *UserRepo) GetAuthLoginLockout(ctx context.Context, keyHash string) (entity.AuthLoginLockout, error) {
	const query = `
SELECT key_hash, consecutive_failures, locked_until, last_failure_at
FROM auth_login_lockouts
WHERE key_hash = $1`

	var lockout entity.AuthLoginLockout

	err := r.Pool.QueryRow(ctx, query, keyHash).Scan(
		&lockout.KeyHash,
		&lockout.ConsecutiveFailures,
		&lockout.LockedUntil,
		&lockout.LastFailureAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.AuthLoginLockout{}, nil
		}

		return entity.AuthLoginLockout{}, fmt.Errorf("UserRepo - GetAuthLoginLockout - QueryRow: %w", err)
	}

	return lockout, nil
}

// IncrementAuthLoginFailure upserts the failure counter and applies the
// lockout deadline when non-nil, returning the new consecutive failure count.
func (r *UserRepo) IncrementAuthLoginFailure(
	ctx context.Context,
	keyHash string,
	lockedUntil *time.Time,
) (int, error) {
	const query = `
INSERT INTO auth_login_lockouts (
    key_hash,
    consecutive_failures,
    locked_until,
    last_failure_at,
    created_at,
    updated_at
) VALUES ($1, 1, $2, now(), now(), now())
ON CONFLICT (key_hash)
DO UPDATE SET
    consecutive_failures = auth_login_lockouts.consecutive_failures + 1,
    locked_until = EXCLUDED.locked_until,
    last_failure_at = now(),
    updated_at = now()
RETURNING consecutive_failures`

	var failures int
	if err := r.Pool.QueryRow(ctx, query, keyHash, lockedUntil).Scan(&failures); err != nil {
		return 0, fmt.Errorf("UserRepo - IncrementAuthLoginFailure - QueryRow: %w", err)
	}

	return failures, nil
}

// ResetAuthLoginLockout clears the failure counter after a successful login.
func (r *UserRepo) ResetAuthLoginLockout(ctx context.Context, keyHash string) error {
	if _, err := r.Pool.Exec(ctx, "DELETE FROM auth_login_lockouts WHERE key_hash = $1", keyHash); err != nil {
		return fmt.Errorf("UserRepo - ResetAuthLoginLockout - r.Pool.Exec: %w", err)
	}

	return nil
}
