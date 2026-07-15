package persistent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	v1 "github.com/alfariesh/surau-backend/internal/controller/restapi/v1"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/usecase/serviceidentity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveServiceTokenRevocationWithoutRestart is the A-2 cutover proof: the
// exact same Fiber process accepts T1+T2, then observes a DB revoke on its next
// request. No handler/router/app object is reconstructed between assertions.
//
//nolint:paralleltest // serial live database evidence
func TestLiveServiceTokenRevocationWithoutRestart(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	identityRepo := NewServiceIdentityRepo(pg)
	identityUC := serviceidentity.New(identityRepo, serviceidentity.Options{})

	principal, err := identityUC.CreateServicePrincipal(
		t.Context(), "", "collab-live-test", "revocation drill", []string{entity.ServiceScopeCollabDraftWrite},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, cleanupErr := pg.Pool.Exec(ctx,
			`DELETE FROM service_request_audit_logs WHERE principal_name = 'collab-live-test'`)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool.Exec(ctx,
			`DELETE FROM service_identity_events WHERE principal_name = 'collab-live-test'`)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool.Exec(ctx, `DELETE FROM service_principals WHERE id = $1::uuid`, principal.ID)
		assert.NoError(t, cleanupErr)
	})

	t1, err := identityUC.IssueServiceToken(t.Context(), "", principal.ID, nil, nil, true)
	require.NoError(t, err)
	t2, err := identityUC.IssueServiceToken(t.Context(), "", principal.ID, nil, nil, true)
	require.NoError(t, err)

	var storedHash []byte
	require.NoError(t, pg.Pool.QueryRow(t.Context(),
		`SELECT secret_hash FROM service_tokens WHERE id = $1::uuid`, t1.Token.ID).Scan(&storedHash))
	assert.Len(t, storedHash, 32)
	assert.NotEqual(t, []byte(t1.Token.Token), storedHash, "database stores a digest, never the raw credential")

	_, err = pg.Pool.Exec(t.Context(), `
INSERT INTO service_tokens (id, principal_id, secret_hash, expires_at, created_at)
VALUES ($1::uuid, $2::uuid, $3, now() + interval '90 days 1 second', now())`,
		uuid.NewString(), principal.ID, make([]byte, 32))
	require.Error(t, err)

	var constraintErr *pgconn.PgError
	require.True(t, errors.As(err, &constraintErr))
	assert.Equal(t, "23514", constraintErr.Code, "database independently enforces the 90-day maximum")

	rows, err := pg.Pool.Query(t.Context(), `
SELECT column_name FROM information_schema.columns WHERE table_name = 'service_tokens'`)
	require.NoError(t, err)

	var tokenColumns []string

	for rows.Next() {
		var column string
		require.NoError(t, rows.Scan(&column))
		tokenColumns = append(tokenColumns, column)
	}

	rows.Close()
	assert.NotContains(t, tokenColumns, "token")
	assert.NotContains(t, tokenColumns, "secret")

	app := fiber.New()
	app.Use(middleware.RequestID())
	v1.NewInternalRoutes(app.Group("/internal"), nil, identityUC, logger.New("error"))

	assertWhoami := func(token string, wantStatus int) {
		t.Helper()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/internal/collab/whoami", nil)
		request.Header.Set(middleware.ServiceTokenHeader, token)
		response, requestErr := app.Test(request)
		require.NoError(t, requestErr)

		defer response.Body.Close()

		assert.Equal(t, wantStatus, response.StatusCode)

		if wantStatus == http.StatusOK {
			var body struct {
				PrincipalName string `json:"principal_name"`
			}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
			assert.Equal(t, "collab-live-test", body.PrincipalName)
		}
	}

	assertWhoami(t1.Token.Token, http.StatusOK)
	assertWhoami(t2.Token.Token, http.StatusOK)
	_, err = identityUC.RevokeServiceToken(t.Context(), "", principal.ID, t1.Token.ID, nil, true)
	require.NoError(t, err)
	assertWhoami(t1.Token.Token, http.StatusUnauthorized)
	assertWhoami(t2.Token.Token, http.StatusOK)

	rows, err = pg.Pool.Query(t.Context(), `
SELECT principal_name, required_scope, route_template, auth_outcome, response_status
FROM service_request_audit_logs
WHERE principal_name = 'collab-live-test'
ORDER BY started_at, id`)
	require.NoError(t, err)

	defer rows.Close()

	var outcomes []string

	for rows.Next() {
		var principalName, scope, route, outcome string

		var status int
		require.NoError(t, rows.Scan(&principalName, &scope, &route, &outcome, &status))
		assert.Equal(t, "collab-live-test", principalName)
		assert.Equal(t, entity.ServiceScopeCollabDraftWrite, scope)
		assert.Equal(t, "/internal/collab/whoami", route)

		outcomes = append(outcomes, outcome)
	}

	require.NoError(t, rows.Err())
	assert.Equal(t, []string{"allowed", "allowed", "token_revoked", "allowed"}, outcomes)
}

