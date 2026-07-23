package app

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOneSignalErasureAlertsRoutePrivacyFailuresToTelegram(t *testing.T) {
	t.Parallel()

	rules, err := os.ReadFile("../../ops/observability/grafana/provisioning/alerting/rules.yml")
	require.NoError(t, err)
	policies, err := os.ReadFile("../../ops/observability/grafana/provisioning/alerting/policies.yml")
	require.NoError(t, err)

	rulesText := string(rules)
	assert.Contains(t, rulesText, "uid: surau-onesignal-erasure-stale")
	assert.Contains(t, rulesText, "max(surau_onesignal_erasure_stale) > bool 0")
	assert.Contains(t, rulesText, "uid: surau-onesignal-erasure-provider-auth")
	assert.Contains(t, rulesText, `reason_code="unauthorized"`)
	assert.Contains(t, rulesText, "source: privacy")
	assert.Contains(t, string(policies), "receiver: telegram-salman")
}
