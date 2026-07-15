package app

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQ6MassFailureAlertContractFitsFiveMinuteBudget(t *testing.T) {
	t.Parallel()

	prometheusConfig, err := os.ReadFile("../../ops/observability/prometheus.yml")
	require.NoError(t, err)
	rules, err := os.ReadFile("../../ops/observability/grafana/provisioning/alerting/rules.yml")
	require.NoError(t, err)
	policies, err := os.ReadFile("../../ops/observability/grafana/provisioning/alerting/policies.yml")
	require.NoError(t, err)
	dashboard, err := os.ReadFile("../../ops/observability/grafana/provisioning/dashboards/surau-health.json")
	require.NoError(t, err)

	assert.Contains(t, string(prometheusConfig), "scrape_interval: 15s")
	assert.Contains(t, string(rules), "uid: surau-notification-mass-failure")
	assert.Contains(t, string(rules), "for: 1m")
	assert.Contains(t, string(rules), "surau_notification_delivery_attempts_5m{result=\"failed\"}")
	assert.Contains(t, string(rules), "max without(instance, job)")
	assert.Contains(t, string(rules), ">= bool 5")
	assert.Contains(t, string(rules), ">= bool 0.5")
	assert.Contains(t, string(policies), "group_wait: 30s")
	assert.Contains(t, string(dashboard), "surau_notification_deliveries_total")
	assert.Contains(t, string(dashboard), "surau_notification_delivery_attempts_total")

	// Worst-case visibility after the threshold is crossed: one scrape, one evaluation interval,
	// the rule's pending period, then Telegram grouping.
	upperBound := 15*time.Second + time.Minute + time.Minute + 30*time.Second
	assert.LessOrEqual(t, upperBound, 5*time.Minute)
}