// TestLiveServiceIdentityRepositoryLifecycle covers the administrative
// transaction paths against PostgreSQL: optimistic locking, event writes,
// per-token/principal revocation, hash lookup, retention cleanup, and legacy
// bootstrap idempotency.
//
//nolint:paralleltest // serial live database lifecycle evidence
func TestLiveServiceIdentityRepositoryLifecycle(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	repository := NewServiceIdentityRepo(pg)
	identityUC := serviceidentity.New(repository, serviceidentity.Options{})
	now := time.Now().UTC().Truncate(time.Microsecond)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	principalName := "registry-live-" + suffix
	legacyName := "legacy-live-" + suffix

	cleanupName := func(ctx context.Context, name string) {
		_, cleanupErr := pg.Pool.Exec(ctx, `DELETE FROM service_request_audit_logs WHERE principal_name = $1`, name)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool.Exec(ctx, `DELETE FROM service_identity_events WHERE principal_name = $1`, name)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool.Exec(ctx, `DELETE FROM service_principals WHERE principal_name = $1`, name)
		assert.NoError(t, cleanupErr)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cleanupName(ctx, principalName)
		cleanupName(ctx, legacyName)
	})

	created, err := repository.CreateServicePrincipal(t.Context(), entity.ServicePrincipal{
		ID: uuid.NewString(), PrincipalName: principalName, Description: "repository lifecycle",
		Scopes: []string{entity.ServiceScopeRAGEvalRead}, CreatedAt: now, UpdatedAt: now,
	}, "")
	require.NoError(t, err)
	assert.Equal(t, principalName, created.PrincipalName)

	items, total, err := repository.ListServicePrincipals(t.Context(), repo.ServicePrincipalFilter{Limit: 100, Offset: 0})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, total, 1)
	assert.Contains(t, items, created)
	got, err := repository.GetServicePrincipal(t.Context(), created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, got.ID)
	_, err = repository.GetServicePrincipal(t.Context(), uuid.NewString())
	require.ErrorIs(t, err, entity.ErrServicePrincipalNotFound)

	_, err = repository.UpdateServicePrincipal(
		t.Context(), created.ID, "missing precondition", created.Scopes, "", nil, false,
	)
	require.ErrorIs(t, err, entity.ErrPreconditionRequired)

	stale := created.UpdatedAt.Add(-time.Second)
	_, err = repository.UpdateServicePrincipal(
		t.Context(), created.ID, "stale", created.Scopes, "", &stale, false,
	)
	require.ErrorIs(t, err, entity.ErrPreconditionFailed)
	updated, err := repository.UpdateServicePrincipal(
		t.Context(), created.ID, "updated", []string{entity.ServiceScopeRAGEvalRead, entity.ServiceScopeEnrichmentRead},
		"", &created.UpdatedAt, false,
	)
	require.NoError(t, err)
	assert.Equal(t, "updated", updated.Description)
	assert.Equal(t, []string{entity.ServiceScopeEnrichmentRead, entity.ServiceScopeRAGEvalRead}, updated.Scopes)

	issued, err := identityUC.IssueServiceToken(t.Context(), "", created.ID, nil, &updated.UpdatedAt, false)
	require.NoError(t, err)

	var storedHash []byte
	require.NoError(t, pg.Pool.QueryRow(t.Context(),
		`SELECT secret_hash FROM service_tokens WHERE id = $1::uuid`, issued.Token.ID).Scan(&storedHash))
	credential, err := repository.GetServiceCredentialByHash(t.Context(), storedHash)
	require.NoError(t, err)
	assert.Equal(t, issued.Token.ID, credential.Token.ID)
	_, err = repository.GetServiceCredentialByHash(t.Context(), make([]byte, sha256.Size))
	require.ErrorIs(t, err, entity.ErrServiceTokenNotFound)

	_, err = repository.RevokeServiceToken(
		t.Context(), created.ID, uuid.NewString(), "", nil, true,
	)
	require.ErrorIs(t, err, entity.ErrServiceTokenNotFound)
	revokedTokenPrincipal, err := repository.RevokeServiceToken(
		t.Context(), created.ID, issued.Token.ID, "", nil, true,
	)
	require.NoError(t, err)
	_, err = repository.RevokeServiceToken(
		t.Context(), created.ID, issued.Token.ID, "", &revokedTokenPrincipal.UpdatedAt, false,
	)
	require.NoError(t, err, "revoke token is idempotent")

	oldAuditID := uuid.NewString()
	newAuditID := uuid.NewString()

	require.NoError(t, repository.CreateServiceRequestAudit(t.Context(), entity.ServiceRequestAudit{
		ID: oldAuditID, PrincipalID: &created.ID, PrincipalName: principalName,
		Method: "GET", RouteTemplate: "/internal/old", AuthOutcome: entity.ServiceAuthOutcomeAllowed,
		StartedAt: now.Add(-91 * 24 * time.Hour),
	}))
	require.NoError(t, repository.CreateServiceRequestAudit(t.Context(), entity.ServiceRequestAudit{
		ID: newAuditID, PrincipalID: &created.ID, PrincipalName: principalName,
		Method: "GET", RouteTemplate: "/internal/new", AuthOutcome: entity.ServiceAuthOutcomeStarted,
		StartedAt: now,
	}))
	require.NoError(t, repository.FinishServiceRequestAudit(t.Context(), newAuditID, entity.ServiceAuthOutcomeAllowed, 202, now))
	require.Error(t, repository.FinishServiceRequestAudit(t.Context(), uuid.NewString(), entity.ServiceAuthOutcomeAllowed, 200, now))
	removed, err := repository.CleanupServiceRequestAudits(t.Context(), now.Add(-90*24*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, removed)

	revokedPrincipal, err := repository.RevokeServicePrincipal(t.Context(), created.ID, "", nil, true)
	require.NoError(t, err)
	require.NotNil(t, revokedPrincipal.RevokedAt)
	_, err = repository.RevokeServicePrincipal(
		t.Context(), created.ID, "", &revokedPrincipal.UpdatedAt, false,
	)
	require.NoError(t, err, "principal revoke is idempotent")
	_, err = identityUC.IssueServiceToken(t.Context(), "", created.ID, nil, nil, true)
	require.ErrorIs(t, err, entity.ErrServicePrincipalRevoked)
	_, err = identityUC.UpdateServicePrincipal(
		t.Context(), "", created.ID, "blocked", []string{entity.ServiceScopeRAGEvalRead}, nil, true,
	)
	require.ErrorIs(t, err, entity.ErrServicePrincipalRevoked)

	var actions []string

	rows, err := pg.Pool.Query(t.Context(), `
SELECT action FROM service_identity_events WHERE principal_name = $1 ORDER BY created_at, action`, principalName)
	require.NoError(t, err)

	for rows.Next() {
		var action string

		require.NoError(t, rows.Scan(&action))
		actions = append(actions, action)
	}

	rows.Close()
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, []string{
		"principal_created", "principal_updated", "token_issued", "token_revoked", "principal_revoked",
	}, actions)

	legacyRaw := "legacy-" + strings.Repeat("x", 32)
	legacyDigest := sha256.Sum256([]byte(legacyRaw))
	legacyPrincipal := entity.ServicePrincipal{
		ID: uuid.NewString(), PrincipalName: legacyName, Description: "legacy overlap",
		Scopes: []string{entity.ServiceScopeCollabDraftWrite}, CreatedAt: now, UpdatedAt: now,
	}
	legacyToken := entity.ServiceTokenRecord{
		ServiceToken: entity.ServiceToken{
			ID: uuid.NewString(), PrincipalID: legacyPrincipal.ID, TokenKind: "legacy",
			ExpiresAt: now.Add(30 * 24 * time.Hour), CreatedAt: now,
		},
		SecretHash: legacyDigest[:],
	}
	bootstrapped, err := repository.BootstrapLegacyServiceToken(t.Context(), legacyPrincipal, legacyToken)
	require.NoError(t, err)
	assert.Equal(t, legacyName, bootstrapped.PrincipalName)
	bootstrappedAgain, err := repository.BootstrapLegacyServiceToken(t.Context(), legacyPrincipal, legacyToken)
	require.NoError(t, err)
	assert.Len(t, bootstrappedAgain.Tokens, 1, "legacy bootstrap never duplicates or extends T1")
}
