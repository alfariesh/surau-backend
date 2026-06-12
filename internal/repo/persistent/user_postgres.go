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
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// UserRepo -.
type UserRepo struct {
	*postgres.Postgres
}

const (
	userReturningColumns     = "id, username, email, role, password_hash, email_verified, token_version, created_at, updated_at"
	userAccountSelectColumns = `
    u.id,
    u.username,
    u.email,
    u.role,
    u.password_hash,
    u.email_verified,
    u.token_version,
    u.created_at,
    u.updated_at,
    p.display_name,
    p.timezone,
    p.country_code,
    COALESCE(p.onboarding_version, 1),
    p.onboarding_completed_at,
    COALESCE(p.personalization_enabled, TRUE),
    COALESCE(p.created_at, u.created_at),
    COALESCE(p.updated_at, u.updated_at),
    COALESCE(pref.preferred_ui_lang, 'id'),
    COALESCE(pref.preferred_content_lang, 'id'),
    COALESCE(pref.fallback_langs, ARRAY['id']::TEXT[]),
    COALESCE(pref.arabic_level, 'none'),
    COALESCE(pref.reader_mode, 'arabic_translation'),
    COALESCE(pref.interests, ARRAY[]::TEXT[]),
    pref.daily_goal_minutes,
    pref.quran_translation_source_id,
    pref.quran_recitation_id,
    COALESCE(pref.created_at, u.created_at),
    COALESCE(pref.updated_at, u.updated_at)`
)

// NewUserRepo -.
func NewUserRepo(pg *postgres.Postgres) *UserRepo {
	return &UserRepo{pg}
}

