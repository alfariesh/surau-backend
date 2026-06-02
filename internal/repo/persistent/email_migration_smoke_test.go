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
