package persistent

import (
	"context"
	"fmt"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
)

// lockoutStaleWindow is how long after the last failed login a lockout counter is
// considered stale and eligible for cleanup.
const lockoutStaleWindow = 24 * time.Hour

// CleanupAuthData deletes expired auth rows in one pass: spent rate-limit
// windows, used/expired one-time tokens past retention, dead sessions past
// retention, stale lockout counters, expired notification cooldowns, and —
// only when a retention is configured — old audit logs.
//
//nolint:funlen // linear sequence of per-table cleanup statements
func (r *UserRepo) CleanupAuthData(
	ctx context.Context,
	policy repo.AuthCleanupPolicy,
) (entity.AuthCleanupResult, error) {
	result := entity.AuthCleanupResult{}
	now := policy.Now
	tokenCutoff := now.Add(-policy.TokenRetention)
	sessionCutoff := now.Add(-policy.SessionRetention)

	steps := []struct {
		count *int64
		query string
		args  []any
	}{
		{
			&result.RateLimits,
			"DELETE FROM auth_rate_limits WHERE expires_at <= $1",
			[]any{now},
		},
		{
			&result.VerificationTokens,
			"DELETE FROM email_verification_tokens WHERE (used_at IS NOT NULL OR expires_at <= $1) AND created_at <= $2",
			[]any{now, tokenCutoff},
		},
		{
			&result.PasswordResetTokens,
			"DELETE FROM password_reset_tokens WHERE (used_at IS NOT NULL OR expires_at <= $1) AND created_at <= $2",
			[]any{now, tokenCutoff},
		},
		{
			&result.EmailChangeTokens,
			"DELETE FROM email_change_tokens WHERE (used_at IS NOT NULL OR expires_at <= $1) AND created_at <= $2",
			[]any{now, tokenCutoff},
		},
		{
			&result.Sessions,
			"DELETE FROM auth_sessions WHERE COALESCE(revoked_at, expires_at) <= $1",
			[]any{sessionCutoff},
		},
		{
			&result.Lockouts,
			"DELETE FROM auth_login_lockouts WHERE last_failure_at <= $1 AND (locked_until IS NULL OR locked_until <= $2)",
			[]any{now.Add(-lockoutStaleWindow), now},
		},
		{
			&result.NotificationCooldowns,
			"DELETE FROM auth_notification_cooldowns WHERE expires_at <= $1",
			[]any{now},
		},
	}
	if policy.AuditRetention > 0 {
		steps = append(steps, struct {
			count *int64
			query string
			args  []any
		}{
			&result.AuditLogs,
			"DELETE FROM auth_audit_logs WHERE created_at <= $1",
			[]any{now.Add(-policy.AuditRetention)},
		})
	}

	for _, step := range steps {
		tag, err := r.Pool.Exec(ctx, step.query, step.args...)
		if err != nil {
			return result, fmt.Errorf("UserRepo - CleanupAuthData - r.Pool.Exec: %w", err)
		}

		*step.count = tag.RowsAffected()
	}

	return result, nil
}
