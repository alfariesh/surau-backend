package user

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/internal/usecase/authmeta"
	"github.com/google/uuid"
)

func repoCleanupPolicy(opts CleanupOptions) repo.AuthCleanupPolicy {
	tokenRetention := opts.TokenRetention
	if tokenRetention <= 0 {
		tokenRetention = 720 * time.Hour
	}
	sessionRetention := opts.SessionRetention
	if sessionRetention <= 0 {
		sessionRetention = 720 * time.Hour
	}

	return repo.AuthCleanupPolicy{
		Now:              time.Now().UTC(),
		TokenRetention:   tokenRetention,
		SessionRetention: sessionRetention,
		AuditRetention:   opts.AuditRetention,
	}
}

// issueSession creates a new refresh-token session family for the user and
// returns it together with a matching access token.
func (uc *UseCase) issueSession(ctx context.Context, user entity.User) (entity.LoginResult, error) {
	sessionID := uuid.NewString()

	return uc.issueSessionRow(ctx, user, sessionID, sessionID, func(session entity.AuthSession) error {
		if uc.sessions == nil {
			return nil
		}

		return uc.sessions.CreateAuthSession(ctx, session)
	})
}

// RefreshSession rotates a refresh token: the presented token is retired and
// a new refresh/access pair is issued. Presenting an already-rotated or
// revoked token is treated as reuse and revokes the whole session family.
func (uc *UseCase) RefreshSession(ctx context.Context, refreshToken string) (result entity.LoginResult, err error) {
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventSessionRefresh, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	if uc.sessions == nil {
		return entity.LoginResult{}, entity.ErrInvalidRefreshToken
	}

	refreshToken = strings.TrimSpace(refreshToken)
	if len(refreshToken) > maxResetTokenInputBytes {
		return entity.LoginResult{}, entity.ErrInvalidRefreshToken
	}

	if err = uc.enforceAuthRateLimit(ctx, authEventSessionRefresh, []rateLimitCheck{
		{keyType: rateLimitKeyTypeToken, value: refreshToken, rule: uc.rateLimit.RefreshToken},
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.RefreshIP},
	}); err != nil {
		return entity.LoginResult{}, err
	}

	tokenHash, err := hashRefreshToken(refreshToken)
	if err != nil {
		return entity.LoginResult{}, err
	}

	session, err := uc.sessions.GetAuthSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, entity.ErrInvalidRefreshToken) {
			return entity.LoginResult{}, entity.ErrInvalidRefreshToken
		}

		return entity.LoginResult{}, fmt.Errorf("UserUseCase - RefreshSession - GetAuthSessionByTokenHash: %w", err)
	}
	auditUserID = session.UserID

	now := time.Now().UTC()
	if session.RevokedAt != nil || session.ReplacedByID != nil {
		uc.revokeSessionFamilyForReuse(ctx, session)

		return entity.LoginResult{}, entity.ErrInvalidRefreshToken
	}
	if !now.Before(session.ExpiresAt) {
		return entity.LoginResult{}, entity.ErrInvalidRefreshToken
	}

	user, err := uc.repo.GetByID(ctx, session.UserID)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			_, _ = uc.sessions.RevokeAuthSessionFamily(ctx, session.FamilyID)

			return entity.LoginResult{}, entity.ErrInvalidRefreshToken
		}

		return entity.LoginResult{}, fmt.Errorf("UserUseCase - RefreshSession - uc.repo.GetByID: %w", err)
	}
	if user.TokenVersion != session.TokenVersion {
		_, _ = uc.sessions.RevokeAuthSessionFamily(ctx, session.FamilyID)

		return entity.LoginResult{}, entity.ErrInvalidRefreshToken
	}

	result, err = uc.issueSessionRow(ctx, user, uuid.NewString(), session.FamilyID, func(next entity.AuthSession) error {
		return uc.sessions.RotateAuthSession(ctx, session.ID, next)
	})
	if err != nil {
		// Losing the rotation race means another caller already spent this
		// token — treat it as reuse and kill the family.
		if errors.Is(err, entity.ErrInvalidRefreshToken) {
			uc.revokeSessionFamilyForReuse(ctx, session)

			return entity.LoginResult{}, entity.ErrInvalidRefreshToken
		}

		return entity.LoginResult{}, err
	}

	return result, nil
}

