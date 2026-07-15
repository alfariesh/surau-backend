package serviceidentity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errDuplicateServiceAudit = errors.New("duplicate service audit")
	errServiceAuditNotFound  = errors.New("service audit not found")
)

func TestFrozenServiceScopes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{
		"collab:draft:write",
		"rag-eval:read",
		"enrichment:read",
		"prompt-registry:manage",
		"inference-budget:manage",
	}, entity.AllServiceScopes())
}

func TestTokenOverlapAndImmediateRevocation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	store := newMemoryIdentityRepo()
	uc := New(store, Options{
		Now:    func() time.Time { return now },
		Random: bytes.NewReader(bytes.Repeat([]byte{0x5a}, tokenSecretBytes*2)),
	})
	principal, err := uc.CreateServicePrincipal(
		t.Context(), "", "collab-server", "draft bridge", []string{entity.ServiceScopeCollabDraftWrite},
	)
	require.NoError(t, err)

	t1, err := uc.IssueServiceToken(t.Context(), "", principal.ID, nil, nil, true)
	require.NoError(t, err)

	now = now.Add(time.Second)
	t2, err := uc.IssueServiceToken(t.Context(), "", principal.ID, nil, nil, true)
	require.NoError(t, err)

	assert.NotEqual(t, t1.Token.ID, t2.Token.ID)
	assert.Equal(t, 2, len(store.tokens))
	assert.Equal(t, DefaultTokenTTL, t1.Token.ExpiresAt.Sub(t1.Token.CreatedAt))
	assert.NotContains(t, mustJSON(t, store.tokens[t1.Token.ID]), t1.Token.Token)
	digest := sha256.Sum256([]byte(t1.Token.Token))
	assert.Equal(t, digest[:], store.tokens[t1.Token.ID].SecretHash)

	_, err = uc.AuthenticateServiceToken(t.Context(), t1.Token.Token, entity.ServiceScopeCollabDraftWrite)
	require.NoError(t, err)
	_, err = uc.AuthenticateServiceToken(t.Context(), t2.Token.Token, entity.ServiceScopeCollabDraftWrite)
	require.NoError(t, err)

	_, err = uc.RevokeServiceToken(t.Context(), "", principal.ID, t1.Token.ID, nil, true)
	require.NoError(t, err)
	auth, err := uc.AuthenticateServiceToken(t.Context(), t1.Token.Token, entity.ServiceScopeCollabDraftWrite)
	require.ErrorIs(t, err, entity.ErrInvalidServiceToken)
	assert.Equal(t, "collab-server", auth.PrincipalName, "verified revoked token remains attributable")
	assert.Equal(t, entity.ServiceAuthOutcomeTokenRevoked, auth.Outcome)
	_, err = uc.AuthenticateServiceToken(t.Context(), t2.Token.Token, entity.ServiceScopeCollabDraftWrite)
	require.NoError(t, err, "overlapping T2 stays live")

	_, err = uc.RevokeServicePrincipal(t.Context(), "", principal.ID, nil, true)
	require.NoError(t, err)
	auth, err = uc.AuthenticateServiceToken(t.Context(), t2.Token.Token, entity.ServiceScopeCollabDraftWrite)
	require.ErrorIs(t, err, entity.ErrInvalidServiceToken)
	assert.Equal(t, entity.ServiceAuthOutcomePrincipalRevoked, auth.Outcome)
}

