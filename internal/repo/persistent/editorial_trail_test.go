package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEditorialTrailWriteFailuresAreCountedNotSwallowed proves a failed
// post-commit trail insert is never silent: it cannot fail the caller (the
// change already committed), so it must move the alerting counter instead.
func TestEditorialTrailWriteFailuresAreCountedNotSwallowed(t *testing.T) {
	t.Parallel()

	// Nothing listens on port 1, so every insert fails without a database.
	pool, err := pgxpool.New(t.Context(), "postgres://surau:surau@127.0.0.1:1/surau?connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	r := NewEditorialRepo(&postgres.Postgres{Pool: pool})

	auditCounter := editorialTrailWriteFailures.WithLabelValues(trailAdminAudit)
	eventCounter := editorialTrailWriteFailures.WithLabelValues(trailProductionEvent)
	auditBefore := testutil.ToFloat64(auditCounter)
	eventBefore := testutil.ToFloat64(eventCounter)

	r.audit(t.Context(), "actor-id", "publication.update", 797, nil, nil, "", map[string]any{"k": "v"})
	r.recordProductionEvent(t.Context(), "actor-id", "project-id", "production_project.update", nil, nil, nil, nil)

	assert.GreaterOrEqual(t, testutil.ToFloat64(auditCounter)-auditBefore, 1.0)
	assert.GreaterOrEqual(t, testutil.ToFloat64(eventCounter)-eventBefore, 1.0)
}

// TestEditorialTrailSeriesAreExportedAtZeroBeforeAnyFailure pins what makes
// the alert fire on the first failure after a restart: increase() only sees a
// 0 -> 1 step when the series was already scraped at 0.
func TestEditorialTrailSeriesAreExportedAtZeroBeforeAnyFailure(t *testing.T) {
	t.Parallel()

	registry := prometheus.NewRegistry()
	newEditorialTrailWriteFailures(registry)

	const expected = `
# HELP surau_editorial_trail_write_failures_total Post-commit editorial audit-log or production-event inserts that failed; the audited change itself committed.
# TYPE surau_editorial_trail_write_failures_total counter
surau_editorial_trail_write_failures_total{trail="admin_audit"} 0
surau_editorial_trail_write_failures_total{trail="production_event"} 0
`
	require.NoError(t, testutil.GatherAndCompare(
		registry, strings.NewReader(expected), "surau_editorial_trail_write_failures_total",
	))
}

// TestEditorialTrailAlertWatchesTheExportedCounter keeps the Grafana rule and
// the Go counter name in lockstep.
func TestEditorialTrailAlertWatchesTheExportedCounter(t *testing.T) {
	t.Parallel()

	rules, err := os.ReadFile("../../../ops/observability/grafana/provisioning/alerting/rules.yml")
	require.NoError(t, err)

	assert.Contains(t, string(rules), "uid: surau-editorial-trail-write")
	assert.Contains(t, string(rules), "surau_editorial_trail_write_failures_total")
}
