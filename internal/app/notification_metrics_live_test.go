package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveNotificationMetricsSurviveCollectorRestart proves Q-6 metrics are sourced from durable
// totals rather than process memory. It is serial because it opens and closes two pools deliberately.
//
//nolint:paralleltest // restart semantics require deterministic setup/cleanup against the live DB
func TestLiveNotificationMetricsSurviveCollectorRestart(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	reasonCode := "restart_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx := context.Background()
	first, err := postgres.New(databaseURL)
	require.NoError(t, err)

	_, err = first.Pool.Exec(ctx, `
INSERT INTO notification_delivery_metric_totals (
    metric_kind, notification_type, result, reason_code, total
) VALUES ('delivery_attempt', 'new_login', 'failed', $1, 7)
ON CONFLICT (metric_kind, notification_type, result, reason_code)
DO UPDATE SET total = EXCLUDED.total`, reasonCode)
	require.NoError(t, err)

	assert.Equal(t, 7.0, gatherNotificationCounter(t, first, reasonCode))
	first.Close()

	second, err := postgres.New(databaseURL)
	require.NoError(t, err)
	assert.Equal(t, 7.0, gatherNotificationCounter(t, second, reasonCode),
		"fresh process collector must read the persisted total")
	_, cleanupErr := second.Pool.Exec(ctx, `
DELETE FROM notification_delivery_metric_totals
WHERE metric_kind = 'delivery_attempt'
  AND notification_type = 'new_login'
  AND result = 'failed'
  AND reason_code = $1`, reasonCode)
	assert.NoError(t, cleanupErr)
	second.Close()
}

// TestLiveNotificationFirstFailureBatchVisible proves the alert's rolling gauge sees the first
// batch directly from persisted attempts, without requiring an earlier Prometheus counter sample.
//
//nolint:paralleltest // serial live-DB fixture with explicit cleanup
func TestLiveNotificationFirstFailureBatchVisible(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)

	t.Cleanup(pg.Close)

	ctx := context.Background()
	userID := uuid.NewString()
	deliveryID := uuid.NewString()
	suffix := strings.ReplaceAll(userID, "-", "")
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO users (id, username, email, password_hash)
VALUES ($1, $2, $3, 'q6-alert-test')`, userID, "q6-alert-"+suffix, "q6-alert-"+suffix+"@example.test")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := pg.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
		assert.NoError(t, cleanupErr)
	})

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO notification_deliveries (
    id, user_id, notification_type, payload, idempotency_key, delivery_deadline_at
) VALUES ($1, $2, 'new_login', '{"external_ids":["fixture"],"contents":{"en":"fixture"}}', $3, $4)`,
		deliveryID, userID, uuid.NewString(), time.Now().UTC().Add(time.Hour))
	require.NoError(t, err)

	for attempt := 1; attempt <= 5; attempt++ {
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO notification_delivery_attempts (
    id, delivery_id, attempt_number, outcome, reason_code, occurred_at
) VALUES ($1, $2, $3, 'failed', 'q6_first_batch', clock_timestamp())`,
			uuid.NewString(), deliveryID, attempt)
		require.NoError(t, err)
	}

	registry := prometheus.NewPedanticRegistry()
	require.NoError(t, registry.Register(newNotificationMetricsCollector(pg.Pool)))
	families, err := registry.Gather()
	require.NoError(t, err)

	var recentFailed float64

	for _, family := range families {
		if family.GetName() != "surau_notification_delivery_attempts_5m" {
			continue
		}

		for _, metric := range family.Metric {
			if metric.Label[0].GetValue() == "failed" {
				recentFailed = metric.GetGauge().GetValue()
			}
		}
	}

	assert.GreaterOrEqual(t, recentFailed, 5.0)
}

func gatherNotificationCounter(t *testing.T, pg *postgres.Postgres, reasonCode string) float64 {
	t.Helper()

	registry := prometheus.NewPedanticRegistry()
	require.NoError(t, registry.Register(newNotificationMetricsCollector(pg.Pool)))
	families, err := registry.Gather()
	require.NoError(t, err)

	for _, family := range families {
		if family.GetName() != "surau_notification_delivery_attempts_total" {
			continue
		}

		for _, metric := range family.Metric {
			labels := make(map[string]string, len(metric.Label))
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}

			if labels["notification_type"] == "new_login" &&
				labels["result"] == "failed" && labels["reason_code"] == reasonCode {
				return metric.GetCounter().GetValue()
			}
		}
	}

	t.Fatalf("durable notification metric %s was not collected", reasonCode)

	return 0
}