// Logout revokes the session family behind one refresh token. It is
// idempotent: unknown or already-revoked tokens return success so the
// endpoint cannot be used as a token-validity oracle.
func (uc *UseCase) Logout(ctx context.Context, refreshToken string) (err error) {
	auditUserID := ""
	defer func() {
		uc.auditAuth(ctx, authEventLogout, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" || len(refreshToken) > maxResetTokenInputBytes {
		return entity.ErrInvalidAuthInput
	}

	if err = uc.enforceAuthRateLimit(ctx, authEventLogout, []rateLimitCheck{
		{keyType: rateLimitKeyTypeIP, value: authmeta.From(ctx).ClientIP, rule: uc.rateLimit.RefreshIP},
	}); err != nil {
		return err
	}

	if uc.sessions == nil {
		return nil
	}

	tokenHash, err := hashRefreshToken(refreshToken)
	if err != nil {
		return entity.ErrInvalidAuthInput
	}

	session, err := uc.sessions.GetAuthSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, entity.ErrInvalidRefreshToken) {
			return nil
		}

		return fmt.Errorf("UserUseCase - Logout - GetAuthSessionByTokenHash: %w", err)
	}
	auditUserID = session.UserID

	if _, err = uc.sessions.RevokeAuthSessionFamily(ctx, session.FamilyID); err != nil {
		return fmt.Errorf("UserUseCase - Logout - RevokeAuthSessionFamily: %w", err)
	}

	return nil
}

// LogoutAll revokes every session for the user and bumps token_version so
// outstanding access tokens die immediately.
func (uc *UseCase) LogoutAll(ctx context.Context, userID string) (err error) {
	auditUserID := strings.TrimSpace(userID)
	defer func() {
		uc.auditAuth(ctx, authEventLogoutAll, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	if auditUserID == "" {
		return entity.ErrInvalidAuthInput
	}
	if uc.sessions == nil {
		return nil
	}

	if _, err = uc.sessions.RevokeAllAuthSessions(ctx, auditUserID); err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return entity.ErrInvalidCredentials
		}

		return fmt.Errorf("UserUseCase - LogoutAll - RevokeAllAuthSessions: %w", err)
	}

	return nil
}

// ListSessions returns the user's active devices (one row per active session
// family), newest activity first. Read-only: no audit, no rate limit.
func (uc *UseCase) ListSessions(ctx context.Context, userID string) ([]entity.AuthSession, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, entity.ErrInvalidAuthInput
	}
	if uc.sessions == nil {
		return []entity.AuthSession{}, nil
	}

	sessions, err := uc.sessions.ListActiveAuthSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("UserUseCase - ListSessions - ListActiveAuthSessions: %w", err)
	}

	return sessions, nil
}

// RevokeSession revokes one of the user's active sessions by its id. The access
// token tied to that device keeps working until it expires (minutes), but the
// refresh token is killed immediately so the device cannot renew.
func (uc *UseCase) RevokeSession(ctx context.Context, userID, sessionID string) (err error) {
	auditUserID := strings.TrimSpace(userID)
	defer func() {
		uc.auditAuth(ctx, authEventSessionRevoke, auditStatus(err), auditUserID, "", auditErrorCode(err), nil)
	}()

	sessionID = strings.TrimSpace(sessionID)
	if auditUserID == "" || sessionID == "" {
		return entity.ErrInvalidAuthInput
	}
	// Session ids are UUIDs; a malformed id can never match a real session, so
	// treat it as not-found rather than letting the DB raise a type error.
	if _, parseErr := uuid.Parse(sessionID); parseErr != nil {
		return entity.ErrAuthSessionNotFound
	}
	if uc.sessions == nil {
		return entity.ErrAuthSessionNotFound
	}

	if err = uc.sessions.RevokeAuthSessionByID(ctx, auditUserID, sessionID); err != nil {
		if errors.Is(err, entity.ErrAuthSessionNotFound) {
			return entity.ErrAuthSessionNotFound
		}

		return fmt.Errorf("UserUseCase - RevokeSession - RevokeAuthSessionByID: %w", err)
	}

	return nil
}