func TestTokenTTLAndScopeBoundaries(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	store := newMemoryIdentityRepo()
	uc := New(store, Options{Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{1}, 96))})
	principal, err := uc.CreateServicePrincipal(
		t.Context(), "", "rag-eval", "evaluation", []string{entity.ServiceScopeRAGEvalRead},
	)
	require.NoError(t, err)

	maximum := now.Add(MaximumTokenTTL)
	issued, err := uc.IssueServiceToken(t.Context(), "", principal.ID, &maximum, nil, true)
	require.NoError(t, err)
	assert.Equal(t, maximum, issued.Token.ExpiresAt)

	tooLate := maximum.Add(time.Nanosecond)
	_, err = uc.IssueServiceToken(t.Context(), "", principal.ID, &tooLate, nil, true)
	require.ErrorIs(t, err, entity.ErrInvalidServicePrincipal)

	past := now
	_, err = uc.IssueServiceToken(t.Context(), "", principal.ID, &past, nil, true)
	require.ErrorIs(t, err, entity.ErrInvalidServicePrincipal)

	auth, err := uc.AuthenticateServiceToken(t.Context(), issued.Token.Token, entity.ServiceScopeEnrichmentRead)
	require.ErrorIs(t, err, entity.ErrInsufficientServiceScope)
	assert.Equal(t, entity.ServiceAuthOutcomeInsufficientScope, auth.Outcome)

	auth, err = uc.AuthenticateServiceToken(t.Context(), "surau_st_not-a-uuid.fake", entity.ServiceScopeRAGEvalRead)
	require.ErrorIs(t, err, entity.ErrInvalidServiceToken)
	assert.Empty(t, auth.PrincipalName, "a guessed UUID must never attribute a fake token")
}

func TestServicePrincipalCRUDValidationAndAuditRetention(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	store := newMemoryIdentityRepo()
	uc := New(store, Options{Now: func() time.Time { return now }})

	_, err := uc.CreateServicePrincipal(t.Context(), "", "Bad Name", "", []string{entity.ServiceScopeRAGEvalRead})
	require.ErrorIs(t, err, entity.ErrInvalidServicePrincipal)
	_, err = uc.CreateServicePrincipal(t.Context(), "", "ok-name", strings.Repeat("x", maxDescription+1), []string{entity.ServiceScopeRAGEvalRead})
	require.ErrorIs(t, err, entity.ErrInvalidServicePrincipal)
	_, err = uc.CreateServicePrincipal(t.Context(), "", "ok-name", "", nil)
	require.ErrorIs(t, err, entity.ErrInvalidServiceScope)
	_, err = uc.CreateServicePrincipal(t.Context(), "", "ok-name", "", []string{"unknown:scope"})
	require.ErrorIs(t, err, entity.ErrInvalidServiceScope)

	principal, err := uc.CreateServicePrincipal(t.Context(), "actor-id", " HTTP-Enrichment ", " catalog jobs ", []string{
		entity.ServiceScopeEnrichmentRead,
		entity.ServiceScopeEnrichmentRead,
		entity.ServiceScopeRAGEvalRead,
	})
	require.NoError(t, err)
	assert.Equal(t, "http-enrichment", principal.PrincipalName)
	assert.Equal(t, "catalog jobs", principal.Description)
	assert.Equal(t, []string{entity.ServiceScopeEnrichmentRead, entity.ServiceScopeRAGEvalRead}, principal.Scopes)

	items, total, err := uc.ListServicePrincipals(t.Context(), 999, -10)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, 1, total)
	assert.Equal(t, repo.ServicePrincipalFilter{Limit: maxListLimit, Offset: 0}, store.lastFilter)

	_, err = uc.GetServicePrincipal(t.Context(), "not-a-uuid")
	require.ErrorIs(t, err, entity.ErrServicePrincipalNotFound)
	got, err := uc.GetServicePrincipal(t.Context(), principal.ID)
	require.NoError(t, err)
	assert.Equal(t, principal.ID, got.ID)

	_, err = uc.UpdateServicePrincipal(t.Context(), "actor-id", "not-a-uuid", "", principal.Scopes, nil, true)
	require.ErrorIs(t, err, entity.ErrServicePrincipalNotFound)
	_, err = uc.UpdateServicePrincipal(t.Context(), "actor-id", principal.ID, "", nil, nil, true)
	require.ErrorIs(t, err, entity.ErrInvalidServiceScope)
	updated, err := uc.UpdateServicePrincipal(t.Context(), "actor-id", principal.ID, "rotated jobs", []string{entity.ServiceScopeEnrichmentRead}, nil, true)
	require.NoError(t, err)
	assert.Equal(t, "rotated jobs", updated.Description)

	auditID, err := uc.CreateServiceRequestAudit(t.Context(), entity.ServiceRequestAudit{
		Method: "GET", RouteTemplate: "/internal/test", AuthOutcome: entity.ServiceAuthOutcomeStarted,
	})
	require.NoError(t, err)

	audit := store.audits[auditID]
	assert.Equal(t, entity.ServicePrincipalUnattributed, audit.PrincipalName)
	assert.Equal(t, now, audit.StartedAt)

	_, err = uc.CreateServiceRequestAudit(t.Context(), audit)
	require.ErrorIs(t, err, entity.ErrServiceIdentityUnavailable)
	require.NoError(t, uc.FinishServiceRequestAudit(t.Context(), auditID, 204))
	assert.Equal(t, entity.ServiceAuthOutcomeAllowed, store.audits[auditID].AuthOutcome)
	require.ErrorIs(t, uc.FinishServiceRequestAudit(t.Context(), "missing", 500), entity.ErrServiceIdentityUnavailable)

	oldID, err := uc.CreateServiceRequestAudit(t.Context(), entity.ServiceRequestAudit{
		ID: "old", PrincipalName: "old-service", Method: "GET", RouteTemplate: "/internal/old",
		AuthOutcome: entity.ServiceAuthOutcomeAllowed, StartedAt: now.Add(-DefaultAuditRetention - time.Second),
	})
	require.NoError(t, err)
	assert.Equal(t, "old", oldID)
	cleanup, err := uc.CleanupServiceRequestAudits(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 1, cleanup.RequestLogs)
	assert.NotContains(t, store.audits, oldID)
}