// Store -.
func (r *UserRepo) Store(ctx context.Context, user *entity.User) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - Store - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

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

	_, err = tx.Exec(ctx, sql, args...)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return entity.ErrUserAlreadyExists
		}

		return fmt.Errorf("UserRepo - Store - tx.Exec: %w", err)
	}

	if err = r.insertDefaultProfileAndPreferences(ctx, tx, user.ID, user.Username, user.CreatedAt); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - Store - tx.Commit: %w", err)
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

	if err = r.insertDefaultProfileAndPreferences(ctx, tx, user.ID, user.Username, user.CreatedAt); err != nil {
		return err
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

// GetAccount returns the auth user plus product profile and preferences.
func (r *UserRepo) GetAccount(ctx context.Context, userID string) (entity.UserAccount, error) {
	const query = `
SELECT
` + userAccountSelectColumns + `
FROM users u
LEFT JOIN user_profiles p ON p.user_id = u.id
LEFT JOIN user_preferences pref ON pref.user_id = u.id
WHERE u.id = $1
    AND u.deleted_at IS NULL`

	account, err := r.scanUserAccount(ctx, query, userID)
	if err != nil {
		return entity.UserAccount{}, err
	}

	return account, nil
}

// ListAccounts returns admin-visible accounts.
func (r *UserRepo) ListAccounts(
	ctx context.Context,
	filter repo.UserFilter,
) ([]entity.UserAccount, int, error) {
	countBuilder := r.Builder.
		Select("COUNT(*)").
		From("users u").
		LeftJoin("user_profiles p ON p.user_id = u.id").
		Where("u.deleted_at IS NULL")
	dataBuilder := r.Builder.
		Select(userAccountSelectColumns).
		From("users u").
		LeftJoin("user_profiles p ON p.user_id = u.id").
		LeftJoin("user_preferences pref ON pref.user_id = u.id").
		Where("u.deleted_at IS NULL").
		OrderBy("u.updated_at DESC", "u.created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	if filter.Query != "" {
		pattern := "%" + filter.Query + "%"
		condition := "(u.email ILIKE ? OR u.username ILIKE ? OR p.display_name ILIKE ?)"
		countBuilder = countBuilder.Where(condition, pattern, pattern, pattern)
		dataBuilder = dataBuilder.Where(condition, pattern, pattern, pattern)
	}
	if filter.Role != "" {
		countBuilder = countBuilder.Where(sq.Eq{"u.role": filter.Role})
		dataBuilder = dataBuilder.Where(sq.Eq{"u.role": filter.Role})
	}
	if filter.EmailVerified != nil {
		countBuilder = countBuilder.Where(sq.Eq{"u.email_verified": *filter.EmailVerified})
		dataBuilder = dataBuilder.Where(sq.Eq{"u.email_verified": *filter.EmailVerified})
	}

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("UserRepo - ListAccounts - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("UserRepo - ListAccounts - Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("UserRepo - ListAccounts - Query: %w", err)
	}
	defer rows.Close()

	accounts := make([]entity.UserAccount, 0)
	for rows.Next() {
		account, scanErr := scanUserAccountRow(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("UserRepo - ListAccounts - scanUserAccountRow: %w", scanErr)
		}
		accounts = append(accounts, account)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("UserRepo - ListAccounts - rows.Err: %w", err)
	}

	return accounts, total, nil
}

// ListUserActivity returns role-change audit history for one user.
func (r *UserRepo) ListUserActivity(
	ctx context.Context,
	filter repo.UserActivityFilter,
) ([]entity.UserActivity, int, error) {
	countBuilder := r.Builder.
		Select("COUNT(*)").
		From("auth_audit_logs").
		Where(sq.Eq{"user_id": filter.UserID, "event": "role_change"})
	dataBuilder := r.Builder.
		Select("id, event, status, user_id, email, client_ip, user_agent, error_code, metadata, created_at").
		From("auth_audit_logs").
		Where(sq.Eq{"user_id": filter.UserID, "event": "role_change"}).
		OrderBy("created_at DESC").
		Limit(filter.Limit).
		Offset(filter.Offset)

	total, err := r.count(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("UserRepo - ListUserActivity - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("UserRepo - ListUserActivity - Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("UserRepo - ListUserActivity - Query: %w", err)
	}
	defer rows.Close()

	activity := make([]entity.UserActivity, 0)
	for rows.Next() {
		item, scanErr := scanUserActivity(rows)
		if scanErr != nil {
			return nil, 0, fmt.Errorf("UserRepo - ListUserActivity - scanUserActivity: %w", scanErr)
		}
		activity = append(activity, item)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("UserRepo - ListUserActivity - rows.Err: %w", err)
	}

	return activity, total, nil
}

// UpsertProfile stores product profile fields for one user.
func (r *UserRepo) UpsertProfile(ctx context.Context, profile entity.UserProfile) error {
	const query = `
INSERT INTO user_profiles (
    user_id,
    display_name,
    timezone,
    country_code,
    onboarding_version,
    onboarding_completed_at,
    personalization_enabled,
    created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now())
ON CONFLICT (user_id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    timezone = EXCLUDED.timezone,
    country_code = EXCLUDED.country_code,
    onboarding_version = EXCLUDED.onboarding_version,
    onboarding_completed_at = EXCLUDED.onboarding_completed_at,
    personalization_enabled = EXCLUDED.personalization_enabled,
    updated_at = now()`

	_, err := r.Pool.Exec(
		ctx,
		query,
		profile.UserID,
		nullableStringPtrArg(profile.DisplayName),
		nullableStringPtrArg(profile.Timezone),
		nullableStringPtrArg(profile.CountryCode),
		profile.OnboardingVersion,
		profile.OnboardingCompletedAt,
		profile.PersonalizationEnabled,
	)
	if err != nil {
		return fmt.Errorf("UserRepo - UpsertProfile - Exec: %w", err)
	}

	return nil
}

// UpsertPreferences stores reader and Quran preferences for one user.
func (r *UserRepo) UpsertPreferences(ctx context.Context, preferences entity.UserPreferences) error {
	const query = `
INSERT INTO user_preferences (
    user_id,
    preferred_ui_lang,
    preferred_content_lang,
    fallback_langs,
    arabic_level,
    reader_mode,
    interests,
    daily_goal_minutes,
    quran_translation_source_id,
    quran_recitation_id,
    created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now(), now())
ON CONFLICT (user_id) DO UPDATE SET
    preferred_ui_lang = EXCLUDED.preferred_ui_lang,
    preferred_content_lang = EXCLUDED.preferred_content_lang,
    fallback_langs = EXCLUDED.fallback_langs,
    arabic_level = EXCLUDED.arabic_level,
    reader_mode = EXCLUDED.reader_mode,
    interests = EXCLUDED.interests,
    daily_goal_minutes = EXCLUDED.daily_goal_minutes,
    quran_translation_source_id = EXCLUDED.quran_translation_source_id,
    quran_recitation_id = EXCLUDED.quran_recitation_id,
    updated_at = now()`

	_, err := r.Pool.Exec(
		ctx,
		query,
		preferences.UserID,
		preferences.PreferredUILang,
		preferences.PreferredContentLang,
		preferences.FallbackLangs,
		preferences.ArabicLevel,
		preferences.ReaderMode,
		preferences.Interests,
		preferences.DailyGoalMinutes,
		nullableStringPtrArg(preferences.QuranTranslationSourceID),
		nullableStringPtrArg(preferences.QuranRecitationID),
	)
	if err != nil {
		return fmt.Errorf("UserRepo - UpsertPreferences - Exec: %w", err)
	}

	return nil
}

// SetRoleByEmail updates one user's role by email.
func (r *UserRepo) SetRoleByEmail(ctx context.Context, email, role string) (entity.UserRoleChange, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.UserRoleChange{}, fmt.Errorf("UserRepo - SetRoleByEmail - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		targetID     string
		previousRole string
	)

	err = tx.QueryRow(
		ctx,
		"SELECT id, role FROM users WHERE email = $1 AND deleted_at IS NULL FOR UPDATE",
		email,
	).Scan(&targetID, &previousRole)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.UserRoleChange{}, entity.ErrUserNotFound
		}

		return entity.UserRoleChange{}, fmt.Errorf("UserRepo - SetRoleByEmail - lock user: %w", err)
	}

	// Demoting the last remaining admin would lock everyone out of the admin
	// API; recovery would require direct DB access or the CLI escape hatch.
	if previousRole == entity.UserRoleAdmin && role != entity.UserRoleAdmin {
		var adminCount int

		err = tx.QueryRow(
			ctx,
			"SELECT count(*) FROM users WHERE role = $1 AND deleted_at IS NULL",
			entity.UserRoleAdmin,
		).Scan(&adminCount)
		if err != nil {
			return entity.UserRoleChange{}, fmt.Errorf("UserRepo - SetRoleByEmail - count admins: %w", err)
		}

		if adminCount <= 1 {
			return entity.UserRoleChange{}, entity.ErrLastAdmin
		}
	}

	const updateQuery = `
UPDATE users
SET role = $2,
    updated_at = now()
WHERE id = $1
RETURNING id, username, email, role, password_hash, email_verified, token_version, created_at, updated_at`

	var change entity.UserRoleChange

	err = tx.QueryRow(ctx, updateQuery, targetID, role).
		Scan(
			&change.User.ID,
			&change.User.Username,
			&change.User.Email,
			&change.User.Role,
			&change.User.PasswordHash,
			&change.User.EmailVerified,
			&change.User.TokenVersion,
			&change.User.CreatedAt,
			&change.User.UpdatedAt,
		)
	if err != nil {
		return entity.UserRoleChange{}, fmt.Errorf("UserRepo - SetRoleByEmail - update QueryRow: %w", err)
	}

	change.PreviousRole = previousRole
	change.NewRole = change.User.Role

	if err = tx.Commit(ctx); err != nil {
		return entity.UserRoleChange{}, fmt.Errorf("UserRepo - SetRoleByEmail - tx.Commit: %w", err)
	}

	return change, nil
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
		Where("deleted_at IS NULL").
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

	if err = revokeAuthSessionsInTx(ctx, tx, userID); err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ChangePassword - %w", err)
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
		Select("id, user_id, token_hash, otp_hash, otp_expires_at, expires_at, used_at, sent_at, created_at").
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
		Select("id, user_id, token_hash, otp_hash, otp_expires_at, expires_at, used_at, sent_at, created_at").
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
		Where("deleted_at IS NULL").
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
		Where("deleted_at IS NULL").
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

	if err = revokeAuthSessionsInTx(ctx, tx, userID); err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.User{}, fmt.Errorf("UserRepo - ResetPasswordWithToken - tx.Commit: %w", err)
	}

	return user, nil
}

