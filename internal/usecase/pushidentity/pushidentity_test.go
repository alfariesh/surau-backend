package pushidentity

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

type minimalUser struct {
	user     entity.User
	sessions []entity.AuthSession
}

func (s *minimalUser) ListSessions(context.Context, string) ([]entity.AuthSession, error) {
	return s.sessions, nil
}
func (s *minimalUser) GetUser(context.Context, string) (entity.User, error) { return s.user, nil }

func TestIssueUsesAuthenticatedIdentityAndES256(t *testing.T) {
	t.Parallel()

	key, path := writeTestKey(t)
	now := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	userID := "550e8400-e29b-41d4-a716-446655440000"
	users := &minimalUser{
		user:     entity.User{ID: userID, TokenVersion: 7},
		sessions: []entity.AuthSession{{FamilyID: "family-a"}},
	}
	uc, err := New(users, Options{
		AppID: "7a650cae-1c1e-4b19-a7fe-393c14b894f0", PrivateKeyFile: path,
		BindingSecret: "01234567890123456789012345678901", TTL: 15 * time.Minute,
		Now: func() time.Time { return now },
	})
	require.NoError(t, err)
	got, err := uc.Issue(context.Background(), userID, "family-a")
	require.NoError(t, err)
	require.Equal(t, userID, got.ExternalID)
	require.Equal(t, int64(900), got.ExpiresIn)
	require.NotContains(t, got.IdentityToken, userID)

	claims := &oneSignalClaims{}
	parsed, err := jwtlib.ParseWithClaims(got.IdentityToken, claims, func(token *jwtlib.Token) (any, error) {
		require.Equal(t, "ES256", token.Method.Alg())

		return &key.PublicKey, nil
	})
	require.NoError(t, err)
	require.True(t, parsed.Valid)
	require.Equal(t, userID, claims.Identity.ExternalID)
	require.Equal(t, "7a650cae-1c1e-4b19-a7fe-393c14b894f0", claims.Issuer)
}

func TestIssueRejectsRevokedSessionAndResolveFailsClosedAcrossAccounts(t *testing.T) {
	t.Parallel()

	_, path := writeTestKey(t)
	users := &minimalUser{user: entity.User{ID: "account-a", TokenVersion: 1}}
	uc, err := New(users, Options{
		AppID: "app", PrivateKeyFile: path, BindingSecret: "01234567890123456789012345678901",
		TTL: 15 * time.Minute,
	})
	require.NoError(t, err)
	_, err = uc.Issue(context.Background(), "account-a", "revoked-family")
	require.ErrorIs(t, err, ErrInactiveSession)

	users.sessions = []entity.AuthSession{{FamilyID: "family-b"}}
	users.user = entity.User{ID: "account-b", TokenVersion: 1}
	got := uc.Resolve(context.Background(), "account-b", "family-b", entity.PushRouteInput{
		SchemaVersion: entity.PushDataSchemaV1, Scope: "personal",
		Intent: "open_khatam_progress", OwnerBinding: uc.binding("account-a", 1),
	})
	require.Equal(t, "home", got.Destination)
}

func writeTestKey(t *testing.T) (key *ecdsa.PrivateKey, path string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalECPrivateKey(key)
	require.NoError(t, err)
	path = filepath.Join(t.TempDir(), "identity.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der}), 0o600))

	return key, path
}