func TestCredentialFailureAndLegacyPaths(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)
	store := newMemoryIdentityRepo()
	uc := New(store, Options{
		Now: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{3}, tokenSecretBytes)),
	})
	principal, err := uc.CreateServicePrincipal(t.Context(), "", "rag-eval", "", []string{entity.ServiceScopeRAGEvalRead})
	require.NoError(t, err)

	_, err = uc.IssueServiceToken(t.Context(), "", "bad-id", nil, nil, true)
	require.ErrorIs(t, err, entity.ErrServicePrincipalNotFound)

	noRandom := New(store, Options{Now: func() time.Time { return now }, Random: bytes.NewReader(nil)})
	_, err = noRandom.IssueServiceToken(t.Context(), "", principal.ID, nil, nil, true)
	require.Error(t, err)

	issued, err := uc.IssueServiceToken(t.Context(), "", principal.ID, nil, nil, true)
	require.NoError(t, err)
	_, err = uc.RevokeServiceToken(t.Context(), "", "bad-id", issued.Token.ID, nil, true)
	require.ErrorIs(t, err, entity.ErrServicePrincipalNotFound)
	_, err = uc.RevokeServiceToken(t.Context(), "", principal.ID, "bad-id", nil, true)
	require.ErrorIs(t, err, entity.ErrServiceTokenNotFound)
	_, err = uc.RevokeServicePrincipal(t.Context(), "", "bad-id", nil, true)
	require.ErrorIs(t, err, entity.ErrServicePrincipalNotFound)

	for _, raw := range []string{"", "surau_st_bad", "surau_st_" + issued.Token.ID + ".bad!"} {
		_, authErr := uc.AuthenticateServiceToken(t.Context(), raw, entity.ServiceScopeRAGEvalRead)
		require.ErrorIs(t, authErr, entity.ErrInvalidServiceToken)
	}

	wrongSecret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, tokenSecretBytes))
	auth, err := uc.AuthenticateServiceToken(
		t.Context(), "surau_st_"+issued.Token.ID+"."+wrongSecret, entity.ServiceScopeRAGEvalRead,
	)
	require.ErrorIs(t, err, entity.ErrInvalidServiceToken)
	assert.Equal(t, entity.ServiceAuthOutcomeInvalid, auth.Outcome)
	assert.Empty(t, auth.PrincipalName)

	now = issued.Token.ExpiresAt
	auth, err = uc.AuthenticateServiceToken(t.Context(), issued.Token.Token, entity.ServiceScopeRAGEvalRead)
	require.ErrorIs(t, err, entity.ErrInvalidServiceToken)
	assert.Equal(t, entity.ServiceAuthOutcomeExpired, auth.Outcome)

	disabled := New(newMemoryIdentityRepo(), Options{Now: func() time.Time { return now }})
	_, err = disabled.BootstrapLegacyCollab(t.Context(), strings.Repeat("a", tokenSecretBytes))
	require.ErrorIs(t, err, entity.ErrInvalidServiceToken)

	legacyStore := newMemoryIdentityRepo()
	legacy := New(legacyStore, Options{Now: func() time.Time { return now }, AllowLegacy: true})
	_, err = legacy.BootstrapLegacyCollab(t.Context(), "short")
	require.ErrorIs(t, err, entity.ErrInvalidServiceToken)

	legacyRaw := strings.Repeat("legacy-token-", 3)
	legacyPrincipal, err := legacy.BootstrapLegacyCollab(t.Context(), legacyRaw)
	require.NoError(t, err)
	assert.Equal(t, "collab-server", legacyPrincipal.PrincipalName)
	legacyAuth, err := legacy.AuthenticateServiceToken(t.Context(), legacyRaw, entity.ServiceScopeCollabDraftWrite)
	require.NoError(t, err)
	assert.Equal(t, "collab-server", legacyAuth.PrincipalName)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	require.NoError(t, err)

	return string(encoded)
}

