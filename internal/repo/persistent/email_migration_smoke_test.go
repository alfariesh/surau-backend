package persistent

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmailManagementMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260531000007_email_management.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	for _, table := range []string{
		"email_templates",
		"email_template_versions",
		"email_event_settings",
		"email_messages",
		"email_subscriptions",
		"email_suppressions",
		"email_campaigns",
		"email_campaign_recipients",
	} {
		assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS "+table, table)
	}
	for _, key := range []string{
		"auth_verification",
		"auth_password_reset",
		"auth_email_change_verification",
		"auth_password_changed",
		"auth_email_verified",
		"auth_new_login",
		"auth_failed_login",
		"auth_role_changed",
		"auth_email_changed",
		"auth_account_deleted",
	} {
		assert.Contains(t, upSQL, key)
	}
	assert.Contains(t, upSQL, "CHECK (category IN ('transactional', 'marketing'))")
	assert.Contains(t, upSQL, "CHECK (lang IN ('id', 'en', 'ar'))")
	assert.Contains(t, upSQL, "CHECK (status IN ('draft', 'scheduled', 'sending', 'sent', 'cancelled'))")
	assert.Contains(t, upSQL, "CREATE UNIQUE INDEX IF NOT EXISTS idx_email_suppressions_email_scope")

	down, err := os.ReadFile("../../../migrations/20260531000007_email_management.down.sql")
	require.NoError(t, err)
	downSQL := string(down)

	for _, table := range []string{
		"email_campaign_recipients",
		"email_campaigns",
		"email_suppressions",
		"email_subscriptions",
		"email_messages",
		"email_event_settings",
		"email_template_versions",
		"email_templates",
	} {
		assert.Contains(t, downSQL, "DROP TABLE IF EXISTS "+table, table)
	}
}

func TestEmailOTPMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260605000001_add_email_otp.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "ALTER TABLE email_verification_tokens")
	assert.Contains(t, upSQL, "ALTER TABLE email_change_tokens")
	assert.Contains(t, upSQL, "otp_hash")
	assert.Contains(t, upSQL, "otp_expires_at")
	assert.Contains(t, upSQL, "{{.otp}}")
	assert.Contains(t, upSQL, "{{.otp_duration}}")

	down, err := os.ReadFile("../../../migrations/20260605000001_add_email_otp.down.sql")
	require.NoError(t, err)
	downSQL := string(down)

	assert.Contains(t, downSQL, "DROP COLUMN IF EXISTS otp_expires_at")
	assert.Contains(t, downSQL, "DROP COLUMN IF EXISTS otp_hash")
	assert.Contains(t, downSQL, "array_remove(array_remove(v.required_variables, 'otp'), 'otp_duration')")
}

func TestEmailDeliveryEventsMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260605000002_email_delivery_events.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS email_delivery_events")
	assert.Contains(t, upSQL, "dedupe_key")
	assert.Contains(t, upSQL, "raw_payload JSONB")
	assert.Contains(t, upSQL, "CHECK (event_type IN ('bounce_hard', 'complaint'))")
	assert.Contains(t, upSQL, "CREATE UNIQUE INDEX IF NOT EXISTS idx_email_delivery_events_dedupe")
	assert.Contains(t, upSQL, "idx_email_delivery_events_message")
	assert.Contains(t, upSQL, "idx_email_delivery_events_campaign")

	down, err := os.ReadFile("../../../migrations/20260605000002_email_delivery_events.down.sql")
	require.NoError(t, err)
	assert.Contains(t, string(down), "DROP TABLE IF EXISTS email_delivery_events")
}

func TestTransactionalEmailRetryMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260605000003_transactional_email_retry.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "ALTER TABLE email_messages")
	assert.Contains(t, upSQL, "ADD COLUMN IF NOT EXISTS html TEXT NOT NULL DEFAULT ''")
	assert.Contains(t, upSQL, "ADD COLUMN IF NOT EXISTS text TEXT NOT NULL DEFAULT ''")
	assert.Contains(t, upSQL, "ADD COLUMN IF NOT EXISTS critical BOOLEAN NOT NULL DEFAULT false")
	assert.Contains(t, upSQL, "ADD COLUMN IF NOT EXISTS headers JSONB NOT NULL DEFAULT '{}'::jsonb")
	assert.Contains(t, upSQL, "CREATE INDEX IF NOT EXISTS idx_email_messages_transactional_retry_due")
	assert.Contains(t, upSQL, "WHERE category = 'transactional' AND status = 'queued'")

	down, err := os.ReadFile("../../../migrations/20260605000003_transactional_email_retry.down.sql")
	require.NoError(t, err)
	downSQL := string(down)

	assert.Contains(t, downSQL, "DROP INDEX IF EXISTS idx_email_messages_transactional_retry_due")
	assert.Contains(t, downSQL, "DROP COLUMN IF EXISTS headers")
	assert.Contains(t, downSQL, "DROP COLUMN IF EXISTS critical")
	assert.Contains(t, downSQL, "DROP COLUMN IF EXISTS text")
	assert.Contains(t, downSQL, "DROP COLUMN IF EXISTS html")
}

func TestEmailProviderPollCursorsMigrationSmoke(t *testing.T) {
	t.Parallel()

	up, err := os.ReadFile("../../../migrations/20260605000004_email_provider_poll_cursors.up.sql")
	require.NoError(t, err)
	upSQL := string(up)

	assert.Contains(t, upSQL, "CREATE TABLE IF NOT EXISTS email_provider_poll_cursors")
	assert.Contains(t, upSQL, "provider VARCHAR(64) NOT NULL")
	assert.Contains(t, upSQL, "cursor_key VARCHAR(255) NOT NULL")
	assert.Contains(t, upSQL, "last_polled_at TIMESTAMP NOT NULL")
	assert.Contains(t, upSQL, "PRIMARY KEY (provider, cursor_key)")

	down, err := os.ReadFile("../../../migrations/20260605000004_email_provider_poll_cursors.down.sql")
	require.NoError(t, err)
	assert.Contains(t, string(down), "DROP TABLE IF EXISTS email_provider_poll_cursors")
}
