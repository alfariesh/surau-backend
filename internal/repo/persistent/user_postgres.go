package persistent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// UserRepo -.
type UserRepo struct {
	*postgres.Postgres
}

const userReturningColumns = "id, username, email, role, password_hash, email_verified, token_version, created_at, updated_at"

// NewUserRepo -.
func NewUserRepo(pg *postgres.Postgres) *UserRepo {
	return &UserRepo{pg}
}

// Store -.
func (r *UserRepo) Store(ctx context.Context, user *entity.User) error {
	sql, args, err := r.Builder.
		Insert("users").
		Columns("id, username, email, role, password_hash, email_verified, token_version, created_at, updated_at").
		Values(
			user.ID,
			user.Username,
			user.Email,
			user.Role,
			user.PasswordHash,
			user.EmailVerified,
			user.TokenVersion,
			user.CreatedAt,
			user.UpdatedAt,
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - Store - r.Builder: %w", err)
	}

	_, err = r.Pool.Exec(ctx, sql, args...)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return entity.ErrUserAlreadyExists
		}

		return fmt.Errorf("UserRepo - Store - r.Pool.Exec: %w", err)
	}

	return nil
}

// StoreWithVerificationToken stores a new user and initial email verification token atomically.
func (r *UserRepo) StoreWithVerificationToken(
	ctx context.Context,
	user *entity.User,
	token *entity.EmailVerificationToken,
) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - StoreWithVerificationToken - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	userSQL, userArgs, err := r.Builder.
		Insert("users").
		Columns("id, username, email, role, password_hash, email_verified, token_version, created_at, updated_at").
		Values(
			user.ID,
			user.Username,
			user.Email,
			user.Role,
			user.PasswordHash,
			user.EmailVerified,
			user.TokenVersion,
			user.CreatedAt,
			user.UpdatedAt,
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - StoreWithVerificationToken - user Builder: %w", err)
	}

	if _, err = tx.Exec(ctx, userSQL, userArgs...); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return entity.ErrUserAlreadyExists
		}

		return fmt.Errorf("UserRepo - StoreWithVerificationToken - insert user: %w", err)
	}

	tokenSQL, tokenArgs, err := r.emailVerificationTokenInsert(token)
	if err != nil {
		return fmt.Errorf("UserRepo - StoreWithVerificationToken - token Builder: %w", err)
	}

	if _, err = tx.Exec(ctx, tokenSQL, tokenArgs...); err != nil {
		return fmt.Errorf("UserRepo - StoreWithVerificationToken - insert token: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - StoreWithVerificationToken - tx.Commit: %w", err)
	}

	return nil
}

// GetByID -.
func (r *UserRepo) GetByID(ctx context.Context, id string) (entity.User, error) {
	return r.getUser(ctx, "id", id)
}

// GetByEmail -.
func (r *UserRepo) GetByEmail(ctx context.Context, email string) (entity.User, error) {
	return r.getUser(ctx, "email", email)
}

// SetRoleByEmail updates one user's role by email.
func (r *UserRepo) SetRoleByEmail(ctx context.Context, email, role string) (entity.User, error) {
	sqlText, args, err := r.Builder.
		Update("users").
		Set("role", role).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"email": email}).
		Suffix("RETURNING " + userReturningColumns).
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - SetRoleByEmail - r.Builder: %w", err)
	}

	var user entity.User
	err = r.Pool.QueryRow(ctx, sqlText, args...).
		Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.Role,
			&user.PasswordHash,
			&user.EmailVerified,
			&user.TokenVersion,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.User{}, entity.ErrUserNotFound
		}

		return entity.User{}, fmt.Errorf("UserRepo - SetRoleByEmail - r.Pool.QueryRow: %w", err)
	}

	return user, nil
}

// ChangePassword updates a password and increments token_version atomically.
func (r *UserRepo) ChangePassword(ctx context.Context, userID, passwordHash string) (entity.User, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ChangePassword - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	userSQL, userArgs, err := r.Builder.
		Update("users").
		Set("password_hash", passwordHash).
		Set("token_version", sq.Expr("token_version + 1")).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": userID}).
		Suffix("RETURNING " + userReturningColumns).
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ChangePassword - user Builder: %w", err)
	}

	var user entity.User
	err = tx.QueryRow(ctx, userSQL, userArgs...).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Role,
		&user.PasswordHash,
		&user.EmailVerified,
		&user.TokenVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.User{}, entity.ErrUserNotFound
		}

		return entity.User{}, fmt.Errorf("UserRepo - ChangePassword - user QueryRow: %w", err)
	}

	revokeSQL, revokeArgs, err := r.Builder.
		Update("password_reset_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ChangePassword - revoke Builder: %w", err)
	}
	if _, err = tx.Exec(ctx, revokeSQL, revokeArgs...); err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ChangePassword - revoke Exec: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ChangePassword - tx.Commit: %w", err)
	}

	return user, nil
}