type memoryIdentityRepo struct {
	principal  entity.ServicePrincipal
	tokens     map[string]entity.ServiceTokenRecord
	audits     map[string]entity.ServiceRequestAudit
	lastFilter repo.ServicePrincipalFilter
}

func newMemoryIdentityRepo() *memoryIdentityRepo {
	return &memoryIdentityRepo{tokens: map[string]entity.ServiceTokenRecord{}, audits: map[string]entity.ServiceRequestAudit{}}
}

//nolint:gocritic // in-memory repository mirrors the production repository interface
func (r *memoryIdentityRepo) CreateServicePrincipal(
	_ context.Context, principal entity.ServicePrincipal, _ string,
) (entity.ServicePrincipal, error) {
	r.principal = principal

	return principal, nil
}

func (r *memoryIdentityRepo) ListServicePrincipals(
	_ context.Context, filter repo.ServicePrincipalFilter,
) ([]entity.ServicePrincipal, int, error) {
	r.lastFilter = filter

	return []entity.ServicePrincipal{r.principal}, 1, nil
}

func (r *memoryIdentityRepo) GetServicePrincipal(_ context.Context, id string) (entity.ServicePrincipal, error) {
	if id != r.principal.ID {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalNotFound
	}

	principal := r.principal
	principal.Tokens = make([]entity.ServiceToken, 0, len(r.tokens))

	for tokenID := range r.tokens {
		token := r.tokens[tokenID]
		principal.Tokens = append(principal.Tokens, token.ServiceToken)
	}

	return principal, nil
}

func (r *memoryIdentityRepo) UpdateServicePrincipal(
	_ context.Context, id, description string, scopes []string, _ string, _ *time.Time, _ bool,
) (entity.ServicePrincipal, error) {
	if id != r.principal.ID {
		return entity.ServicePrincipal{}, entity.ErrServicePrincipalNotFound
	}

	r.principal.Description = description
	r.principal.Scopes = scopes

	return r.GetServicePrincipal(context.Background(), id)
}

