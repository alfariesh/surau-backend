// Package serviceidentity implements named, scoped machine identities (A-2).
package serviceidentity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/google/uuid"
)

const (
	DefaultTokenTTL       = 30 * 24 * time.Hour
	MaximumTokenTTL       = 90 * 24 * time.Hour
	DefaultAuditRetention = 90 * 24 * time.Hour

	defaultListLimit = 50
	maxListLimit     = 100
	maxListOffset    = 10_000
	tokenSecretBytes = 32
	maxDescription   = 500
	structuredParts  = 2
)

// Options supplies deterministic seams for tests and the one-release legacy bridge.
type Options struct {
	Now            func() time.Time
	Random         io.Reader
	AuditRetention time.Duration
	AllowLegacy    bool
}

// UseCase owns validation, token generation, and live authentication checks.
type UseCase struct {
	repo           repo.ServiceIdentityRepo
	now            func() time.Time
	random         io.Reader
	auditRetention time.Duration
	allowLegacy    bool
}

// New constructs the service identity usecase.
func New(identityRepo repo.ServiceIdentityRepo, opts Options) *UseCase {
	if opts.Now == nil {
		opts.Now = time.Now
	}

	if opts.Random == nil {
		opts.Random = rand.Reader
	}

	if opts.AuditRetention <= 0 {
		opts.AuditRetention = DefaultAuditRetention
	}

	return &UseCase{
		repo:           identityRepo,
		now:            opts.Now,
		random:         opts.Random,
		auditRetention: opts.AuditRetention,
		allowLegacy:    opts.AllowLegacy,
	}
}

// CreateServicePrincipal registers an immutable machine name and scope set.
func (uc *UseCase) CreateServicePrincipal(
	ctx context.Context,
	actorID, name, description string,
	scopes []string,
) (entity.ServicePrincipal, error) {
	name, description, scopes, err := normalizePrincipal(name, description, scopes)
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	now := uc.now().UTC()

	return uc.repo.CreateServicePrincipal(ctx, entity.ServicePrincipal{
		ID:            uuid.NewString(),
		PrincipalName: name,
		Description:   description,
		Scopes:        scopes,
		CreatedBy:     stringPointer(actorID),
		CreatedAt:     now,
		UpdatedAt:     now,
	}, actorID)
}

// ListServicePrincipals returns a clamped admin list.
func (uc *UseCase) ListServicePrincipals(
	ctx context.Context,
	limit, offset int,
) ([]entity.ServicePrincipal, int, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}

	limit = min(limit, maxListLimit)
	offset = min(max(offset, 0), maxListOffset)

	return uc.repo.ListServicePrincipals(ctx, repo.ServicePrincipalFilter{Limit: limit, Offset: offset})
}

// GetServicePrincipal returns safe metadata only.
func (uc *UseCase) GetServicePrincipal(
	ctx context.Context,
	id string,
) (entity.ServicePrincipal, error) {
	if _, err := uuid.Parse(id); err != nil {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalNotFound
	}

	return uc.repo.GetServicePrincipal(ctx, id)
}

// UpdateServicePrincipal changes description/scopes while preserving name.
func (uc *UseCase) UpdateServicePrincipal(
	ctx context.Context,
	actorID, id, description string,
	scopes []string,
	expected *time.Time,
	force bool,
) (entity.ServicePrincipal, error) {
	if _, err := uuid.Parse(id); err != nil {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalNotFound
	}

	description, scopes, err := normalizeDescriptionAndScopes(description, scopes)
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	return uc.repo.UpdateServicePrincipal(ctx, id, description, scopes, actorID, expected, force)
}