// CleanupAuthData deletes expired auth rows using the configured retentions.
// It is called by the app's periodic maintenance job, not by transports.
func (uc *UseCase) CleanupAuthData(ctx context.Context) (entity.AuthCleanupResult, error) {
	if uc.maintenance == nil {
		return entity.AuthCleanupResult{}, nil
	}

	result, err := uc.maintenance.CleanupAuthData(ctx, repoCleanupPolicy(uc.cleanup))
	if err != nil {
		return entity.AuthCleanupResult{}, fmt.Errorf("UserUseCase - CleanupAuthData: %w", err)
	}

	return result, nil
}

// issueSessionRow builds the refresh token + session row, persists it via
// store, and returns the access/refresh pair.
func (uc *UseCase) issueSessionRow(
	ctx context.Context,
	user entity.User,
	sessionID string,
	familyID string,
	store func(entity.AuthSession) error,
) (entity.LoginResult, error) {
	rawTokenBytes := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		return entity.LoginResult{}, fmt.Errorf("UserUseCase - issueSessionRow - rand.Read: %w", err)
	}
	rawToken := base64.RawURLEncoding.EncodeToString(rawTokenBytes)

	now := time.Now().UTC()
	refreshExpiresAt := now.Add(uc.refreshTokenTTL)
	meta := authmeta.From(ctx)
	session := entity.AuthSession{
		ID:               sessionID,
		FamilyID:         familyID,
		UserID:           user.ID,
		RefreshTokenHash: hashTokenBytes(rawTokenBytes),
		TokenVersion:     user.TokenVersion,
		UserAgent:        truncateRunes(meta.UserAgent, maxEmailUserAgentRunes),
		ClientIP:         strings.TrimSpace(meta.ClientIP),
		CreatedAt:        now,
		LastUsedAt:       now,
		ExpiresAt:        refreshExpiresAt,
	}

	if err := store(session); err != nil {
		if errors.Is(err, entity.ErrInvalidRefreshToken) {
			return entity.LoginResult{}, err
		}

		return entity.LoginResult{}, fmt.Errorf("UserUseCase - issueSessionRow - store: %w", err)
	}

	accessToken, accessExpiresAt, err := uc.jwt.GenerateSessionToken(user.ID, user.TokenVersion, familyID)
	if err != nil {
		return entity.LoginResult{}, fmt.Errorf("UserUseCase - issueSessionRow - GenerateSessionToken: %w", err)
	}

	return entity.LoginResult{
		User:             user,
		SessionID:        familyID,
		AccessToken:      accessToken,
		AccessExpiresAt:  accessExpiresAt,
		RefreshToken:     rawToken,
		RefreshExpiresAt: refreshExpiresAt,
	}, nil
}

func (uc *UseCase) revokeSessionFamilyForReuse(ctx context.Context, session entity.AuthSession) {
	revoked, err := uc.sessions.RevokeAuthSessionFamily(ctx, session.FamilyID)
	metadata := map[string]string{
		"family_id":        session.FamilyID,
		"revoked_sessions": strconv.FormatInt(revoked, 10),
	}
	if err != nil {
		metadata["revoke_error"] = "true"
	}
	uc.auditAuth(ctx, authEventRefreshReuse, authAuditStatusFailure, session.UserID, "", "refresh_token_reuse", metadata)
}

func hashRefreshToken(token string) (string, error) {
	token = strings.TrimSpace(token)
	rawTokenBytes, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(rawTokenBytes) != refreshTokenBytes {
		return "", entity.ErrInvalidRefreshToken
	}

	return hashTokenBytes(rawTokenBytes), nil
}