//nolint:gocritic // in-memory repository mirrors the production repository interface
func (r *memoryIdentityRepo) IssueServiceToken(
	_ context.Context, token entity.ServiceTokenRecord, _ string, _ *time.Time, _ bool,
) (entity.ServicePrincipal, error) {
	r.tokens[token.ID] = token
	r.principal.UpdatedAt = token.CreatedAt

	return r.GetServicePrincipal(context.Background(), token.PrincipalID)
}

func (r *memoryIdentityRepo) RevokeServiceToken(
	_ context.Context, principalID, tokenID, _ string, _ *time.Time, _ bool,
) (entity.ServicePrincipal, error) {
	token, ok := r.tokens[tokenID]
	if !ok {
		return entity.ServicePrincipal{}, entity.ErrServiceTokenNotFound
	}

	now := time.Now().UTC()
	token.RevokedAt = &now
	r.tokens[tokenID] = token

	return r.GetServicePrincipal(context.Background(), principalID)
}

func (r *memoryIdentityRepo) RevokeServicePrincipal(
	_ context.Context, principalID, _ string, _ *time.Time, _ bool,
) (entity.ServicePrincipal, error) {
	now := time.Now().UTC()
	r.principal.RevokedAt = &now

	return r.GetServicePrincipal(context.Background(), principalID)
}

func (r *memoryIdentityRepo) GetServiceCredentialByID(
	_ context.Context, tokenID string,
) (entity.ServiceCredential, error) {
	token, ok := r.tokens[tokenID]
	if !ok {
		return entity.ServiceCredential{}, entity.ErrServiceTokenNotFound
	}

	return entity.ServiceCredential{
		Token: token, PrincipalID: r.principal.ID, PrincipalName: r.principal.PrincipalName,
		Scopes: r.principal.Scopes, RevokedAt: r.principal.RevokedAt,
	}, nil
}

func (r *memoryIdentityRepo) GetServiceCredentialByHash(
	_ context.Context, hash []byte,
) (entity.ServiceCredential, error) {
	for id := range r.tokens {
		token := r.tokens[id]
		if bytes.Equal(token.SecretHash, hash) {
			return r.GetServiceCredentialByID(context.Background(), id)
		}
	}

	return entity.ServiceCredential{}, entity.ErrServiceTokenNotFound
}

//nolint:gocritic // in-memory repository mirrors the production repository interface
func (r *memoryIdentityRepo) BootstrapLegacyServiceToken(
	ctx context.Context, principal entity.ServicePrincipal, token entity.ServiceTokenRecord,
) (entity.ServicePrincipal, error) {
	_, err := r.CreateServicePrincipal(ctx, principal, "")
	if err != nil {
		return entity.ServicePrincipal{}, err
	}

	return r.IssueServiceToken(ctx, token, "", nil, true)
}

//nolint:gocritic // in-memory repository mirrors the production repository interface
func (r *memoryIdentityRepo) CreateServiceRequestAudit(_ context.Context, audit entity.ServiceRequestAudit) error {
	if _, exists := r.audits[audit.ID]; exists {
		return errDuplicateServiceAudit
	}

	r.audits[audit.ID] = audit

	return nil
}

func (r *memoryIdentityRepo) FinishServiceRequestAudit(
	_ context.Context, id, outcome string, status int, finishedAt time.Time,
) error {
	audit, ok := r.audits[id]
	if !ok {
		return errServiceAuditNotFound
	}

	audit.AuthOutcome = outcome
	audit.ResponseStatus = &status
	audit.FinishedAt = &finishedAt
	r.audits[id] = audit

	return nil
}

func (r *memoryIdentityRepo) CleanupServiceRequestAudits(_ context.Context, before time.Time) (int64, error) {
	var removed int64

	for id := range r.audits {
		audit := r.audits[id]
		if audit.StartedAt.Before(before) {
			delete(r.audits, id)

			removed++
		}
	}

	return removed, nil
}