// IssueServiceToken creates a 256-bit opaque secret and stores only its digest.
func (uc *UseCase) IssueServiceToken(
	ctx context.Context,
	actorID, principalID string,
	expiresAt *time.Time,
	expected *time.Time,
	force bool,
) (entity.ServiceTokenIssueResult, error) {
	if _, err := uuid.Parse(principalID); err != nil {
		return entity.ServiceTokenIssueResult{}, entity.ErrServicePrincipalNotFound
	}

	now := uc.now().UTC()

	expires := now.Add(DefaultTokenTTL)
	if expiresAt != nil {
		expires = expiresAt.UTC()
	}

	if !expires.After(now) || expires.After(now.Add(MaximumTokenTTL)) {
		return entity.ServiceTokenIssueResult{}, entity.ErrInvalidServicePrincipal
	}

	secret := make([]byte, tokenSecretBytes)
	if _, err := io.ReadFull(uc.random, secret); err != nil {
		return entity.ServiceTokenIssueResult{}, fmt.Errorf("serviceidentity issue random: %w", err)
	}

	tokenID := uuid.NewString()
	raw := "surau_st_" + tokenID + "." + base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(raw))
	record := entity.ServiceTokenRecord{
		ServiceToken: entity.ServiceToken{
			ID:          tokenID,
			PrincipalID: principalID,
			TokenKind:   "structured",
			ExpiresAt:   expires,
			CreatedBy:   stringPointer(actorID),
			CreatedAt:   now,
		},
		SecretHash: digest[:],
	}

	principal, err := uc.repo.IssueServiceToken(ctx, record, actorID, expected, force)
	if err != nil {
		return entity.ServiceTokenIssueResult{}, err
	}

	return entity.ServiceTokenIssueResult{
		Principal: principal,
		Token: entity.IssuedServiceToken{
			ServiceToken: record.ServiceToken,
			Token:        raw,
		},
	}, nil
}

// RevokeServiceToken retires one credential while sibling T2 remains active.
func (uc *UseCase) RevokeServiceToken(
	ctx context.Context,
	actorID, principalID, tokenID string,
	expected *time.Time,
	force bool,
) (entity.ServicePrincipal, error) {
	if _, err := uuid.Parse(principalID); err != nil {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalNotFound
	}

	if _, err := uuid.Parse(tokenID); err != nil {
		return entity.ServicePrincipal{}, entity.ErrServiceTokenNotFound
	}

	return uc.repo.RevokeServiceToken(ctx, principalID, tokenID, actorID, expected, force)
}

// RevokeServicePrincipal permanently disables all credentials for one identity.
func (uc *UseCase) RevokeServicePrincipal(
	ctx context.Context,
	actorID, principalID string,
	expected *time.Time,
	force bool,
) (entity.ServicePrincipal, error) {
	if _, err := uuid.Parse(principalID); err != nil {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalNotFound
	}

	return uc.repo.RevokeServicePrincipal(ctx, principalID, actorID, expected, force)
}

// AuthenticateServiceToken performs a fresh DB lookup for every request. It
// returns verified identity metadata for revoked/expired tokens so audit can
// attribute them; fake/malformed tokens never inherit the claimed UUID name.
func (uc *UseCase) AuthenticateServiceToken(
	ctx context.Context,
	rawToken, requiredScope string,
) (entity.ServiceAuthentication, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return entity.ServiceAuthentication{Outcome: entity.ServiceAuthOutcomeMissing}, entity.ErrInvalidServiceToken
	}

	credential, outcome, err := uc.lookupCredential(ctx, rawToken)
	if err != nil {
		return entity.ServiceAuthentication{Outcome: outcome}, err
	}

	digest := sha256.Sum256([]byte(rawToken))
	if subtle.ConstantTimeCompare(digest[:], credential.Token.SecretHash) != 1 {
		return entity.ServiceAuthentication{Outcome: entity.ServiceAuthOutcomeInvalid}, entity.ErrInvalidServiceToken
	}

	auth := entity.ServiceAuthentication{
		PrincipalID:   credential.PrincipalID,
		PrincipalName: credential.PrincipalName,
		TokenID:       credential.Token.ID,
		Scopes:        slices.Clone(credential.Scopes),
		ExpiresAt:     credential.Token.ExpiresAt,
		Outcome:       entity.ServiceAuthOutcomeAllowed,
	}
	if credential.Token.RevokedAt != nil {
		auth.Outcome = entity.ServiceAuthOutcomeTokenRevoked

		return auth, entity.ErrInvalidServiceToken
	}

	if credential.RevokedAt != nil {
		auth.Outcome = entity.ServiceAuthOutcomePrincipalRevoked

		return auth, entity.ErrInvalidServiceToken
	}

	if !credential.Token.ExpiresAt.After(uc.now().UTC()) {
		auth.Outcome = entity.ServiceAuthOutcomeExpired

		return auth, entity.ErrInvalidServiceToken
	}

	if requiredScope != "" && !auth.HasScope(requiredScope) {
		auth.Outcome = entity.ServiceAuthOutcomeInsufficientScope

		return auth, entity.ErrInsufficientServiceScope
	}

	return auth, nil
}

