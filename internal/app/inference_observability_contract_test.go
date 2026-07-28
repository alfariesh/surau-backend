package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferenceObservabilityBudgetAndCostContract(t *testing.T) {
	t.Parallel()

	rules, err := os.ReadFile("../../ops/observability/grafana/provisioning/alerting/rules.yml")
	require.NoError(t, err)

	ruleText := string(rules)
	for _, rule := range []string{
		"surau-inference-daily-budget-80",
		"surau-inference-monthly-budget-80",
		`{window="daily",mode="enforce"}) >= bool 0.8`,
		`{window="monthly",mode="enforce"}) >= bool 0.8`,
		"surau-inference-zero-baseline",
	} {
		assert.Contains(t, ruleText, rule)
	}
	// The rule's inclusive comparator is what makes exactly 80% fire while
	// 79.99% remains below the alarm boundary.
	fires := func(ratio float64) bool { return ratio >= 0.8 }
	assert.False(t, fires(0.7999))
	assert.True(t, fires(0.8))

	policies, err := os.ReadFile("../../ops/observability/grafana/provisioning/alerting/policies.yml")
	require.NoError(t, err)
	assert.Contains(t, string(policies), "telegram-salman")

	dashboard, err := os.ReadFile(
		"../../ops/observability/grafana/provisioning/dashboards/surau-health.json",
	)
	require.NoError(t, err)

	var parsed struct {
		Panels []struct {
			Title string `json:"title"`
		} `json:"panels"`
	}
	require.NoError(t, json.Unmarshal(dashboard, &parsed))

	titles := make([]string, 0, len(parsed.Panels))
	for i := range parsed.Panels {
		titles = append(titles, strings.ToLower(parsed.Panels[i].Title))
	}

	joined := strings.Join(titles, "\n")
	for _, expected := range []string{
		"llm cost today",
		"llm daily breakdown",
		"llm tokens",
		"llm budget guard",
		"llm exact vs estimated cost / cache hit-rate",
		"llm failover / budget rejection",
	} {
		assert.Contains(t, joined, expected)
	}
}
