package app

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Background-loop health metrics (F1-B): every loop stamps its last success
// so a silently dead loop is visible on the dashboard and alertable.
//
//nolint:gochecknoglobals // process-wide Prometheus instruments (promauto pattern)
var (
	loopLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "surau_loop_last_success_timestamp_seconds",
		Help: "Unix time of the last successful pass of each background loop.",
	}, []string{"loop"})

	loopRuns = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_loop_runs_total",
		Help: "Background loop passes by result.",
	}, []string{"loop", "result"})
)

// recordLoopRun stamps one background-loop pass; call with the pass error.
func recordLoopRun(loop string, err error) {
	if err != nil {
		loopRuns.WithLabelValues(loop, "error").Inc()

		return
	}

	loopRuns.WithLabelValues(loop, "success").Inc()
	loopLastSuccess.WithLabelValues(loop).SetToCurrentTime()
}

const (
	emailQueueStatsCacheTTL = 10 * time.Second
	emailQueueQueryTimeout  = 5 * time.Second
)

// emailQueueCollector exposes email-pipeline gauges (queue depth, oldest due
// message age, terminal failures) straight from Postgres at scrape time, with
// a short cache so bursts of scrapes cost one query.
type emailQueueCollector struct {
	pool *pgxpool.Pool

	mu       sync.Mutex
	fetched  time.Time
	queued   float64
	oldest   float64
	failed   float64
	retrying float64

	queuedDesc   *prometheus.Desc
	oldestDesc   *prometheus.Desc
	failedDesc   *prometheus.Desc
	retryingDesc *prometheus.Desc
}

func newEmailQueueCollector(pool *pgxpool.Pool) *emailQueueCollector {
	return &emailQueueCollector{
		pool: pool,
		queuedDesc: prometheus.NewDesc("surau_email_queued",
			"Email messages currently queued.", nil, nil),
		oldestDesc: prometheus.NewDesc("surau_email_oldest_due_seconds",
			"Age of the oldest queued message that is already due for dispatch.", nil, nil),
		failedDesc: prometheus.NewDesc("surau_email_failed",
			"Email messages in terminal failed status (dead letters).", nil, nil),
		retryingDesc: prometheus.NewDesc("surau_email_retrying",
			"Queued messages that already failed at least one attempt.", nil, nil),
	}
}

func (c *emailQueueCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.queuedDesc, c.oldestDesc, c.failedDesc, c.retryingDesc} {
		ch <- desc
	}
}

func (c *emailQueueCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.fetched) > emailQueueStatsCacheTTL {
		ctx, cancel := context.WithTimeout(context.Background(), emailQueueQueryTimeout)
		defer cancel()

		// Stale values are better than a scrape failure: on error keep the
		// previous numbers (fetched stays old, so the next scrape retries).
		err := c.pool.QueryRow(ctx, `
SELECT
    count(*) FILTER (WHERE status = 'queued'),
    coalesce(extract(epoch FROM now() - min(scheduled_at)
        FILTER (WHERE status = 'queued' AND scheduled_at <= now())), 0),
    count(*) FILTER (WHERE status = 'failed'),
    count(*) FILTER (WHERE status = 'queued' AND attempts > 0)
FROM email_messages`).Scan(&c.queued, &c.oldest, &c.failed, &c.retrying)
		if err == nil {
			c.fetched = time.Now()
		}
	}

	ch <- prometheus.MustNewConstMetric(c.queuedDesc, prometheus.GaugeValue, c.queued)

	ch <- prometheus.MustNewConstMetric(c.oldestDesc, prometheus.GaugeValue, c.oldest)

	ch <- prometheus.MustNewConstMetric(c.failedDesc, prometheus.GaugeValue, c.failed)

	ch <- prometheus.MustNewConstMetric(c.retryingDesc, prometheus.GaugeValue, c.retrying)
}

func registerEmailQueueMetrics(pool *pgxpool.Pool) {
	// MustRegister panics on double-registration; app.Run is called once per
	// process (tests construct their own registries), so this is safe.
	prometheus.MustRegister(newEmailQueueCollector(pool))
}
