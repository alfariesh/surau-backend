package persistent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// editorialTrailWriteTimeout bounds one post-commit audit-log or
// production-event insert.
const editorialTrailWriteTimeout = 5 * time.Second

const (
	trailAdminAudit      = "admin_audit"
	trailProductionEvent = "production_event"
)

// editorialTrailWriteFailures counts post-commit trail inserts that failed.
// The change they describe has already committed, so the failure cannot be
// returned to the caller; it is counted and raised by the Grafana alert
// "editorial audit trail write failed" instead of disappearing silently.
//
//nolint:gochecknoglobals // process-wide Prometheus instrument (promauto pattern)
var editorialTrailWriteFailures = newEditorialTrailWriteFailures(prometheus.DefaultRegisterer)

// newEditorialTrailWriteFailures registers the counter with every trail
// series already exported at zero. A series created by its first failure is
// born at 1, and increase() over samples that all read 1 is 0, so the alert
// would miss the first failure after every restart, i.e. after every deploy.
func newEditorialTrailWriteFailures(registerer prometheus.Registerer) *prometheus.CounterVec {
	failures := promauto.With(registerer).NewCounterVec(prometheus.CounterOpts{
		Name: "surau_editorial_trail_write_failures_total",
		Help: "Post-commit editorial audit-log or production-event inserts that failed; the audited change itself committed.",
	}, []string{"trail"})
	for _, trail := range []string{trailAdminAudit, trailProductionEvent} {
		failures.WithLabelValues(trail)
	}

	return failures
}

// trailContext detaches a post-commit trail write from the request: a client
// that disconnects right after a committed change must not erase its record.
func trailContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), editorialTrailWriteTimeout)
}

// audit appends an admin_audit_logs row for a change that has already
// committed. It never fails the caller; see editorialTrailWriteFailures.
func (r *EditorialRepo) audit(
	ctx context.Context,
	actorID string,
	action string,
	bookID int,
	pageID *int,
	headingID *int,
	collectionSlug string,
	payload any,
) {
	payloadJSON, err := trailPayload(payload)
	if err != nil {
		editorialTrailWriteFailures.WithLabelValues(trailAdminAudit).Inc()

		return
	}

	ctx, cancel := trailContext(ctx)
	defer cancel()

	if _, err = r.Pool.Exec(
		ctx, `
INSERT INTO admin_audit_logs (id, actor_id, action, book_id, page_id, heading_id, collection_slug, payload, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, now())`,
		uuid.New().String(),
		actorID,
		action,
		positiveIntNil(bookID),
		pageID,
		headingID,
		emptyStringNil(collectionSlug),
		payloadJSON,
	); err != nil {
		editorialTrailWriteFailures.WithLabelValues(trailAdminAudit).Inc()
	}
}

// recordProductionEvent appends a book_production_events timeline row for a
// change that has already committed. It never fails the caller; see
// editorialTrailWriteFailures.
func (r *EditorialRepo) recordProductionEvent(
	ctx context.Context,
	actorID,
	projectID,
	eventType string,
	assetType *string,
	headingID *int,
	note *string,
	payload any,
) {
	payloadJSON, err := trailPayload(payload)
	if err != nil {
		editorialTrailWriteFailures.WithLabelValues(trailProductionEvent).Inc()

		return
	}

	ctx, cancel := trailContext(ctx)
	defer cancel()

	if _, err = r.Pool.Exec(
		ctx, `
INSERT INTO book_production_events (
    id, project_id, actor_id, event_type, asset_type, heading_id, note, payload, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, now())`,
		uuid.New().String(),
		projectID,
		emptyStringNil(actorID),
		eventType,
		assetType,
		headingID,
		note,
		payloadJSON,
	); err != nil {
		editorialTrailWriteFailures.WithLabelValues(trailProductionEvent).Inc()
	}
}

// trailPayload encodes an optional payload; an empty string becomes SQL NULL
// through nullif in the insert.
func trailPayload(payload any) (string, error) {
	if payload == nil {
		return "", nil
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return string(encoded), nil
}
