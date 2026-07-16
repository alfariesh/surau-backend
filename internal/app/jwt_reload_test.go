package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/pkg/jwt"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReloadJWTKeysetSwitchesSignerWithoutRestart(t *testing.T) {
	t.Parallel()

	const (
		oldSecret = "old-secret-0123456789-abcdefghijkl"
		newSecret = "new-secret-0123456789-abcdefghijkl"
	)

	path := filepath.Join(t.TempDir(), "keyset.json")
	writeAppJWTKeyset(t, path, jwt.Keyset{
		Version:   jwt.KeysetVersion,
		ActiveKID: "old",
		LegacyKID: "old",
		Keys:      map[string]string{"old": oldSecret},
	})
	manager, err := jwt.NewFromKeysetFile(path, time.Hour, "", "")
	require.NoError(t, err)

	oldToken, err := manager.GenerateToken("old-user")
	require.NoError(t, err)
	assert.Equal(t, "old", appJWTTokenKID(t, oldToken))

	writeAppJWTKeyset(t, path, jwt.Keyset{
		Version:   jwt.KeysetVersion,
		ActiveKID: "new",
		LegacyKID: "old",
		Keys: map[string]string{
			"old": oldSecret,
			"new": newSecret,
		},
	})
	reloadJWTKeyset(manager, testLogger())

	newToken, err := manager.GenerateToken("new-user")
	require.NoError(t, err)
	assert.Equal(t, "new", appJWTTokenKID(t, newToken))

	_, err = manager.ParseToken(oldToken)
	require.NoError(t, err)
	_, err = manager.ParseToken(newToken)
	require.NoError(t, err)

	writeAppJWTKeyset(t, path, jwt.Keyset{
		Version:   jwt.KeysetVersion,
		ActiveKID: "broken",
		Keys:      map[string]string{"broken": "short"},
	})
	reloadJWTKeyset(manager, testLogger())

	lastKnownGoodToken, err := manager.GenerateToken("still-new-user")
	require.NoError(t, err)
	assert.Equal(t, "new", appJWTTokenKID(t, lastKnownGoodToken))
}

func writeAppJWTKeyset(t *testing.T, path string, keyset jwt.Keyset) {
	t.Helper()

	contents, err := json.Marshal(keyset)
	require.NoError(t, err)

	temporary := path + ".tmp"
	require.NoError(t, os.WriteFile(temporary, contents, 0o600))
	require.NoError(t, os.Rename(temporary, path))
}

func appJWTTokenKID(t *testing.T, tokenString string) string {
	t.Helper()

	token, _, err := jwtlib.NewParser().ParseUnverified(tokenString, jwtlib.MapClaims{})
	require.NoError(t, err)

	keyID, ok := token.Header["kid"].(string)
	require.True(t, ok)

	return keyID
}
