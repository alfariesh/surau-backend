package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
)

// checkLoginLockout rejects logins for a key that is currently locked out.
// Fail-closed on repo errors, matching the rate limiter's philosophy.
func (uc *UseCase) checkLoginLockout(ctx context.Context, email string) error {
	if !uc.lockoutEnabled() {
		return nil
	}

	lockout, err := uc.lockout.GetAuthLoginLockout(ctx, lockoutKeyHash(email))
	if err != nil {
		return fmt.Errorf("UserUseCase - checkLoginLockout - GetAuthLoginLockout: %w", err)
	}

	if lockout.LockedUntil != nil && time.Now().UTC().Before(*lockout.LockedUntil) {
		return entity.ErrAccountLocked
	}

	return nil
}

// recordLoginFailure increments the consecutive-failure counter and applies
// an escalating lockout once the threshold is crossed. Failures are tracked
// per email key whether or not the account exists, so the lockout cannot be
// used to enumerate accounts. Best-effort: errors only surface in audit logs
// via the absence of a lock, never to the caller.
func (uc *UseCase) recordLoginFailure(ctx context.Context, email string) {
	if !uc.lockoutEnabled() {
		return
	}

	keyHash := lockoutKeyHash(email)

	lockout, err := uc.lockout.GetAuthLoginLockout(ctx, keyHash)
	if err != nil {
		return
	}

	var lockedUntil *time.Time

	if duration := uc.lockoutDuration(lockout.ConsecutiveFailures + 1); duration > 0 {
		deadline := time.Now().UTC().Add(duration)
		lockedUntil = &deadline
	}

	_, _ = uc.lockout.IncrementAuthLoginFailure(ctx, keyHash, lockedUntil) //nolint:errcheck // lockout bookkeeping must not fail the login flow
}

// resetLoginLockout clears the failure counter after a successful login.
func (uc *UseCase) resetLoginLockout(ctx context.Context, email string) {
	if !uc.lockoutEnabled() {
		return
	}

	_ = uc.lockout.ResetAuthLoginLockout(ctx, lockoutKeyHash(email)) //nolint:errcheck // lockout bookkeeping must not fail the login flow
}

// lockoutDuration returns the lockout length for the given consecutive
// failure count: 0 below the threshold, then Base × Factor^(tier-1) capped at
// MaxDuration, where the tier grows every Threshold failures.
func (uc *UseCase) lockoutDuration(failures int) time.Duration {
	opts := uc.lockoutOptions
	if failures < opts.Threshold {
		return 0
	}

	duration := opts.BaseDuration

	tier := failures / opts.Threshold
	for i := 1; i < tier; i++ {
		duration *= time.Duration(opts.Factor)
		if duration >= opts.MaxDuration {
			return opts.MaxDuration
		}
	}

	if duration > opts.MaxDuration {
		return opts.MaxDuration
	}

	return duration
}

func (uc *UseCase) lockoutEnabled() bool {
	return uc.lockoutOptions.Enabled && uc.lockout != nil
}

func lockoutKeyHash(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	hash := sha256.Sum256([]byte("login_lockout\x00email\x00" + normalized))

	return hex.EncodeToString(hash[:])
}