// ReplaceVerificationToken revokes previous unused tokens and stores a new one.
func (r *UserRepo) ReplaceVerificationToken(ctx context.Context, token *entity.EmailVerificationToken) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - ReplaceVerificationToken - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	sqlText, args, err := r.Builder.
		Update("email_verification_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": token.UserID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - ReplaceVerificationToken - revoke Builder: %w", err)
	}

	if _, err = tx.Exec(ctx, sqlText, args...); err != nil {
		return fmt.Errorf("UserRepo - ReplaceVerificationToken - revoke: %w", err)
	}

	tokenSQL, tokenArgs, err := r.emailVerificationTokenInsert(token)
	if err != nil {
		return fmt.Errorf("UserRepo - ReplaceVerificationToken - insert Builder: %w", err)
	}

	if _, err = tx.Exec(ctx, tokenSQL, tokenArgs...); err != nil {
		return fmt.Errorf("UserRepo - ReplaceVerificationToken - insert: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - ReplaceVerificationToken - tx.Commit: %w", err)
	}

	return nil
}

// RevokeUnusedVerificationTokens marks all currently unused verification tokens as used.
func (r *UserRepo) RevokeUnusedVerificationTokens(ctx context.Context, userID string) error {
	sqlText, args, err := r.Builder.
		Update("email_verification_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - RevokeUnusedVerificationTokens - Builder: %w", err)
	}

	if _, err = r.Pool.Exec(ctx, sqlText, args...); err != nil {
		return fmt.Errorf("UserRepo - RevokeUnusedVerificationTokens - Exec: %w", err)
	}

	return nil
}

// GetVerificationTokenByHash finds a verification token by its SHA-256 hash.
func (r *UserRepo) GetVerificationTokenByHash(ctx context.Context, tokenHash string) (entity.EmailVerificationToken, error) {
	sqlText, args, err := r.Builder.
		Select("id, user_id, token_hash, expires_at, used_at, sent_at, created_at").
		From("email_verification_tokens").
		Where(sq.Eq{"token_hash": tokenHash}).
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailVerificationToken{}, fmt.Errorf("UserRepo - GetVerificationTokenByHash - Builder: %w", err)
	}

	token, err := r.scanEmailVerificationToken(ctx, sqlText, args...)
	if err != nil {
		return entity.EmailVerificationToken{}, err
	}

	return token, nil
}

// GetLatestUnusedVerificationToken returns the most recent unused token for cooldown checks.
func (r *UserRepo) GetLatestUnusedVerificationToken(ctx context.Context, userID string) (entity.EmailVerificationToken, error) {
	sqlText, args, err := r.Builder.
		Select("id, user_id, token_hash, expires_at, used_at, sent_at, created_at").
		From("email_verification_tokens").
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		OrderBy("sent_at DESC").
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailVerificationToken{}, fmt.Errorf("UserRepo - GetLatestUnusedVerificationToken - Builder: %w", err)
	}

	token, err := r.scanEmailVerificationToken(ctx, sqlText, args...)
	if err != nil {
		return entity.EmailVerificationToken{}, err
	}

	return token, nil
}

// VerifyEmailWithToken marks a token used and the owning user verified atomically.
func (r *UserRepo) VerifyEmailWithToken(ctx context.Context, tokenID, userID string) (entity.User, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - VerifyEmailWithToken - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	tokenSQL, tokenArgs, err := r.Builder.
		Update("email_verification_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"id": tokenID, "user_id": userID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - VerifyEmailWithToken - token Builder: %w", err)
	}

	tag, err := tx.Exec(ctx, tokenSQL, tokenArgs...)
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - VerifyEmailWithToken - token Exec: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return entity.User{}, entity.ErrInvalidVerificationToken
	}

	userSQL, userArgs, err := r.Builder.
		Update("users").
		Set("email_verified", true).
		Set("email_verified_at", sq.Expr("COALESCE(email_verified_at, now())")).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": userID}).
		Suffix("RETURNING " + userReturningColumns).
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - VerifyEmailWithToken - user Builder: %w", err)
	}

	var user entity.User
	err = tx.QueryRow(ctx, userSQL, userArgs...).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Role,
		&user.PasswordHash,
		&user.EmailVerified,
		&user.TokenVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.User{}, entity.ErrUserNotFound
		}

		return entity.User{}, fmt.Errorf("UserRepo - VerifyEmailWithToken - user QueryRow: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - VerifyEmailWithToken - tx.Commit: %w", err)
	}

	return user, nil
}

