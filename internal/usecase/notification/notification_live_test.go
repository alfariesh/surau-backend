package notification

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errSimulatedNotificationCrash = errors.New("simulated crash before delivery commit")

// TestLiveReminderProviderIdempotencySurvivesFullRestart closes the first database pool after the
// fake provider accepted but before delivery state committed. A fresh pool/usecase retries the
// original UUID; the fake provider observes two HTTP operations but creates one notification.
//
//nolint:paralleltest // serial live-DB process-restart invariant
func TestLiveReminderProviderIdempotencySurvivesFullRestart(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	ctx := context.Background()
	firstPG, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(firstPG.Close)

	userID := seedNotificationRestartUser(t, firstPG)
	acceptedAttemptsBefore := notificationMetricTotal(
		t,
		firstPG,
		"delivery_attempt",
		entity.PushDeliveryAccepted,
		"accepted",
	)
	acceptedDeliveriesBefore := notificationMetricTotal(
		t,
		firstPG,
		"delivery",
		entity.NotificationStatusAccepted,
		"",
	)
	failedAttemptsBefore := notificationMetricTotal(
		t,
		firstPG,
		"delivery_attempt",
		entity.PushDeliveryFailed,
		"provider_unavailable",
	)
	t.Cleanup(func() {
		cleanupNotificationRestartFixture(
			t,
			databaseURL,
			userID,
			acceptedAttemptsBefore,
			acceptedDeliveriesBefore,
			failedAttemptsBefore,
		)
	})

	provider := newIdempotentFakeProvider()
	firstNow := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	firstUC := New(
		&accountReaderStub{},
		&crashingDeliveryRepo{PersonalRepo: persistent.NewPersonalRepo(firstPG)},
		provider,
		Options{
			QuietStart:    "21:00",
			QuietEnd:      "07:00",
			LeaseDuration: 30 * time.Second,
			Now:           func() time.Time { return firstNow },
		},
		testLogger{},
	)

	_, err = firstUC.DispatchReminders(ctx)
	require.ErrorIs(t, err, errSimulatedNotificationCrash)
	firstPG.Close()

	secondPG, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(secondPG.Close)

	secondNow := firstNow.Add(time.Minute)
	secondUC := New(
		&accountReaderStub{},
		persistent.NewPersonalRepo(secondPG),
		provider,
		Options{
			QuietStart:    "21:00",
			QuietEnd:      "07:00",
			LeaseDuration: 30 * time.Second,
			Now:           func() time.Time { return secondNow },
		},
		testLogger{},
	)

	report, err := secondUC.DispatchReminders(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Accepted)
	assert.Equal(t, 1, report.RecoveredReminders)
	assert.Equal(t, 2, provider.requestCount())
	assert.Equal(t, 1, provider.notificationCount(),
		"same OneSignal idempotency UUID must represent one provider notification")

	var deliveryCount int

	var status, idempotencyKey, providerID string

	err = secondPG.Pool.QueryRow(ctx, `
SELECT count(*) OVER (), status, idempotency_key::text, provider_notification_id
FROM notification_deliveries
WHERE user_id = $1 AND notification_type = 'streak_reminder'`, userID).Scan(
		&deliveryCount,
		&status,
		&idempotencyKey,
		&providerID,
	)
	require.NoError(t, err)
	assert.Equal(t, 1, deliveryCount)
	assert.Equal(t, entity.NotificationStatusAccepted, status)
	assert.NotEmpty(t, idempotencyKey)
	assert.NotEmpty(t, providerID)

	_, err = secondUC.DispatchReminders(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, provider.requestCount(), "accepted daily row must suppress later restart sweeps")
	secondPG.Close()
}