// CreateServiceRequestAudit writes the mandatory pre-handler evidence row.
//
//nolint:gocritic // value parameter is normalized before crossing the repository boundary
func (uc *UseCase) CreateServiceRequestAudit(
	ctx context.Context,
	audit entity.ServiceRequestAudit,
) (string, error) {
	if audit.ID == "" {
		audit.ID = uuid.NewString()
	}

	if audit.PrincipalName == "" {
		audit.PrincipalName = entity.ServicePrincipalUnattributed
	}

	if audit.StartedAt.IsZero() {
		audit.StartedAt = uc.now().UTC()
	}

	if err := uc.repo.CreateServiceRequestAudit(ctx, audit); err != nil {
		return "", fmt.Errorf("%w: %w", entity.ErrServiceIdentityUnavailable, err)
	}

	return audit.ID, nil
}

// FinishServiceRequestAudit stamps final HTTP outcome after the handler.
func (uc *UseCase) FinishServiceRequestAudit(ctx context.Context, id string, status int) error {
	if err := uc.repo.FinishServiceRequestAudit(
		ctx,
		id,
		entity.ServiceAuthOutcomeAllowed,
		status,
		uc.now().UTC(),
	); err != nil {
		return fmt.Errorf("%w: %w", entity.ErrServiceIdentityUnavailable, err)
	}

	return nil
}

// CleanupServiceRequestAudits enforces the selected 90-day retention.
func (uc *UseCase) CleanupServiceRequestAudits(
	ctx context.Context,
) (entity.ServiceAuditCleanupResult, error) {
	count, err := uc.repo.CleanupServiceRequestAudits(ctx, uc.now().UTC().Add(-uc.auditRetention))
	if err != nil {
		return entity.ServiceAuditCleanupResult{}, err
	}

	return entity.ServiceAuditCleanupResult{RequestLogs: count}, nil
}

// BootstrapLegacyCollab hashes the existing secret exactly once. A repeated
// startup never renews or un-revokes its credential.
func (uc *UseCase) BootstrapLegacyCollab(
	ctx context.Context,
	rawToken string,
) (entity.ServicePrincipal, error) {
	rawToken = strings.TrimSpace(rawToken)
	if !uc.allowLegacy || len(rawToken) < tokenSecretBytes {
		return entity.ServicePrincipal{}, entity.ErrInvalidServiceToken
	}

	now := uc.now().UTC()
	digest := sha256.Sum256([]byte(rawToken))
	principalID := uuid.NewString()

	return uc.repo.BootstrapLegacyServiceToken(ctx, entity.ServicePrincipal{
		ID:            principalID,
		PrincipalName: "collab-server",
		Description:   "Realtime editorial draft bridge",
		Scopes:        []string{entity.ServiceScopeCollabDraftWrite},
		CreatedAt:     now,
		UpdatedAt:     now,
	}, entity.ServiceTokenRecord{
		ServiceToken: entity.ServiceToken{
			ID:          uuid.NewString(),
			PrincipalID: principalID,
			TokenKind:   "legacy",
			ExpiresAt:   now.Add(DefaultTokenTTL),
			CreatedAt:   now,
		},
		SecretHash: digest[:],
	})
}

