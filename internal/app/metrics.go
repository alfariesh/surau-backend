package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/alfariesh/surau-backend/internal/backfill"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/jwt"
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

	// Citable Unit registry audit (phase-1b B-1 AC-3). Violations are registry
	// invariant breaches and alert via Grafana (> 0 => Telegram); info counts
	// are dashboard-only observations (legacy dangling owned by B-3, stale
	// books awaiting a backfill re-run).
	citableAuditViolations = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "surau_citable_audit_violations",
		Help: "Citable-unit registry invariant violations by check; any nonzero value should alert.",
	}, []string{"check"})

	citableAuditInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "surau_citable_audit_info",
		Help: "Citable-unit audit observations (no alert): stale books and pre-registry legacy dangling citations.",
	}, []string{"check"})

	citableUnits = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "surau_citable_units",
		Help: "Citable units in the registry by lifecycle.",
	}, []string{"lifecycle"})

	jwtKeysetReloads = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "surau_jwt_keyset_reloads_total",
		Help: "JWT keyset reload attempts by bounded result.",
	}, []string{"result"})

	jwtKeysetKeys = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "surau_jwt_keyset_keys",
		Help: "Number of HS256 verification keys in the current JWT keyset.",
	})

	jwtLegacyCompatibility = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "surau_jwt_legacy_compatibility_enabled",
		Help: "Whether living JWTs without kid are accepted through the explicit legacy key (1=yes).",
	})

	jwtKeysetLastReload = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "surau_jwt_keyset_last_reload_timestamp_seconds",
		Help: "Unix time of the last successful JWT keyset reload.",
	})
)

func recordJWTKeysetStatus(status jwt.KeysetStatus) {
	jwtKeysetKeys.Set(float64(len(status.KeyIDs)))

	if status.LegacyKID == "" {
		jwtLegacyCompatibility.Set(0)

		return
	}

	jwtLegacyCompatibility.Set(1)
}

func recordJWTKeysetReload(success bool, status jwt.KeysetStatus) {
	result := "error"
	if success {
		result = "success"

		jwtKeysetLastReload.SetToCurrentTime()
	}

	jwtKeysetReloads.WithLabelValues(result).Inc()
	recordJWTKeysetStatus(status)
}