// ReplacePasswordResetToken revokes previous unused reset tokens and stores a new one.
func (r *UserRepo) ReplacePasswordResetToken(ctx context.Context, token *entity.PasswordResetToken) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - ReplacePasswordResetToken - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	sqlText, args, err := r.Builder.
		Update("password_reset_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": token.UserID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - ReplacePasswordResetToken - revoke Builder: %w", err)
	}

	if _, err = tx.Exec(ctx, sqlText, args...); err != nil {
		return fmt.Errorf("UserRepo - ReplacePasswordResetToken - revoke: %w", err)
	}

	tokenSQL, tokenArgs, err := r.passwordResetTokenInsert(token)
	if err != nil {
		return fmt.Errorf("UserRepo - ReplacePasswordResetToken - insert Builder: %w", err)
	}

	if _, err = tx.Exec(ctx, tokenSQL, tokenArgs...); err != nil {
		return fmt.Errorf("UserRepo - ReplacePasswordResetToken - insert: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - ReplacePasswordResetToken - tx.Commit: %w", err)
	}

	return nil
}

// RevokeUnusedPasswordResetTokens marks all currently unused reset tokens as used.
func (r *UserRepo) RevokeUnusedPasswordResetTokens(ctx context.Context, userID string) error {
	sqlText, args, err := r.Builder.
		Update("password_reset_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - RevokeUnusedPasswordResetTokens - Builder: %w", err)
	}

	if _, err = r.Pool.Exec(ctx, sqlText, args...); err != nil {
		return fmt.Errorf("UserRepo - RevokeUnusedPasswordResetTokens - Exec: %w", err)
	}

	return nil
}

// GetPasswordResetTokenByHash finds a password reset token by its SHA-256 hash.
func (r *UserRepo) GetPasswordResetTokenByHash(ctx context.Context, tokenHash string) (entity.PasswordResetToken, error) {
	sqlText, args, err := r.Builder.
		Select("id, user_id, token_hash, expires_at, used_at, sent_at, created_at").
		From("password_reset_tokens").
		Where(sq.Eq{"token_hash": tokenHash}).
		Limit(1).
		ToSql()
	if err != nil {
		return entity.PasswordResetToken{}, fmt.Errorf("UserRepo - GetPasswordResetTokenByHash - Builder: %w", err)
	}

	token, err := r.scanPasswordResetToken(ctx, sqlText, args...)
	if err != nil {
		return entity.PasswordResetToken{}, err
	}

	return token, nil
}

// GetLatestUnusedPasswordResetToken returns the most recent unused token for cooldown checks.
func (r *UserRepo) GetLatestUnusedPasswordResetToken(ctx context.Context, userID string) (entity.PasswordResetToken, error) {
	sqlText, args, err := r.Builder.
		Select("id, user_id, token_hash, expires_at, used_at, sent_at, created_at").
		From("password_reset_tokens").
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		OrderBy("sent_at DESC").
		Limit(1).
		ToSql()
	if err != nil {
		return entity.PasswordResetToken{}, fmt.Errorf("UserRepo - GetLatestUnusedPasswordResetToken - Builder: %w", err)
	}

	token, err := r.scanPasswordResetToken(ctx, sqlText, args...)
	if err != nil {
		return entity.PasswordResetToken{}, err
	}

	return token, nil
}

// ResetPasswordWithToken marks a reset token used and updates the user's password atomically.
func (r *UserRepo) ResetPasswordWithToken(ctx context.Context, tokenID, userID, passwordHash string) (entity.User, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	tokenSQL, tokenArgs, err := r.Builder.
		Update("password_reset_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"id": tokenID, "user_id": userID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - token Builder: %w", err)
	}

	tag, err := tx.Exec(ctx, tokenSQL, tokenArgs...)
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - token Exec: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return entity.User{}, entity.ErrInvalidPasswordResetToken
	}

	userSQL, userArgs, err := r.Builder.
		Update("users").
		Set("password_hash", passwordHash).
		Set("email_verified", true).
		Set("email_verified_at", sq.Expr("COALESCE(email_verified_at, now())")).
		Set("token_version", sq.Expr("token_version + 1")).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": userID}).
		Suffix("RETURNING " + userReturningColumns).
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - user Builder: %w", err)
	}

	var user entity.User
	err = tx.QueryRow(ctx, userSQL, userArgs...).Scan(
		&user.ID,
		&user.Username,
		&user.Email,
		&user.Role,
		&user.PasswordHash,
		&user.EmailVerified,
		&user.TokenVersion,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.User{}, entity.ErrUserNotFound
		}

		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - user QueryRow: %w", err)
	}

	revokeSQL, revokeArgs, err := r.Builder.
		Update("password_reset_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - revoke Builder: %w", err)
	}
	if _, err = tx.Exec(ctx, revokeSQL, revokeArgs...); err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - revoke Exec: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - tx.Commit: %w", err)
	}

	return user, nil
}