func (uc *UseCase) lookupCredential(
	ctx context.Context,
	rawToken string,
) (entity.ServiceCredential, string, error) {
	const prefix = "surau_st_"
	if after, ok := strings.CutPrefix(rawToken, prefix); ok {
		body := after

		parts := strings.Split(body, ".")
		if len(parts) != structuredParts {
			return entity.ServiceCredential{}, entity.ServiceAuthOutcomeMalformed, entity.ErrInvalidServiceToken
		}

		if _, err := uuid.Parse(parts[0]); err != nil {
			return entity.ServiceCredential{}, entity.ServiceAuthOutcomeMalformed, entity.ErrInvalidServiceToken
		}

		secret, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil || len(secret) != tokenSecretBytes {
			return entity.ServiceCredential{}, entity.ServiceAuthOutcomeMalformed, entity.ErrInvalidServiceToken
		}

		credential, err := uc.repo.GetServiceCredentialByID(ctx, parts[0])

		return mapCredentialLookup(credential, err)
	}

	if !uc.allowLegacy {
		return entity.ServiceCredential{}, entity.ServiceAuthOutcomeMalformed, entity.ErrInvalidServiceToken
	}

	digest := sha256.Sum256([]byte(rawToken))
	credential, err := uc.repo.GetServiceCredentialByHash(ctx, digest[:])

	return mapCredentialLookup(credential, err)
}

//nolint:gocritic // credential is returned by value and only inspected in this mapper
func mapCredentialLookup(
	credential entity.ServiceCredential,
	err error,
) (entity.ServiceCredential, string, error) {
	if errors.Is(err, entity.ErrServiceTokenNotFound) {
		return entity.ServiceCredential{}, entity.ServiceAuthOutcomeInvalid, entity.ErrInvalidServiceToken
	}

	if err != nil {
		return entity.ServiceCredential{}, entity.ServiceAuthOutcomeUnavailable,
			fmt.Errorf("%w: %w", entity.ErrServiceIdentityUnavailable, err)
	}

	return credential, entity.ServiceAuthOutcomeAllowed, nil
}

func normalizePrincipal(
	name, description string,
	scopes []string,
) (normalizedName, normalizedDescription string, normalizedScopes []string, err error) {
	name = strings.ToLower(strings.TrimSpace(name))

	matched, err := regexp.MatchString(`^[a-z][a-z0-9-]{2,62}$`, name)
	if err != nil || !matched {
		return "", "", nil, entity.ErrInvalidServicePrincipal
	}

	description, scopes, err = normalizeDescriptionAndScopes(description, scopes)

	return name, description, scopes, err
}

func normalizeDescriptionAndScopes(
	description string,
	scopes []string,
) (normalizedDescription string, normalizedScopes []string, err error) {
	description = strings.TrimSpace(description)
	if len(description) > maxDescription {
		return "", nil, entity.ErrInvalidServicePrincipal
	}

	set := make(map[string]struct{}, len(scopes))
	for _, raw := range scopes {
		scope := strings.ToLower(strings.TrimSpace(raw))
		if !entity.IsValidServiceScope(scope) {
			return "", nil, entity.ErrInvalidServiceScope
		}

		set[scope] = struct{}{}
	}

	if len(set) == 0 {
		return "", nil, entity.ErrInvalidServiceScope
	}

	normalized := make([]string, 0, len(set))
	for scope := range set {
		normalized = append(normalized, scope)
	}

	slices.Sort(normalized)

	return description, normalized, nil
}

func stringPointer(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}
