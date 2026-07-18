package persistent

import (
	"context"
	"errors"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/jackc/pgx/v5"
)

const authSessionColumns = "id, family_id, user_id, refresh_token_hash, token_version, " +
	"user_agent, client_ip, created_at, last_used_at, expires_at, revoked_at, replaced_by_id, " +
	"mfa_verified_at"

// CreateAuthSession stores a new refresh-token session row.
func (r *UserRepo) CreateAuthSession(ctx context.Context, session entity.AuthSession) error { //nolint:gocritic // value param fixed by the repo interface contract
	sqlText, args, err := r.Builder.
		Insert("auth_sessions").
		Columns(authSessionColumns).
		Values(
			session.ID,
			session.FamilyID,
			session.UserID,
			session.RefreshTokenHash,
			session.TokenVersion,
			nullableStringArg(session.UserAgent),
			nullableStringArg(session.ClientIP),
			session.CreatedAt,
			session.LastUsedAt,
			session.ExpiresAt,
			nullableTimeArg(session.RevokedAt),
			nullableStringPtrArg(session.ReplacedByID),
			nullableTimeArg(session.MFAVerifiedAt),
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - CreateAuthSession - r.Builder: %w", err)
	}

	if _, err = r.Pool.Exec(ctx, sqlText, args...); err != nil {
		return fmt.Errorf("UserRepo - CreateAuthSession - r.Pool.Exec: %w", err)
	}

	return nil
}

// GetAuthSessionByTokenHash returns the session row for one refresh-token hash.
func (r *UserRepo) GetAuthSessionByTokenHash(ctx context.Context, tokenHash string) (entity.AuthSession, error) {
	sqlText, args, err := r.Builder.
		Select(authSessionColumns).
		From("auth_sessions").
		Where(sq.Eq{"refresh_token_hash": tokenHash}).
		ToSql()
	if err != nil {
		return entity.AuthSession{}, fmt.Errorf("UserRepo - GetAuthSessionByTokenHash - r.Builder: %w", err)
	}

	session, err := scanAuthSession(r.Pool.QueryRow(ctx, sqlText, args...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.AuthSession{}, entity.ErrInvalidRefreshToken
		}

		return entity.AuthSession{}, fmt.Errorf("UserRepo - GetAuthSessionByTokenHash - QueryRow: %w", err)
	}

	return session, nil
}

// RotateAuthSession locks and validates the old row, then revokes it and
// inserts its replacement atomically. A concurrent spend remains reuse, while
// ordinary inactivity expiry is reported separately so it does not raise a
// false security alarm.
func (r *UserRepo) RotateAuthSession(
	ctx context.Context,
	oldID string,
	next *entity.AuthSession,
	validity repo.AuthSessionValidity,
) error {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UserRepo - RotateAuthSession - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	state, err := r.lockAuthSessionRotationState(ctx, tx, oldID)
	if err != nil {
		return err
	}

	if err := state.validate(validity); err != nil {
		return err
	}

	if retireErr := r.retireAuthSession(ctx, tx, oldID, next.ID, validity.Now); retireErr != nil {
		return retireErr
	}

	if insertErr := r.insertRotatedAuthSession(ctx, tx, next); insertErr != nil {
		return insertErr
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UserRepo - RotateAuthSession - tx.Commit: %w", err)
	}

	return nil
}

func (r *UserRepo) retireAuthSession(
	ctx context.Context,
	tx pgx.Tx,
	oldID string,
	replacementID string,
	now time.Time,
) error {
	revokeSQL, revokeArgs, err := r.Builder.
		Update("auth_sessions").
		Set("revoked_at", now).
		Set("replaced_by_id", replacementID).
		Set("last_used_at", now).
		Where(sq.Eq{"id": oldID}).
		Where("revoked_at IS NULL").
		Where("replaced_by_id IS NULL").
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - RotateAuthSession - revoke Builder: %w", err)
	}

	tag, err := tx.Exec(ctx, revokeSQL, revokeArgs...)
	if err != nil {
		return fmt.Errorf("UserRepo - RotateAuthSession - revoke Exec: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return entity.ErrInvalidRefreshToken
	}

	return nil
}

