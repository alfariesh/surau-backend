package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOneSignalErasureMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260723000001_onesignal_user_erasures.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260723000001_onesignal_user_erasures.down.sql")
	require.NoError(t, err)

	upSQL := string(up)
	assert.Contains(t, upSQL, "CREATE TABLE onesignal_user_erasures")
	assert.Contains(t, upSQL, "status IN ('pending', 'verifying', 'verified')")
	assert.Contains(t, upSQL, "external_id_hash CHAR(64)")
	assert.Contains(t, upSQL, "external_id_ciphertext TEXT")
	assert.Contains(t, upSQL, "onesignal_user_erasures_due_idx")
	assert.Contains(t, upSQL, "onesignal_user_erasures_verified_retention_idx")
	assert.Contains(t, upSQL, "CREATE TABLE onesignal_user_erasure_attempts")
	assert.NotContains(t, strings.ToLower(upSQL), "external_id uuid")

	downSQL := string(down)
	attemptsDrop := strings.Index(downSQL, "DROP TABLE IF EXISTS onesignal_user_erasure_attempts")
	erasuresDrop := strings.Index(downSQL, "DROP TABLE IF EXISTS onesignal_user_erasures")

	require.NotEqual(t, -1, attemptsDrop)
	require.NotEqual(t, -1, erasuresDrop)
	assert.Less(t, attemptsDrop, erasuresDrop)
}