// ReplaceEmailChangeToken revokes previous unused email-change tokens and stores a new one.
func (r *UserRepo) ReplaceEmailChangeToken(ctx context.Context, token *entity.EmailChangeToken) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - ReplaceEmailChangeToken - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	sqlText, args, err := r.Builder.
		Update("email_change_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": token.UserID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - ReplaceEmailChangeToken - revoke Builder: %w", err)
	}
	if _, err = tx.Exec(ctx, sqlText, args...); err != nil {
		return fmt.Errorf("UserRepo - ReplaceEmailChangeToken - revoke: %w", err)
	}

	tokenSQL, tokenArgs, err := r.emailChangeTokenInsert(token)
	if err != nil {
		return fmt.Errorf("UserRepo - ReplaceEmailChangeToken - insert Builder: %w", err)
	}
	if _, err = tx.Exec(ctx, tokenSQL, tokenArgs...); err != nil {
		return fmt.Errorf("UserRepo - ReplaceEmailChangeToken - insert: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - ReplaceEmailChangeToken - tx.Commit: %w", err)
	}

	return nil
}

// RevokeUnusedEmailChangeTokens marks all currently unused email-change tokens as used.
func (r *UserRepo) RevokeUnusedEmailChangeTokens(ctx context.Context, userID string) error {
	sqlText, args, err := r.Builder.
		Update("email_change_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - RevokeUnusedEmailChangeTokens - Builder: %w", err)
	}

	if _, err = r.Pool.Exec(ctx, sqlText, args...); err != nil {
		return fmt.Errorf("UserRepo - RevokeUnusedEmailChangeTokens - Exec: %w", err)
	}

	return nil
}