// TestLiveRetryBackoffSurvivesFullRestart proves a future next_attempt_at is authoritative after a
// process restart: the pre-due sweep does not call the provider and tells F1-C exactly when to wake.
//
//nolint:paralleltest // serial live-DB restart and durable-clock invariant
func TestLiveRetryBackoffSurvivesFullRestart(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	ctx := context.Background()
	firstPG, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(firstPG.Close)

	userID := seedNotificationRestartUser(t, firstPG)
	acceptedAttemptsBefore := notificationMetricTotal(
		t, firstPG, "delivery_attempt", entity.PushDeliveryAccepted, "accepted",
	)
	acceptedDeliveriesBefore := notificationMetricTotal(
		t, firstPG, "delivery", entity.NotificationStatusAccepted, "",
	)
	failedAttemptsBefore := notificationMetricTotal(
		t, firstPG, "delivery_attempt", entity.PushDeliveryFailed, "provider_unavailable",
	)
	t.Cleanup(func() {
		cleanupNotificationRestartFixture(
			t,
			databaseURL,
			userID,
			acceptedAttemptsBefore,
			acceptedDeliveriesBefore,
			failedAttemptsBefore,
		)
	})

	provider := &retryThenAcceptProvider{}
	firstNow := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	firstUC := New(
		&accountReaderStub{},
		persistent.NewPersonalRepo(firstPG),
		provider,
		Options{QuietStart: "21:00", QuietEnd: "07:00", Now: func() time.Time { return firstNow }},
		testLogger{},
	)

	_, err = firstUC.DispatchReminders(ctx)
	require.Error(t, err)

	var firstHint interface{ RetryAfter() time.Duration }
	require.ErrorAs(t, err, &firstHint)
	assert.Equal(t, 2*time.Minute, firstHint.RetryAfter())
	assert.Equal(t, 1, provider.requestCount())
	firstPG.Close()

	secondPG, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(secondPG.Close)

	clock := firstNow.Add(time.Minute)
	secondUC := New(
		&accountReaderStub{},
		persistent.NewPersonalRepo(secondPG),
		provider,
		Options{QuietStart: "21:00", QuietEnd: "07:00", Now: func() time.Time { return clock }},
		testLogger{},
	)

	_, err = secondUC.DispatchReminders(ctx)
	require.Error(t, err)

	var restartHint interface{ RetryAfter() time.Duration }
	require.ErrorAs(t, err, &restartHint)
	assert.Equal(t, time.Minute, restartHint.RetryAfter())
	assert.Equal(t, 1, provider.requestCount(), "a restart before next_attempt_at must not call OneSignal")

	clock = firstNow.Add(2 * time.Minute)
	report, err := secondUC.DispatchReminders(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Accepted)
	assert.Equal(t, 2, provider.requestCount())
	assert.Equal(t, provider.requestKeys()[0], provider.requestKeys()[1],
		"retry after restart must retain the original provider idempotency UUID")
	secondPG.Close()
}

type crashingDeliveryRepo struct {
	*persistent.PersonalRepo
}

func (*crashingDeliveryRepo) RecordNotificationDeliveryAttempt(
	context.Context,
	*entity.NotificationDeliveryAttempt,
) error {
	return errSimulatedNotificationCrash
}

type idempotentFakeProvider struct {
	mu            sync.Mutex
	requests      int
	notifications map[string]string
}

type retryThenAcceptProvider struct {
	mu   sync.Mutex
	keys []string
}

func (p *retryThenAcceptProvider) Send(
	_ context.Context,
	_ entity.PushNotification,
	idempotencyKey string,
) (entity.PushDeliveryResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.keys = append(p.keys, idempotencyKey)
	if len(p.keys) == 1 {
		return entity.PushDeliveryResult{
			Outcome:      entity.PushDeliveryFailed,
			ReasonCode:   "provider_unavailable",
			ReasonDetail: "temporary fake outage",
			Retryable:    true,
			Systemic:     true,
			RetryAfter:   2 * time.Minute,
		}, errTestProvider
	}

	return entity.PushDeliveryResult{
		Outcome:                entity.PushDeliveryAccepted,
		ProviderNotificationID: "fake-provider-recovered",
		HTTPStatus:             200,
	}, nil
}

func (p *retryThenAcceptProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.keys)
}

func (p *retryThenAcceptProvider) requestKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.keys...)
}

func newIdempotentFakeProvider() *idempotentFakeProvider {
	return &idempotentFakeProvider{notifications: make(map[string]string)}
}

