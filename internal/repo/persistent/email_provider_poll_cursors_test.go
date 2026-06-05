package persistent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEmailProviderPollCursorSQL(t *testing.T) {
	t.Parallel()

	getSQL := getEmailProviderPollCursorSQL()
	assert.Contains(t, getSQL, "FROM email_provider_poll_cursors")
	assert.Contains(t, getSQL, "WHERE provider = $1 AND cursor_key = $2")

	upsertSQL := upsertEmailProviderPollCursorSQL()
	assert.Contains(t, upsertSQL, "INSERT INTO email_provider_poll_cursors")
	assert.Contains(t, upsertSQL, "provider, cursor_key, last_polled_at")
	assert.Contains(t, upsertSQL, "ON CONFLICT (provider, cursor_key) DO UPDATE SET")
	assert.Contains(t, upsertSQL, "last_polled_at = EXCLUDED.last_polled_at")
	assert.Contains(t, upsertSQL, "RETURNING")
}
