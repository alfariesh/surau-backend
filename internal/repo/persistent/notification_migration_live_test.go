package persistent

import (
	"context"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveNotificationMigrationRoundTrip executes the Q-6 migration up -> down -> up and confirms
// the durable tables, constraints, indexes, and zero metric baselines are recreated cleanly.
//
//nolint:paralleltest // destructive only within the dedicated serial SURAU_LIVE_PG test database
func TestLiveNotificationMigrationRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	up, err := os.ReadFile("../../../migrations/20260715000002_q6_notification_delivery_reliability.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260715000002_q6_notification_delivery_reliability.down.sql")
	require.NoError(t, err)

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	t.Cleanup(func() {
		_, cleanupErr := pg.Pool.Exec(context.Background(), string(up))
		assert.NoError(t, cleanupErr)
	})

	ctx := context.Background()
	_, err = pg.Pool.Exec(ctx, string(down))
	require.NoError(t, err)

	var deliveryTable any

	err = pg.Pool.QueryRow(ctx, `SELECT to_regclass('public.notification_deliveries')`).Scan(&deliveryTable)
	require.NoError(t, err)
	assert.Nil(t, deliveryTable)

	_, err = pg.Pool.Exec(ctx, string(up))
	require.NoError(t, err)

	var tables, metricBaselines int

	err = pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM unnest(ARRAY[
    to_regclass('public.notification_deliveries'),
    to_regclass('public.notification_delivery_attempts'),
    to_regclass('public.notification_delivery_metric_totals')
]) AS table_names(name)
WHERE name IS NOT NULL`).Scan(&tables)
	require.NoError(t, err)

	err = pg.Pool.QueryRow(ctx, `SELECT count(*) FROM notification_delivery_metric_totals`).Scan(&metricBaselines)
	require.NoError(t, err)

	assert.Equal(t, 3, tables)
	assert.Equal(t, 60, metricBaselines)
}