func (r *UserRepo) insertRotatedAuthSession(
	ctx context.Context,
	tx pgx.Tx,
	next *entity.AuthSession,
) error {
	insertSQL, insertArgs, err := r.Builder.
		Insert("auth_sessions").
		Columns(authSessionColumns).
		Values(
			next.ID,
			next.FamilyID,
			next.UserID,
			next.RefreshTokenHash,
			next.TokenVersion,
			nullableStringArg(next.UserAgent),
			nullableStringArg(next.ClientIP),
			next.CreatedAt,
			next.LastUsedAt,
			next.ExpiresAt,
			nullableTimeArg(next.RevokedAt),
			nullableStringPtrArg(next.ReplacedByID),
			nullableTimeArg(next.MFAVerifiedAt),
		).
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - RotateAuthSession - insert Builder: %w", err)
	}

	if _, err = tx.Exec(ctx, insertSQL, insertArgs...); err != nil {
		return fmt.Errorf("UserRepo - RotateAuthSession - insert Exec: %w", err)
	}

	return nil
}

type authSessionRotationState struct {
	revokedAt  *time.Time
	replacedBy *string
	lastUsedAt time.Time
	expiresAt  time.Time
}

func (r *UserRepo) lockAuthSessionRotationState(
	ctx context.Context,
	tx pgx.Tx,
	oldID string,
) (authSessionRotationState, error) {
	stateSQL, stateArgs, err := r.Builder.
		Select("revoked_at", "replaced_by_id", "last_used_at", "expires_at").
		From("auth_sessions").
		Where(sq.Eq{"id": oldID}).
		Suffix("FOR UPDATE").
		ToSql()
	if err != nil {
		return authSessionRotationState{}, fmt.Errorf("UserRepo - RotateAuthSession - state Builder: %w", err)
	}

	var state authSessionRotationState
	if err = tx.QueryRow(ctx, stateSQL, stateArgs...).Scan(
		&state.revokedAt,
		&state.replacedBy,
		&state.lastUsedAt,
		&state.expiresAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return authSessionRotationState{}, entity.ErrInvalidRefreshToken
		}

		return authSessionRotationState{}, fmt.Errorf("UserRepo - RotateAuthSession - state QueryRow: %w", err)
	}

	return state, nil
}

func (state authSessionRotationState) validate(validity repo.AuthSessionValidity) error {
	if state.revokedAt != nil || state.replacedBy != nil {
		return entity.ErrInvalidRefreshToken
	}

	if !validity.Now.Before(state.expiresAt) || !state.lastUsedAt.After(validity.IdleCutoff) {
		return entity.ErrRefreshSessionExpired
	}

	return nil
}

// RevokeAuthSessionFamily revokes every active session in one rotation chain.
func (r *UserRepo) RevokeAuthSessionFamily(ctx context.Context, familyID string) (int64, error) {
	sqlText, args, err := r.Builder.
		Update("auth_sessions").
		Set("revoked_at", sq.Expr("now()")).
		Where(sq.Eq{"family_id": familyID}).
		Where("revoked_at IS NULL").
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("UserRepo - RevokeAuthSessionFamily - r.Builder: %w", err)
	}

	tag, err := r.Pool.Exec(ctx, sqlText, args...)
	if err != nil {
		return 0, fmt.Errorf("UserRepo - RevokeAuthSessionFamily - r.Pool.Exec: %w", err)
	}

	return tag.RowsAffected(), nil
}

// RevokeAllAuthSessions revokes every active session for the user and bumps
// users.token_version in one transaction so outstanding access tokens die too.
func (r *UserRepo) RevokeAllAuthSessions(ctx context.Context, userID string) (int64, error) {
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("UserRepo - RevokeAllAuthSessions - r.Pool.Begin: %w", err)
	}
	defer tx.Rollback(ctx)

	userSQL, userArgs, err := r.Builder.
		Update("users").
		Set("token_version", sq.Expr("token_version + 1")).
		Set("updated_at", sq.Expr("now()")).
		Where(sq.Eq{"id": userID}).
		Where("deleted_at IS NULL").
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("UserRepo - RevokeAllAuthSessions - user Builder: %w", err)
	}

	tag, err := tx.Exec(ctx, userSQL, userArgs...)
	if err != nil {
		return 0, fmt.Errorf("UserRepo - RevokeAllAuthSessions - user Exec: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return 0, entity.ErrUserNotFound
	}

	revokeSQL, revokeArgs, err := r.Builder.
		Update("auth_sessions").
		Set("revoked_at", sq.Expr("now()")).
		Where(sq.Eq{"user_id": userID}).
		Where("revoked_at IS NULL").
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("UserRepo - RevokeAllAuthSessions - revoke Builder: %w", err)
	}

	revoked, err := tx.Exec(ctx, revokeSQL, revokeArgs...)
	if err != nil {
		return 0, fmt.Errorf("UserRepo - RevokeAllAuthSessions - revoke Exec: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("UserRepo - RevokeAllAuthSessions - tx.Commit: %w", err)
	}

	return revoked.RowsAffected(), nil
}