// recordCitableAudit publishes one audit pass and returns the violation total
// (the number the Grafana rule alerts on).
func recordCitableAudit(report *entity.CitableAuditReport) int64 {
	v := report.Violations
	citableAuditViolations.WithLabelValues("book_gone").Set(float64(v.BookGone))
	citableAuditViolations.WithLabelValues("superseded_no_successor").Set(float64(v.SupersededNoSuccessor))
	citableAuditViolations.WithLabelValues("active_with_successor").Set(float64(v.ActiveWithSuccessor))
	citableAuditViolations.WithLabelValues("lineage_cycle").Set(float64(v.LineageCycle))
	citableAuditViolations.WithLabelValues("hash_mismatch").Set(float64(v.HashMismatch))
	citableAuditViolations.WithLabelValues("anchor_malformed").Set(float64(v.AnchorMalformed))
	citableAuditViolations.WithLabelValues("footnote_parent").Set(float64(v.FootnoteParent))
	citableAuditViolations.WithLabelValues("quran_binding").Set(float64(v.QuranBinding))
	citableAuditViolations.WithLabelValues("quran_interpretive").Set(float64(v.QuranInterpretive))
	citableAuditViolations.WithLabelValues("interpretive_safety").Set(float64(v.InterpretiveSafety))
	citableAuditViolations.WithLabelValues("rag_projection_dangling").Set(float64(v.RAGProjectionDangling))
	citableAuditViolations.WithLabelValues("approved_mention_anchor").Set(float64(v.ApprovedMentionAnchor))
	citableAuditViolations.WithLabelValues("mention_unit_dangling").Set(float64(v.MentionUnitDangling))
	citableAuditViolations.WithLabelValues("mention_binding_mismatch").Set(float64(v.MentionBindingMismatch))
	citableAuditViolations.WithLabelValues("cross_reference_anchor").Set(float64(v.CrossReferenceAnchor))

	citableAuditInfo.WithLabelValues("stale_books").Set(float64(report.Info.StaleBooks))
	citableAuditInfo.WithLabelValues("stale_quran_surahs").Set(float64(report.Info.StaleQuranSurahs))
	citableAuditInfo.WithLabelValues("legacy_quran_book_references").Set(float64(report.Info.LegacyQuranBookReferences))
	citableAuditInfo.WithLabelValues("legacy_knowledge_mentions").Set(float64(report.Info.LegacyKnowledgeMentions))
	citableAuditInfo.WithLabelValues("legacy_knowledge_source_spans").Set(float64(report.Info.LegacyKnowledgeSourceSpans))
	citableAuditInfo.WithLabelValues("legacy_knowledge_rejections").Set(float64(report.Info.LegacyKnowledgeRejections))

	for _, lifecycle := range []string{
		entity.UnitLifecycleActive, entity.UnitLifecycleSuperseded, entity.UnitLifecycleTombstoned,
	} {
		citableUnits.WithLabelValues(lifecycle).Set(float64(report.UnitsByLifecycle[lifecycle]))
	}

	return v.BookGone + v.SupersededNoSuccessor + v.ActiveWithSuccessor + v.LineageCycle +
		v.HashMismatch + v.AnchorMalformed + v.FootnoteParent + v.QuranBinding + v.QuranInterpretive +
		v.InterpretiveSafety + v.RAGProjectionDangling + v.ApprovedMentionAnchor +
		v.MentionUnitDangling + v.MentionBindingMismatch + v.CrossReferenceAnchor
}

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
	emailQueueStatsCacheTTL   = 10 * time.Second
	emailQueueQueryTimeout    = 5 * time.Second
	notificationStatsCacheTTL = 10 * time.Second
	notificationQueryTimeout  = 5 * time.Second
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

type notificationMetricSample struct {
	kind             string
	notificationType string
	result           string
	reasonCode       string
	total            float64
}

// notificationMetricsCollector exports database-backed cumulative counters. Unlike process-local
// counters, an accepted/failed attempt cannot disappear when the application restarts before the
// next Prometheus scrape.
type notificationMetricsCollector struct {
	pool *pgxpool.Pool

	mu             sync.Mutex
	fetched        time.Time
	samples        []notificationMetricSample
	recentAccepted float64
	recentFailed   float64

	attemptsDesc   *prometheus.Desc
	deliveriesDesc *prometheus.Desc
	skipsDesc      *prometheus.Desc
	recentDesc     *prometheus.Desc
}

func newNotificationMetricsCollector(pool *pgxpool.Pool) *notificationMetricsCollector {
	return &notificationMetricsCollector{
		pool: pool,
		attemptsDesc: prometheus.NewDesc(
			"surau_notification_delivery_attempts_total",
			"OneSignal provider attempts by notification type, result, and bounded reason code.",
			[]string{"notification_type", "result", "reason_code"},
			nil,
		),
		deliveriesDesc: prometheus.NewDesc(
			"surau_notification_deliveries_total",
			"Unique logical OneSignal deliveries in a terminal accepted or failed state.",
			[]string{"notification_type", "status"},
			nil,
		),
		skipsDesc: prometheus.NewDesc(
			"surau_notification_reminder_skips_total",
			"Reminder evaluations skipped before a OneSignal provider attempt.",
			[]string{"reason"},
			nil,
		),
		recentDesc: prometheus.NewDesc(
			"surau_notification_delivery_attempts_5m",
			"OneSignal provider attempts observed in the rolling five-minute alert window.",
			[]string{"result"},
			nil,
		),
	}
}

func (c *notificationMetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.attemptsDesc

	ch <- c.deliveriesDesc

	ch <- c.skipsDesc

	ch <- c.recentDesc
}