// GetEmailChangeTokenByHash finds an email-change token by its SHA-256 hash.
func (r *UserRepo) GetEmailChangeTokenByHash(ctx context.Context, tokenHash string) (entity.EmailChangeToken, error) {
	sqlText, args, err := r.Builder.
		Select("id, user_id, new_email, token_hash, otp_hash, otp_expires_at, expires_at, used_at, sent_at, created_at").
		From("email_change_tokens").
		Where(sq.Eq{"token_hash": tokenHash}).
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailChangeToken{}, fmt.Errorf("UserRepo - GetEmailChangeTokenByHash - Builder: %w", err)
	}

	token, err := r.scanEmailChangeToken(ctx, sqlText, args...)
	if err != nil {
		return entity.EmailChangeToken{}, err
	}

	return token, nil
}

// GetLatestUnusedEmailChangeToken returns the most recent unused token for cooldown checks.
func (r *UserRepo) GetLatestUnusedEmailChangeToken(ctx context.Context, userID string) (entity.EmailChangeToken, error) {
	sqlText, args, err := r.Builder.
		Select("id, user_id, new_email, token_hash, otp_hash, otp_expires_at, expires_at, used_at, sent_at, created_at").
		From("email_change_tokens").
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		OrderBy("sent_at DESC").
		Limit(1).
		ToSql()
	if err != nil {
		return entity.EmailChangeToken{}, fmt.Errorf("UserRepo - GetLatestUnusedEmailChangeToken - Builder: %w", err)
	}

	token, err := r.scanEmailChangeToken(ctx, sqlText, args...)
	if err != nil {
		return entity.EmailChangeToken{}, err
	}

	return token, nil
}