// IncrementAuthRateLimit increments a rate-limit counter for one fixed window.
func (r *UserRepo) IncrementAuthRateLimit(
	ctx context.Context,
	limit entity.AuthRateLimit,
) (entity.AuthRateLimitResult, error) {
	const query = `
INSERT INTO auth_rate_limits (
    action,
    key_hash,
    window_start,
    window_seconds,
    count,
    expires_at,
    created_at,
    updated_at
) VALUES ($1, $2, $3, $4, 1, $5, now(), now())
ON CONFLICT (action, key_hash, window_start)
DO UPDATE SET
    count = auth_rate_limits.count + 1,
    window_seconds = EXCLUDED.window_seconds,
    expires_at = GREATEST(auth_rate_limits.expires_at, EXCLUDED.expires_at),
    updated_at = now()
RETURNING count, expires_at`

	var (
		count     int
		expiresAt time.Time
	)
	err := r.Pool.QueryRow(
		ctx,
		query,
		limit.Action,
		limit.KeyHash,
		limit.WindowStart,
		limit.WindowSeconds,
		limit.ExpiresAt,
	).Scan(&count, &expiresAt)
	if err != nil {
		return entity.AuthRateLimitResult{}, fmt.Errorf("UserRepo - IncrementAuthRateLimit - QueryRow: %w", err)
	}

	retryAfter := time.Until(expiresAt)
	if retryAfter < 0 {
		retryAfter = 0
	}

	return entity.AuthRateLimitResult{
		Allowed:    count <= limit.MaxAttempts,
		Count:      count,
		RetryAfter: retryAfter,
	}, nil
}

// StoreAuthAuditLog stores a sanitized authentication audit event.
func (r *UserRepo) StoreAuthAuditLog(ctx context.Context, log entity.AuthAuditLog) error {
	metadata := log.Metadata
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("UserRepo - StoreAuthAuditLog - json.Marshal: %w", err)
	}

	sqlText, args, err := r.Builder.
		Insert("auth_audit_logs").
		Columns("id, event, status, user_id, email, client_ip, user_agent, error_code, metadata, created_at").
		Values(
			log.ID,
			log.Event,
			log.Status,
			nullableStringArg(log.UserID),
			nullableStringArg(log.Email),
			nullableStringArg(log.ClientIP),
			nullableStringArg(log.UserAgent),
			nullableStringArg(log.ErrorCode),
			string(metadataJSON),
			log.CreatedAt,
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - StoreAuthAuditLog - Builder: %w", err)
	}

	if _, err = r.Pool.Exec(ctx, sqlText, args...); err != nil {
		return fmt.Errorf("UserRepo - StoreAuthAuditLog - Exec: %w", err)
	}

	return nil
}

// RecordAuthLoginFingerprint stores a login fingerprint and reports whether it is new.
func (r *UserRepo) RecordAuthLoginFingerprint(
	ctx context.Context,
	fingerprint entity.AuthLoginFingerprint,
) (bool, error) {
	const query = `
WITH inserted AS (
    INSERT INTO auth_login_fingerprints (
        user_id,
        fingerprint_hash,
        client_ip,
        user_agent,
        first_seen_at,
        last_seen_at
    ) VALUES ($1, $2, $3, $4, $5, $5)
    ON CONFLICT (user_id, fingerprint_hash) DO NOTHING
    RETURNING true AS is_new
), updated AS (
    UPDATE auth_login_fingerprints
    SET
        client_ip = $3,
        user_agent = $4,
        last_seen_at = $5
    WHERE user_id = $1
        AND fingerprint_hash = $2
        AND NOT EXISTS (SELECT 1 FROM inserted)
    RETURNING false AS is_new
)
SELECT is_new FROM inserted
UNION ALL
SELECT is_new FROM updated`

	var isNew bool
	err := r.Pool.QueryRow(
		ctx,
		query,
		fingerprint.UserID,
		fingerprint.FingerprintHash,
		nullableStringArg(fingerprint.ClientIP),
		nullableStringArg(fingerprint.UserAgent),
		fingerprint.SeenAt,
	).Scan(&isNew)
	if err != nil {
		return false, fmt.Errorf("UserRepo - RecordAuthLoginFingerprint - QueryRow: %w", err)
	}

	return isNew, nil
}