func (c *notificationMetricsCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.fetched) > notificationStatsCacheTTL {
		c.refresh()
	}

	for _, sample := range c.samples {
		c.emit(ch, sample)
	}

	ch <- prometheus.MustNewConstMetric(c.recentDesc, prometheus.GaugeValue, c.recentAccepted, "accepted")

	ch <- prometheus.MustNewConstMetric(c.recentDesc, prometheus.GaugeValue, c.recentFailed, "failed")
}

func (c *notificationMetricsCollector) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), notificationQueryTimeout)
	defer cancel()

	rows, err := c.pool.Query(ctx, `
SELECT metric_kind, notification_type, result, reason_code, total
FROM notification_delivery_metric_totals
ORDER BY metric_kind, notification_type, result, reason_code`)
	if err != nil {
		return
	}
	defer rows.Close()

	fresh := make([]notificationMetricSample, 0)

	for rows.Next() {
		var sample notificationMetricSample
		if err := rows.Scan(
			&sample.kind,
			&sample.notificationType,
			&sample.result,
			&sample.reasonCode,
			&sample.total,
		); err != nil {
			return
		}

		fresh = append(fresh, sample)
	}

	if rows.Err() != nil {
		return
	}

	rows.Close()

	var recentAccepted, recentFailed float64
	if err := c.pool.QueryRow(ctx, `
SELECT
    count(*) FILTER (WHERE outcome = 'accepted'),
    count(*) FILTER (WHERE outcome = 'failed')
FROM notification_delivery_attempts
WHERE occurred_at >= clock_timestamp() - INTERVAL '5 minutes'
  AND occurred_at <= clock_timestamp()`).Scan(
		&recentAccepted,
		&recentFailed,
	); err != nil {
		return
	}

	c.samples = fresh
	c.recentAccepted = recentAccepted
	c.recentFailed = recentFailed
	c.fetched = time.Now()
}

func (c *notificationMetricsCollector) emit(ch chan<- prometheus.Metric, sample notificationMetricSample) {
	switch sample.kind {
	case "delivery_attempt":
		ch <- prometheus.MustNewConstMetric(
			c.attemptsDesc,
			prometheus.CounterValue,
			sample.total,
			sample.notificationType,
			sample.result,
			sample.reasonCode,
		)
	case "delivery":
		ch <- prometheus.MustNewConstMetric(
			c.deliveriesDesc,
			prometheus.CounterValue,
			sample.total,
			sample.notificationType,
			sample.result,
		)
	case "reminder_skip":
		ch <- prometheus.MustNewConstMetric(
			c.skipsDesc,
			prometheus.CounterValue,
			sample.total,
			sample.reasonCode,
		)
	}
}

func registerNotificationMetrics(pool *pgxpool.Pool) {
	prometheus.MustRegister(newNotificationMetricsCollector(pool))
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
	prometheus.MustRegister(newCitableCatalogCollector(pool))
}

type citableCatalogSnapshot struct {
	target       float64
	materialized float64
	missing      float64
	stale        float64
	queue        map[string]float64
	attempts     map[string]float64
	duration     map[string]float64
}

// citableCatalogCollector is the K-1 proof surface: one bounded set of labels
// reports raw-published coverage and durable queue outcomes without book ids.
type citableCatalogCollector struct {
	pool *pgxpool.Pool

	mu       sync.Mutex
	fetched  time.Time
	snapshot citableCatalogSnapshot

	coverageDesc *prometheus.Desc
	queueDesc    *prometheus.Desc
	attemptsDesc *prometheus.Desc
	durationDesc *prometheus.Desc
}

