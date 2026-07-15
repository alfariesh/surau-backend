package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	v1 "github.com/alfariesh/surau-backend/internal/controller/restapi/v1"
	"github.com/alfariesh/surau-backend/internal/entity"
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
