package persistent

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthSessionsMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260610000001_create_auth_sessions.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS auth_sessions")
	assert.Contains(t, upSQL, "refresh_token_hash CHAR(64) NOT NULL UNIQUE")
	assert.Contains(t, upSQL, "family_id UUID NOT NULL")
	assert.Contains(t, upSQL, "ON auth_sessions (user_id) WHERE revoked_at IS NULL")
	assert.Contains(t, upSQL, "ON auth_sessions (family_id) WHERE revoked_at IS NULL")
	assert.Contains(t, upSQL, "ON auth_sessions (expires_at)")

	down, err := os.ReadFile("../../../migrations/20260610000001_create_auth_sessions.down.sql")
	require.NoError(t, err)
	assert.Contains(t, string(down), "DROP TABLE IF EXISTS auth_sessions")
}

func TestAuthLoginLockoutsMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260610000002_create_auth_login_lockouts.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS auth_login_lockouts")
	assert.Contains(t, upSQL, "key_hash CHAR(64) PRIMARY KEY")
	assert.Contains(t, upSQL, "locked_until TIMESTAMP NULL")

	down, err := os.ReadFile("../../../migrations/20260610000002_create_auth_login_lockouts.down.sql")
	require.NoError(t, err)
	assert.Contains(t, string(down), "DROP TABLE IF EXISTS auth_login_lockouts")
}

func TestDropUsersEmailKeyMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260610000003_drop_users_email_key.up.sql")
	require.NoError(t, err)
	assert.Contains(t, string(up), "DROP CONSTRAINT IF EXISTS users_email_key")

	down, err := os.ReadFile("../../../migrations/20260610000003_drop_users_email_key.down.sql")
	require.NoError(t, err)
	assert.Contains(t, string(down), "ADD CONSTRAINT users_email_key UNIQUE (email)")
}