// AcquireAuthNotificationCooldown atomically acquires one notification cooldown slot.
func (r *UserRepo) AcquireAuthNotificationCooldown(
	ctx context.Context,
	cooldown entity.AuthNotificationCooldown,
) (bool, error) {
	const query = `
INSERT INTO auth_notification_cooldowns (
    event,
    key_hash,
    expires_at,
    created_at,
    updated_at
) VALUES ($1, $2, $3, now(), now())
ON CONFLICT (event, key_hash)
DO UPDATE SET
    expires_at = EXCLUDED.expires_at,
    updated_at = now()
WHERE auth_notification_cooldowns.expires_at <= now()
RETURNING true`

	var acquired bool
	err := r.Pool.QueryRow(
		ctx,
		query,
		cooldown.Event,
		cooldown.KeyHash,
		cooldown.ExpiresAt,
	).Scan(&acquired)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}

		return false, fmt.Errorf("UserRepo - AcquireAuthNotificationCooldown - QueryRow: %w", err)
	}

	return acquired, nil
}

func (r *UserRepo) getUser(ctx context.Context, column, value string) (entity.User, error) {
	sql, args, err := r.Builder.
		Select(userReturningColumns).
		From("users").
		Where(sq.Eq{column: value}).
		ToSql()
	if err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - getUser - r.Builder: %w", err)
	}

	var user entity.User

	err = r.Pool.QueryRow(ctx, sql, args...).
		Scan(
			&user.ID,
			&user.Username,
			&user.Email,
			&user.Role,
			&user.PasswordHash,
			&user.EmailVerified,
			&user.TokenVersion,
			&user.CreatedAt,
			&user.UpdatedAt,
		)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.User{}, entity.ErrUserNotFound
		}

		return entity.User{}, fmt.Errorf("UserRepo - getUser - r.Pool.QueryRow: %w", err)
	}

	return user, nil
}

func (r *UserRepo) emailVerificationTokenInsert(token *entity.EmailVerificationToken) (string, []any, error) {
	return r.Builder.
		Insert("email_verification_tokens").
		Columns("id, user_id, token_hash, expires_at, used_at, sent_at, created_at").
		Values(token.ID, token.UserID, token.TokenHash, token.ExpiresAt, token.UsedAt, token.SentAt, token.CreatedAt).
		ToSql()
}

func (r *UserRepo) passwordResetTokenInsert(token *entity.PasswordResetToken) (string, []any, error) {
	return r.Builder.
		Insert("password_reset_tokens").
		Columns("id, user_id, token_hash, expires_at, used_at, sent_at, created_at").
		Values(token.ID, token.UserID, token.TokenHash, token.ExpiresAt, token.UsedAt, token.SentAt, token.CreatedAt).
		ToSql()
}

func (r *UserRepo) scanEmailVerificationToken(
	ctx context.Context,
	sqlText string,
	args ...any,
) (entity.EmailVerificationToken, error) {
	var (
		token  entity.EmailVerificationToken
		usedAt sql.NullTime
	)

	err := r.Pool.QueryRow(ctx, sqlText, args...).Scan(
		&token.ID,
		&token.UserID,
		&token.TokenHash,
		&token.ExpiresAt,
		&usedAt,
		&token.SentAt,
		&token.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailVerificationToken{}, entity.ErrVerificationTokenNotFound
		}

		return entity.EmailVerificationToken{}, fmt.Errorf("UserRepo - scanEmailVerificationToken - QueryRow: %w", err)
	}
	if usedAt.Valid {
		token.UsedAt = &usedAt.Time
	}

	return token, nil
}

func nullableStringArg(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	return value
}

func (r *UserRepo) scanPasswordResetToken(
	ctx context.Context,
	sqlText string,
	args ...any,
) (entity.PasswordResetToken, error) {
	var (
		token  entity.PasswordResetToken
		usedAt sql.NullTime
	)

	err := r.Pool.QueryRow(ctx, sqlText, args...).Scan(
		&token.ID,
		&token.UserID,
		&token.TokenHash,
		&token.ExpiresAt,
		&usedAt,
		&token.SentAt,
		&token.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.PasswordResetToken{}, entity.ErrPasswordResetTokenNotFound
		}

		return entity.PasswordResetToken{}, fmt.Errorf("UserRepo - scanPasswordResetToken - QueryRow: %w", err)
	}
	if usedAt.Valid {
		token.UsedAt = &usedAt.Time
	}

	return token, nil
}