func newCitableCatalogCollector(pool *pgxpool.Pool) *citableCatalogCollector {
	return &citableCatalogCollector{
		pool: pool,
		coverageDesc: prometheus.NewDesc(
			"surau_citable_catalog_books",
			"Raw-published kitab catalog coverage by bounded state.",
			[]string{"state"}, nil,
		),
		queueDesc: prometheus.NewDesc(
			"surau_citable_catalog_queue_items",
			"Durable K-1 catalog queue items by job and state.",
			[]string{"job", "state"}, nil,
		),
		attemptsDesc: prometheus.NewDesc(
			"surau_citable_catalog_queue_attempts",
			"Total durable K-1 catalog queue attempts by job.",
			[]string{"job"}, nil,
		),
		durationDesc: prometheus.NewDesc(
			"surau_citable_catalog_completed_duration_seconds",
			"Cumulative duration of completed K-1 catalog items by job.",
			[]string{"job"}, nil,
		),
	}
}

func (c *citableCatalogCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{c.coverageDesc, c.queueDesc, c.attemptsDesc, c.durationDesc} {
		ch <- desc
	}
}

func (c *citableCatalogCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.fetched) > backfillStatsCacheTTL {
		if snapshot, err := c.fetch(); err == nil {
			c.snapshot = snapshot
			c.fetched = time.Now()
		}
	}

	coverage := map[string]float64{
		"target":       c.snapshot.target,
		"materialized": c.snapshot.materialized,
		"missing":      c.snapshot.missing,
		"stale":        c.snapshot.stale,
	}
	for state, value := range coverage {
		ch <- prometheus.MustNewConstMetric(c.coverageDesc, prometheus.GaugeValue, value, state)
	}

	for key, value := range c.snapshot.queue {
		job, state, ok := splitMetricKey(key)
		if ok {
			ch <- prometheus.MustNewConstMetric(c.queueDesc, prometheus.GaugeValue, value, job, state)
		}
	}

	for job, value := range c.snapshot.attempts {
		ch <- prometheus.MustNewConstMetric(c.attemptsDesc, prometheus.GaugeValue, value, job)
	}

	for job, value := range c.snapshot.duration {
		ch <- prometheus.MustNewConstMetric(c.durationDesc, prometheus.GaugeValue, value, job)
	}
}