// ListActiveAuthSessions returns the user's unrevoked, unexpired sessions
// ordered by most recent activity. Rotation keeps one active row per family,
// so each row corresponds to one active device.
func (r *UserRepo) ListActiveAuthSessions(
	ctx context.Context,
	userID string,
	validity repo.AuthSessionValidity,
) ([]entity.AuthSession, error) {
	sqlText, args, err := r.Builder.
		Select(authSessionColumns).
		From("auth_sessions").
		Where(sq.Eq{"user_id": userID}).
		Where("revoked_at IS NULL").
		Where(sq.Gt{"expires_at": validity.Now}).
		Where(sq.Gt{"last_used_at": validity.IdleCutoff}).
		OrderBy("last_used_at DESC").
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("UserRepo - ListActiveAuthSessions - r.Builder: %w", err)
	}

	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("UserRepo - ListActiveAuthSessions - Query: %w", err)
	}
	defer rows.Close()

	sessions := make([]entity.AuthSession, 0)

	for rows.Next() {
		session, scanErr := scanAuthSession(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("UserRepo - ListActiveAuthSessions - scanAuthSession: %w", scanErr)
		}

		sessions = append(sessions, session)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("UserRepo - ListActiveAuthSessions - rows.Err: %w", err)
	}

	return sessions, nil
}

// RevokeAuthSessionByID revokes the family behind one active session, scoped to
// the owning user so callers cannot revoke other users' sessions. The session
// row id is resolved to its family first, then the whole rotation chain is
// revoked (a family has one active row, so this kills exactly that device).
func (r *UserRepo) RevokeAuthSessionByID(
	ctx context.Context,
	userID,
	sessionID string,
	validity repo.AuthSessionValidity,
) error {
	lookupSQL, lookupArgs, err := r.Builder.
		Select("family_id").
		From("auth_sessions").
		Where(sq.Eq{"id": sessionID, "user_id": userID}).
		Where("revoked_at IS NULL").
		Where(sq.Gt{"expires_at": validity.Now}).
		Where(sq.Gt{"last_used_at": validity.IdleCutoff}).
		ToSql()
	if err != nil {
		return fmt.Errorf("UserRepo - RevokeAuthSessionByID - lookup Builder: %w", err)
	}

	var familyID string
	if err = r.Pool.QueryRow(ctx, lookupSQL, lookupArgs...).Scan(&familyID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.ErrAuthSessionNotFound
		}

		return fmt.Errorf("UserRepo - RevokeAuthSessionByID - lookup QueryRow: %w", err)
	}

	if _, err = r.RevokeAuthSessionFamily(ctx, familyID); err != nil {
		return fmt.Errorf("UserRepo - RevokeAuthSessionByID - RevokeAuthSessionFamily: %w", err)
	}

	return nil
}

// revokeAuthSessionsInTx revokes every active session for the user inside an
// existing transaction. Used by mutations that already bump token_version.
func revokeAuthSessionsInTx(ctx context.Context, tx pgx.Tx, userID string) error {
	const query = "UPDATE auth_sessions SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL"
	if _, err := tx.Exec(ctx, query, userID); err != nil {
		return fmt.Errorf("revokeAuthSessionsInTx: %w", err)
	}

	return nil
}

func scanAuthSession(row rowScanner) (entity.AuthSession, error) {
	var (
		session   entity.AuthSession
		userAgent *string
		clientIP  *string
	)

	err := row.Scan(
		&session.ID,
		&session.FamilyID,
		&session.UserID,
		&session.RefreshTokenHash,
		&session.TokenVersion,
		&userAgent,
		&clientIP,
		&session.CreatedAt,
		&session.LastUsedAt,
		&session.ExpiresAt,
		&session.RevokedAt,
		&session.ReplacedByID,
		&session.MFAVerifiedAt,
	)
	if err != nil {
		return entity.AuthSession{}, err
	}

	if userAgent != nil {
		session.UserAgent = *userAgent
	}

	if clientIP != nil {
		session.ClientIP = *clientIP
	}

	return session, nil
}
