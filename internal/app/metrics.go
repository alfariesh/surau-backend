package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alfariesh/surau-backend/internal/backfill"
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
// Recovered panics (F1-C) are counted under their own result label.
func recordLoopRun(loop string, err error) {
	if err != nil {
		result := "error"
		if errors.Is(err, errLoopPanic) {
			result = "panic"
		}

		loopRuns.WithLabelValues(loop, result).Inc()

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

const (
	backfillStatsCacheTTL = 60 * time.Second
	backfillQueryTimeout  = 5 * time.Second
)

// backfillJobStats is one cached snapshot row for the backfill collector.
type backfillJobStats struct {
	job        string
	rowsTotal  float64
	rowsDone   float64
	pending    float64
	lastUpdate float64
}

// backfillCollector exposes resumable-backfill progress (F1-H) at scrape
// time from the backfill_jobs checkpoint table plus a live pending count per
// registered job (drift visibility after completion). The CLI only writes
// Postgres; the already-scraped app surfaces it — no extra scrape target.
type backfillCollector struct {
	pool *pgxpool.Pool

	mu      sync.Mutex
	fetched time.Time
	stats   []backfillJobStats

	totalDesc      *prometheus.Desc
	doneDesc       *prometheus.Desc
	pendingDesc    *prometheus.Desc
	lastUpdateDesc *prometheus.Desc
}

func newBackfillCollector(pool *pgxpool.Pool) *backfillCollector {
	labels := []string{"job"}

	return &backfillCollector{
		pool: pool,
		totalDesc: prometheus.NewDesc("surau_backfill_rows_total",
			"Total rows the backfill job planned to process.", labels, nil),
		doneDesc: prometheus.NewDesc("surau_backfill_rows_done",
			"Rows the backfill job has processed so far.", labels, nil),
		pendingDesc: prometheus.NewDesc("surau_backfill_pending_rows",
			"Rows currently still needing the backfill (drift stays visible after completion).", labels, nil),
		lastUpdateDesc: prometheus.NewDesc("surau_backfill_last_update_timestamp_seconds",
			"Unix time of the backfill job's last checkpoint write.", labels, nil),
	}
}

func (c *backfillCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.totalDesc, c.doneDesc, c.pendingDesc, c.lastUpdateDesc} {
		ch <- desc
	}
}

func (c *backfillCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.fetched) > backfillStatsCacheTTL {
		if stats, err := c.fetch(); err == nil {
			c.stats = stats
			c.fetched = time.Now()
		}
		// On error keep the previous snapshot (stale beats scrape failure).
	}

	for _, s := range c.stats {
		ch <- prometheus.MustNewConstMetric(c.totalDesc, prometheus.GaugeValue, s.rowsTotal, s.job)

		ch <- prometheus.MustNewConstMetric(c.doneDesc, prometheus.GaugeValue, s.rowsDone, s.job)

		ch <- prometheus.MustNewConstMetric(c.pendingDesc, prometheus.GaugeValue, s.pending, s.job)

		ch <- prometheus.MustNewConstMetric(c.lastUpdateDesc, prometheus.GaugeValue, s.lastUpdate, s.job)
	}
}

func (c *backfillCollector) fetch() ([]backfillJobStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), backfillQueryTimeout)
	defer cancel()

	rows, err := c.pool.Query(ctx, `
SELECT job_name, rows_total, rows_done, extract(epoch FROM updated_at)
FROM backfill_jobs
ORDER BY job_name`)
	if err != nil {
		return nil, fmt.Errorf("backfill metrics: query checkpoints: %w", err)
	}
	defer rows.Close()

	byJob := make(map[string]backfillJobStats)

	for rows.Next() {
		var s backfillJobStats
		if err := rows.Scan(&s.job, &s.rowsTotal, &s.rowsDone, &s.lastUpdate); err != nil {
			return nil, fmt.Errorf("backfill metrics: scan checkpoint: %w", err)
		}

		byJob[s.job] = s
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("backfill metrics: checkpoints rows: %w", err)
	}

	// Live pending count per REGISTERED job: catches drift (new unprocessed
	// rows) even when the checkpoint says completed.
	for _, job := range backfill.Jobs() {
		pending, err := job.CountRemaining(ctx, c.pool)
		if err != nil {
			return nil, fmt.Errorf("backfill metrics: pending %s: %w", job.Name(), err)
		}

		s, ok := byJob[job.Name()]
		if !ok {
			s = backfillJobStats{job: job.Name()}
		}

		s.pending = float64(pending)
		byJob[s.job] = s
	}

	stats := make([]backfillJobStats, 0, len(byJob))
	for _, s := range byJob {
		stats = append(stats, s)
	}

	return stats, nil
}

func registerBackfillMetrics(pool *pgxpool.Pool) {
	prometheus.MustRegister(newBackfillCollector(pool))
}