//nolint:funlen // coverage and queue metric projections remain one consistent snapshot
func (c *citableCatalogCollector) fetch() (citableCatalogSnapshot, error) {
	ctx, cancel := context.WithTimeout(context.Background(), backfillQueryTimeout)
	defer cancel()

	snapshot := citableCatalogSnapshot{
		queue:    map[string]float64{},
		attempts: map[string]float64{},
		duration: map[string]float64{},
	}

	err := c.pool.QueryRow(ctx, `
SELECT COUNT(*)::float8,
       COUNT(*) FILTER (
           WHERE b.units_derived_at IS NOT NULL
             AND b.units_stale_at IS NULL
             AND b.units_derivation_profile_version = $1
             AND EXISTS (
                 SELECT 1 FROM citable_units unit
                 WHERE unit.book_id = b.id
                   AND unit.lifecycle = 'active'
                   AND unit.content_role = 'book_page'
             )
       )::float8,
       COUNT(*) FILTER (WHERE b.units_derived_at IS NULL)::float8,
       COUNT(*) FILTER (
           WHERE b.units_stale_at IS NOT NULL
              OR b.units_derivation_profile_version IS DISTINCT FROM $1
       )::float8
FROM book_publications publication
JOIN books b ON b.id = publication.book_id
WHERE publication.status = 'published' AND b.is_deleted = FALSE`, entity.KitabUnitDerivationProfileVersion).
		Scan(&snapshot.target, &snapshot.materialized, &snapshot.missing, &snapshot.stale)
	if err != nil {
		return snapshot, fmt.Errorf("citable catalog metrics: coverage: %w", err)
	}

	rows, err := c.pool.Query(ctx, `
SELECT job_name,
       status,
       COUNT(*)::float8,
       SUM(attempts)::float8,
       COALESCE(SUM(EXTRACT(EPOCH FROM (finished_at - started_at)))
           FILTER (WHERE status = 'completed'), 0)::float8
FROM citable_unit_catalog_queue
GROUP BY job_name, status
ORDER BY job_name, status`)
	if err != nil {
		return snapshot, fmt.Errorf("citable catalog metrics: queue: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var job, state string

		var count, attempts, duration float64
		if err := rows.Scan(&job, &state, &count, &attempts, &duration); err != nil {
			return snapshot, fmt.Errorf("citable catalog metrics: scan: %w", err)
		}

		snapshot.queue[job+"\x00"+state] = count
		snapshot.attempts[job] += attempts
		snapshot.duration[job] += duration
	}

	if err := rows.Err(); err != nil {
		return snapshot, fmt.Errorf("citable catalog metrics: rows: %w", err)
	}

	return snapshot, nil
}

func splitMetricKey(key string) (left, right string, ok bool) {
	for i := range key {
		if key[i] == 0 {
			return key[:i], key[i+1:], true
		}
	}

	return "", "", false
}

const (
	dbSizeCacheTTL      = 60 * time.Second
	dbSizeQueryTimeout  = 5 * time.Second
	dbSizeTopNRelations = 20
)

// dbRelationSize is one cached row for the relation-size collector.
type dbRelationSize struct {
	relation   string
	totalBytes float64
	indexBytes float64
}

// dbSizeCollector exposes per-relation table+index sizes (F1-G) at scrape
// time — postgres-exporter has no built-in collector for relation sizes, and
// the app already follows this cached scrape-time pattern (email queue,
// backfill). Top-N by total size keeps label cardinality bounded.
type dbSizeCollector struct {
	pool *pgxpool.Pool

	mu      sync.Mutex
	fetched time.Time
	sizes   []dbRelationSize

	totalDesc *prometheus.Desc
	indexDesc *prometheus.Desc
}

func newDBSizeCollector(pool *pgxpool.Pool) *dbSizeCollector {
	labels := []string{"relation"}

	return &dbSizeCollector{
		pool: pool,
		totalDesc: prometheus.NewDesc("surau_db_relation_total_bytes",
			"Total on-disk size (table + indexes + toast) of the largest relations.", labels, nil),
		indexDesc: prometheus.NewDesc("surau_db_relation_index_bytes",
			"Index size of the largest relations.", labels, nil),
	}
}

func (c *dbSizeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.totalDesc

	ch <- c.indexDesc
}

func (c *dbSizeCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Since(c.fetched) > dbSizeCacheTTL {
		if sizes, err := c.fetch(); err == nil {
			c.sizes = sizes
			c.fetched = time.Now()
		}
		// On error keep the previous snapshot (stale beats scrape failure).
	}

	for _, s := range c.sizes {
		ch <- prometheus.MustNewConstMetric(c.totalDesc, prometheus.GaugeValue, s.totalBytes, s.relation)

		ch <- prometheus.MustNewConstMetric(c.indexDesc, prometheus.GaugeValue, s.indexBytes, s.relation)
	}
}

func (c *dbSizeCollector) fetch() ([]dbRelationSize, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dbSizeQueryTimeout)
	defer cancel()

	rows, err := c.pool.Query(ctx, `
SELECT relname,
       pg_total_relation_size(relid)::float8,
       pg_indexes_size(relid)::float8
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(relid) DESC
LIMIT $1`, dbSizeTopNRelations)
	if err != nil {
		return nil, fmt.Errorf("db size metrics: query: %w", err)
	}
	defer rows.Close()

	sizes := make([]dbRelationSize, 0, dbSizeTopNRelations)

	for rows.Next() {
		var s dbRelationSize
		if err := rows.Scan(&s.relation, &s.totalBytes, &s.indexBytes); err != nil {
			return nil, fmt.Errorf("db size metrics: scan: %w", err)
		}

		sizes = append(sizes, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db size metrics: rows: %w", err)
	}

	return sizes, nil
}

func registerDBSizeMetrics(pool *pgxpool.Pool) {
	prometheus.MustRegister(newDBSizeCollector(pool))
}