func (p *idempotentFakeProvider) Send(
	_ context.Context,
	_ entity.PushNotification,
	idempotencyKey string,
) (entity.PushDeliveryResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.requests++

	providerID, ok := p.notifications[idempotencyKey]
	if !ok {
		providerID = fmt.Sprintf("fake-provider-%d", len(p.notifications)+1)
		p.notifications[idempotencyKey] = providerID
	}

	return entity.PushDeliveryResult{
		Outcome:                entity.PushDeliveryAccepted,
		ProviderNotificationID: providerID,
		HTTPStatus:             200,
	}, nil
}

func (p *idempotentFakeProvider) requestCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.requests
}

func (p *idempotentFakeProvider) notificationCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.notifications)
}

func seedNotificationRestartUser(t *testing.T, pg *postgres.Postgres) string {
	t.Helper()

	ctx := context.Background()
	userID := uuid.NewString()
	suffix := strings.ReplaceAll(userID, "-", "")
	_, err := pg.Pool.Exec(ctx, `
INSERT INTO users (id, username, email, password_hash)
VALUES ($1, $2, $3, 'q6-restart-test')`,
		userID, "q6-restart-"+suffix, "q6-restart-"+suffix+"@example.test")
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO user_profiles (user_id, timezone) VALUES ($1, 'Asia/Jakarta')`, userID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO user_preferences (user_id, preferred_ui_lang, notify_streak_reminders)
VALUES ($1, 'id', TRUE)`, userID)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO reading_activity (user_id, activity_date, quran_ayahs_read, quran_events)
VALUES ($1, '2026-07-15', 1, 1)`, userID)
	require.NoError(t, err)

	return userID
}

func notificationMetricTotal(
	t *testing.T,
	pg *postgres.Postgres,
	kind,
	result,
	reason string,
) int64 {
	t.Helper()

	var total int64

	err := pg.Pool.QueryRow(context.Background(), `
SELECT total
FROM notification_delivery_metric_totals
WHERE metric_kind = $1
  AND notification_type = 'streak_reminder'
  AND result = $2
  AND reason_code = $3`, kind, result, reason).Scan(&total)
	require.NoError(t, err)

	return total
}

func cleanupNotificationRestartFixture(
	t *testing.T,
	databaseURL,
	userID string,
	attemptsBefore,
	deliveriesBefore,
	failedAttemptsBefore int64,
) {
	t.Helper()

	pg, err := postgres.New(databaseURL)
	if err != nil {
		t.Errorf("open Q-6 cleanup pool: %v", err)

		return
	}
	defer pg.Close()

	ctx := context.Background()
	_, err = pg.Pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	assert.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
DELETE FROM auth_notification_cooldowns
WHERE event = 'streak_reminder'
  AND key_hash IN (
      encode(sha256(
          convert_to('streak_reminder', 'UTF8') || '\x00'::bytea ||
          convert_to($1::text, 'UTF8') || '\x00'::bytea
      ), 'hex'),
      encode(sha256(
          convert_to('streak_reminder', 'UTF8') || '\x00'::bytea ||
          convert_to($1::text, 'UTF8') || '\x00'::bytea ||
          convert_to('2026-07-16', 'UTF8') || '\x00'::bytea
      ), 'hex')
  )`, userID)
	assert.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
UPDATE notification_delivery_metric_totals
SET total = $1
WHERE metric_kind = 'delivery_attempt'
  AND notification_type = 'streak_reminder'
  AND result = 'accepted'
  AND reason_code = 'accepted'`, attemptsBefore)
	assert.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
UPDATE notification_delivery_metric_totals
SET total = $1
WHERE metric_kind = 'delivery'
  AND notification_type = 'streak_reminder'
  AND result = 'accepted'
  AND reason_code = ''`, deliveriesBefore)
	assert.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
UPDATE notification_delivery_metric_totals
SET total = $1
WHERE metric_kind = 'delivery_attempt'
  AND notification_type = 'streak_reminder'
  AND result = 'failed'
  AND reason_code = 'provider_unavailable'`, failedAttemptsBefore)
	assert.NoError(t, err)
}