// ChangeEmailWithToken atomically marks an email-change token used and updates the user email.
func (r *UserRepo) ChangeEmailWithToken(
	ctx context.Context,
	tokenID string,
	userID string,
	newEmail string,
) (entity.EmailChangeResult, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var oldEmail string
	err = tx.QueryRow(ctx, "SELECT email FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE", userID).
		Scan(&oldEmail)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailChangeResult{}, entity.ErrUserNotFound
		}

		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - lock user: %w", err)
	}

	tokenSQL, tokenArgs, err := r.Builder.
		Update("email_change_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"id": tokenID, "user_id": userID, "new_email": newEmail}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - token Builder: %w", err)
	}
	tag, err := tx.Exec(ctx, tokenSQL, tokenArgs...)
	if err != nil {
		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - token Exec: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return entity.EmailChangeResult{}, entity.ErrInvalidEmailChangeToken
	}

	userSQL, userArgs, err := r.Builder.
		Update("users").
		Set("email", newEmail).
		Set("email_verified", true).
		Set("email_verified_at", sq.Expr("now()")).
		Set("token_version", sq.Expr("token_version + 1")).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": userID}).
		Where("deleted_at IS NULL").
		Suffix("RETURNING " + userReturningColumns).
		ToSql()
	if err != nil {
		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - user Builder: %w", err)
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
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return entity.EmailChangeResult{}, entity.ErrUserAlreadyExists
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailChangeResult{}, entity.ErrUserNotFound
		}

		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - user QueryRow: %w", err)
	}

	revokeSQL, revokeArgs, err := r.Builder.
		Update("email_change_tokens").
		Set("used_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": userID}).
		Where("used_at IS NULL").
		ToSql()
	if err != nil {
		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - revoke Builder: %w", err)
	}
	if _, err = tx.Exec(ctx, revokeSQL, revokeArgs...); err != nil {
		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - revoke Exec: %w", err)
	}

	if err = revokeAuthSessionsInTx(ctx, tx, userID); err != nil {
		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return entity.EmailChangeResult{}, fmt.Errorf("UserRepo - ChangeEmailWithToken - tx.Commit: %w", err)
	}

	return entity.EmailChangeResult{User: user, OldEmail: oldEmail, NewEmail: user.Email}, nil
}

// DeleteAccount soft-deletes and anonymizes a user account in one transaction.
func (r *UserRepo) DeleteAccount(ctx context.Context, userID string) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - DeleteAccount - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	shortID := userID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	deletedEmail := "deleted+" + userID + "@deleted.local"
	deletedUsername := "deleted-user-" + shortID

	const userSQL = `
UPDATE users
SET
    username = $2,
    email = $3,
    password_hash = $4,
    email_verified = false,
    email_verified_at = NULL,
    token_version = token_version + 1,
    deleted_at = now(),
    updated_at = now()
WHERE id = $1
    AND deleted_at IS NULL`
	tag, err := tx.Exec(ctx, userSQL, userID, deletedUsername, deletedEmail, "deleted:"+userID)
	if err != nil {
		return fmt.Errorf("UserRepo - DeleteAccount - user Exec: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return entity.ErrUserNotFound
	}

	statements := []string{
		"UPDATE user_profiles SET display_name = NULL, timezone = NULL, country_code = NULL, personalization_enabled = false, updated_at = now() WHERE user_id = $1",
		"DELETE FROM user_preferences WHERE user_id = $1",
		"DELETE FROM tasks WHERE user_id = $1",
		"DELETE FROM history WHERE user_id = $1",
		"DELETE FROM reading_progress WHERE user_id = $1",
		"DELETE FROM quran_reading_progress WHERE user_id = $1",
		"DELETE FROM saved_items WHERE user_id = $1",
		"DELETE FROM translation_feedbacks WHERE user_id = $1",
		"DELETE FROM email_verification_tokens WHERE user_id = $1",
		"DELETE FROM password_reset_tokens WHERE user_id = $1",
		"DELETE FROM email_change_tokens WHERE user_id = $1",
		"DELETE FROM auth_sessions WHERE user_id = $1",
		"DELETE FROM auth_login_fingerprints WHERE user_id = $1",
		"UPDATE auth_audit_logs SET email = NULL, client_ip = NULL, user_agent = NULL WHERE user_id = $1",
	}
	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement, userID); err != nil {
			return fmt.Errorf("UserRepo - DeleteAccount - cleanup Exec: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - DeleteAccount - tx.Commit: %w", err)
	}

	return nil
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

	retryAfter := max(time.Until(expiresAt), 0)

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

// ListAuthAuditEventsSince returns audit rows for one event type created after
// since, oldest first. Used by the refresh-reuse alerter to find new events.
func (r *UserRepo) ListAuthAuditEventsSince(
	ctx context.Context,
	event string,
	since time.Time,
	limit int,
) ([]entity.AuthAuditLog, error) {
	if limit <= 0 {
		limit = 100
	}

	sqlText, args, err := r.Builder.
		Select("id, event, status, user_id, email, client_ip, user_agent, error_code, metadata, created_at").
		From("auth_audit_logs").
		Where(sq.Eq{"event": event}).
		Where(sq.Gt{"created_at": since}).
		OrderBy("created_at ASC").
		Limit(uint64(limit)).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("UserRepo - ListAuthAuditEventsSince - Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("UserRepo - ListAuthAuditEventsSince - Query: %w", err)
	}
	defer rows.Close()

	logs := make([]entity.AuthAuditLog, 0)

	for rows.Next() {
		item, scanErr := scanAuthAuditLog(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("UserRepo - ListAuthAuditEventsSince - scanAuthAuditLog: %w", scanErr)
		}

		logs = append(logs, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("UserRepo - ListAuthAuditEventsSince - rows.Err: %w", err)
	}

	return logs, nil
}

func scanAuthAuditLog(row rowScanner) (entity.AuthAuditLog, error) {
	var (
		log          entity.AuthAuditLog
		userID       *string
		email        *string
		clientIP     *string
		userAgent    *string
		errorCode    *string
		metadataJSON []byte
	)

	err := row.Scan(
		&log.ID,
		&log.Event,
		&log.Status,
		&userID,
		&email,
		&clientIP,
		&userAgent,
		&errorCode,
		&metadataJSON,
		&log.CreatedAt,
	)
	if err != nil {
		return entity.AuthAuditLog{}, err
	}

	if userID != nil {
		log.UserID = *userID
	}

	if email != nil {
		log.Email = *email
	}

	if clientIP != nil {
		log.ClientIP = *clientIP
	}

	if userAgent != nil {
		log.UserAgent = *userAgent
	}

	if errorCode != nil {
		log.ErrorCode = *errorCode
	}

	if len(metadataJSON) > 0 {
		_ = json.Unmarshal(metadataJSON, &log.Metadata) //nolint:errcheck // malformed audit metadata degrades to empty, never fails the read
	}

	return log, nil
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
		Where("deleted_at IS NULL").
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

func (r *UserRepo) scanUserAccount(ctx context.Context, sqlText string, args ...any) (entity.UserAccount, error) {
	account, err := scanUserAccountRow(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.UserAccount{}, entity.ErrUserNotFound
		}

		return entity.UserAccount{}, fmt.Errorf("UserRepo - scanUserAccount - QueryRow: %w", err)
	}

	return account, nil
}

func scanUserAccountRow(row rowScanner) (entity.UserAccount, error) {
	var (
		account                  entity.UserAccount
		displayName              sql.NullString
		timezone                 sql.NullString
		countryCode              sql.NullString
		onboardingCompletedAt    sql.NullTime
		dailyGoalMinutes         sql.NullInt64
		quranTranslationSourceID sql.NullString
		quranRecitationID        sql.NullString
	)

	err := row.Scan(
		&account.ID,
		&account.Username,
		&account.Email,
		&account.Role,
		&account.PasswordHash,
		&account.EmailVerified,
		&account.TokenVersion,
		&account.CreatedAt,
		&account.UpdatedAt,
		&displayName,
		&timezone,
		&countryCode,
		&account.Profile.OnboardingVersion,
		&onboardingCompletedAt,
		&account.Profile.PersonalizationEnabled,
		&account.Profile.CreatedAt,
		&account.Profile.UpdatedAt,
		&account.Preferences.PreferredUILang,
		&account.Preferences.PreferredContentLang,
		&account.Preferences.FallbackLangs,
		&account.Preferences.ArabicLevel,
		&account.Preferences.ReaderMode,
		&account.Preferences.Interests,
		&dailyGoalMinutes,
		&quranTranslationSourceID,
		&quranRecitationID,
		&account.Preferences.CreatedAt,
		&account.Preferences.UpdatedAt,
	)
	if err != nil {
		return entity.UserAccount{}, err
	}

	account.Profile.UserID = account.ID
	account.Preferences.UserID = account.ID
	if displayName.Valid {
		account.Profile.DisplayName = &displayName.String
	}
	if timezone.Valid {
		account.Profile.Timezone = &timezone.String
	}
	if countryCode.Valid {
		account.Profile.CountryCode = &countryCode.String
	}
	if onboardingCompletedAt.Valid {
		account.Profile.OnboardingCompletedAt = &onboardingCompletedAt.Time
	}
	if dailyGoalMinutes.Valid {
		value := int(dailyGoalMinutes.Int64)
		account.Preferences.DailyGoalMinutes = &value
	}
	if quranTranslationSourceID.Valid {
		account.Preferences.QuranTranslationSourceID = &quranTranslationSourceID.String
	}
	if quranRecitationID.Valid {
		account.Preferences.QuranRecitationID = &quranRecitationID.String
	}
	account.OnboardingRequired = account.Profile.OnboardingCompletedAt == nil

	return account, nil
}

func scanUserActivity(row rowScanner) (entity.UserActivity, error) {
	var item entity.UserActivity
	var userID sql.NullString
	var email sql.NullString
	var clientIP sql.NullString
	var userAgent sql.NullString
	var errorCode sql.NullString
	var metadataBytes []byte

	err := row.Scan(
		&item.ID,
		&item.Event,
		&item.Status,
		&userID,
		&email,
		&clientIP,
		&userAgent,
		&errorCode,
		&metadataBytes,
		&item.CreatedAt,
	)
	if err != nil {
		return entity.UserActivity{}, err
	}

	if userID.Valid {
		item.UserID = userID.String
	}
	if email.Valid {
		item.Email = email.String
	}
	item.ClientIP = nullableString(clientIP)
	item.UserAgent = nullableString(userAgent)
	item.ErrorCode = nullableString(errorCode)

	var metadata map[string]string
	if len(metadataBytes) > 0 {
		if err = json.Unmarshal(metadataBytes, &metadata); err != nil {
			return entity.UserActivity{}, err
		}
	}
	item.ActorID = stringPtrFromMap(metadata, "actor_id")
	item.ActorEmail = stringPtrFromMap(metadata, "actor_email")
	item.OldRole = stringPtrFromMap(metadata, "old_role")
	item.NewRole = stringPtrFromMap(metadata, "new_role")
	if item.NewRole == nil {
		item.NewRole = stringPtrFromMap(metadata, "role")
	}

	return item, nil
}

func stringPtrFromMap(values map[string]string, key string) *string {
	value := strings.TrimSpace(values[key])
	if value == "" {
		return nil
	}

	return &value
}

func (r *UserRepo) count(ctx context.Context, builder sq.SelectBuilder) (int, error) {
	sqlText, args, err := builder.ToSql()
	if err != nil {
		return 0, fmt.Errorf("building count query: %w", err)
	}

	var total int
	if err = r.Pool.QueryRow(ctx, sqlText, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("executing count query: %w", err)
	}

	return total, nil
}

func (r *UserRepo) insertDefaultProfileAndPreferences(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	displayName string,
	createdAt time.Time,
) error {
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	profile := entity.DefaultUserProfile(userID, createdAt)
	displayName = strings.TrimSpace(displayName)
	if displayName != "" {
		profile.DisplayName = &displayName
	}
	preferences := entity.DefaultUserPreferences(userID, createdAt)

	profileSQL, profileArgs, err := r.Builder.
		Insert("user_profiles").
		Columns(
			"user_id",
			"display_name",
			"onboarding_version",
			"personalization_enabled",
			"created_at",
			"updated_at",
		).
		Values(
			profile.UserID,
			nullableStringPtrArg(profile.DisplayName),
			profile.OnboardingVersion,
			profile.PersonalizationEnabled,
			profile.CreatedAt,
			profile.UpdatedAt,
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - insertDefaultProfileAndPreferences - profile Builder: %w", err)
	}
	if _, err = tx.Exec(ctx, profileSQL, profileArgs...); err != nil {
		return fmt.Errorf("UserRepo - insertDefaultProfileAndPreferences - profile Exec: %w", err)
	}

	preferencesSQL, preferencesArgs, err := r.Builder.
		Insert("user_preferences").
		Columns(
			"user_id",
			"preferred_ui_lang",
			"preferred_content_lang",
			"fallback_langs",
			"arabic_level",
			"reader_mode",
			"interests",
			"created_at",
			"updated_at",
		).
		Values(
			preferences.UserID,
			preferences.PreferredUILang,
			preferences.PreferredContentLang,
			preferences.FallbackLangs,
			preferences.ArabicLevel,
			preferences.ReaderMode,
			preferences.Interests,
			preferences.CreatedAt,
			preferences.UpdatedAt,
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - insertDefaultProfileAndPreferences - preferences Builder: %w", err)
	}
	if _, err = tx.Exec(ctx, preferencesSQL, preferencesArgs...); err != nil {
		return fmt.Errorf("UserRepo - insertDefaultProfileAndPreferences - preferences Exec: %w", err)
	}

	return nil
}

func (r *UserRepo) emailVerificationTokenInsert(token *entity.EmailVerificationToken) (string, []any, error) {
	return r.Builder.
		Insert("email_verification_tokens").
		Columns("id, user_id, token_hash, otp_hash, otp_expires_at, expires_at, used_at, sent_at, created_at").
		Values(
			token.ID,
			token.UserID,
			token.TokenHash,
			nullableStringArg(token.OTPHash),
			nullableTimeArg(token.OTPExpiresAt),
			token.ExpiresAt,
			token.UsedAt,
			token.SentAt,
			token.CreatedAt,
		).
		ToSql()
}

func (r *UserRepo) passwordResetTokenInsert(token *entity.PasswordResetToken) (string, []any, error) {
	return r.Builder.
		Insert("password_reset_tokens").
		Columns("id, user_id, token_hash, expires_at, used_at, sent_at, created_at").
		Values(token.ID, token.UserID, token.TokenHash, token.ExpiresAt, token.UsedAt, token.SentAt, token.CreatedAt).
		ToSql()
}

func (r *UserRepo) emailChangeTokenInsert(token *entity.EmailChangeToken) (string, []any, error) {
	return r.Builder.
		Insert("email_change_tokens").
		Columns("id, user_id, new_email, token_hash, otp_hash, otp_expires_at, expires_at, used_at, sent_at, created_at").
		Values(
			token.ID,
			token.UserID,
			token.NewEmail,
			token.TokenHash,
			nullableStringArg(token.OTPHash),
			nullableTimeArg(token.OTPExpiresAt),
			token.ExpiresAt,
			token.UsedAt,
			token.SentAt,
			token.CreatedAt,
		).
		ToSql()
}

func (r *UserRepo) scanEmailVerificationToken(
	ctx context.Context,
	sqlText string,
	args ...any,
) (entity.EmailVerificationToken, error) {
	var (
		token        entity.EmailVerificationToken
		otpHash      sql.NullString
		otpExpiresAt sql.NullTime
		usedAt       sql.NullTime
	)

	err := r.Pool.QueryRow(ctx, sqlText, args...).Scan(
		&token.ID,
		&token.UserID,
		&token.TokenHash,
		&otpHash,
		&otpExpiresAt,
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
	if otpHash.Valid {
		token.OTPHash = otpHash.String
	}
	if otpExpiresAt.Valid {
		token.OTPExpiresAt = &otpExpiresAt.Time
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

func nullableStringPtrArg(value *string) any {
	if value == nil {
		return nil
	}

	return nullableStringArg(*value)
}

func nullableTimeArg(value *time.Time) any {
	if value == nil {
		return nil
	}

	return *value
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

func (r *UserRepo) scanEmailChangeToken(
	ctx context.Context,
	sqlText string,
	args ...any,
) (entity.EmailChangeToken, error) {
	var (
		token        entity.EmailChangeToken
		otpHash      sql.NullString
		otpExpiresAt sql.NullTime
		usedAt       sql.NullTime
	)

	err := r.Pool.QueryRow(ctx, sqlText, args...).Scan(
		&token.ID,
		&token.UserID,
		&token.NewEmail,
		&token.TokenHash,
		&otpHash,
		&otpExpiresAt,
		&token.ExpiresAt,
		&usedAt,
		&token.SentAt,
		&token.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.EmailChangeToken{}, entity.ErrEmailChangeTokenNotFound
		}

		return entity.EmailChangeToken{}, fmt.Errorf("UserRepo - scanEmailChangeToken - QueryRow: %w", err)
	}
	if otpHash.Valid {
		token.OTPHash = otpHash.String
	}
	if otpExpiresAt.Valid {
		token.OTPExpiresAt = &otpExpiresAt.Time
	}
	if usedAt.Valid {
		token.UsedAt = &usedAt.Time
	}

	return token, nil
}
