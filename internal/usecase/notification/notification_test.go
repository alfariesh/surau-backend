package notification

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errTestProvider = errors.New("provider unavailable")
	errTestPersist  = errors.New("database unavailable")
)

func TestDispatchRemindersAcceptedAndDailyDedupeSurvivesUseCaseRestart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	candidate := testReminderCandidate(now)
	claimed := false
	providerCalls := 0

	var stableCooldown, legacyCooldown string

	repo := &deliveryRepoStub{
		candidates: entity.ReminderCandidatesResult{Candidates: []entity.ReminderCandidate{candidate}},
		claimFn: func(claim *entity.ReminderDeliveryClaim, _ time.Time) (entity.NotificationDelivery, bool, string, error) {
			stableCooldown = claim.CooldownKeyHash
			legacyCooldown = claim.LegacyCooldownKeyHash

			if claimed {
				return entity.NotificationDelivery{}, false, "daily_duplicate", nil
			}

			claimed = true

			return deliveryFromCreate(&claim.Delivery), true, "", nil
		},
	}
	pusher := pusherStub{sendFn: func(
		context.Context,
		entity.PushNotification,
		string,
	) (entity.PushDeliveryResult, error) {
		providerCalls++

		return acceptedProviderResult(), nil
	}}

	first := newTestUseCase(repo, pusher, now)
	firstReport, err := first.DispatchReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, firstReport.Accepted)

	// Reconstruct the usecase with no shared in-memory state. The fake repo deliberately keeps the
	// durable daily row, mirroring a new process connected to the same database.
	second := newTestUseCase(repo, pusher, now.Add(time.Hour))
	secondReport, err := second.DispatchReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), secondReport.Skipped["daily_duplicate"])
	assert.Equal(t, 1, providerCalls)
	assert.NotEmpty(t, stableCooldown)
	assert.NotEqual(t, stableCooldown, legacyCooldown, "new time-only and rolling-deploy legacy keys must both exist")
}

func TestDispatchRemindersRetryUsesSameProviderIdempotencyKey(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	candidate := testReminderCandidate(now)

	var stored entity.NotificationDelivery

	var retryNext time.Time

	repo := &deliveryRepoStub{
		candidates: entity.ReminderCandidatesResult{Candidates: []entity.ReminderCandidate{candidate}},
		claimFn: func(claim *entity.ReminderDeliveryClaim, _ time.Time) (entity.NotificationDelivery, bool, string, error) {
			if stored.ID == "" {
				stored = deliveryFromCreate(&claim.Delivery)
			} else {
				stored.LeaseToken = claim.Delivery.LeaseToken
				stored.LeaseExpiresAt = claim.Delivery.LeaseExpiresAt
			}

			return stored, true, "", nil
		},
		recordFn: func(attempt *entity.NotificationDeliveryAttempt) error {
			stored.AttemptCount++
			stored.Status = entity.NotificationStatusRetrying
			retryNext = attempt.NextAttemptAt

			if attempt.Outcome == entity.PushDeliveryAccepted {
				stored.Status = entity.NotificationStatusAccepted
			}

			return nil
		},
	}

	var keys []string

	pusher := pusherStub{sendFn: func(
		_ context.Context,
		_ entity.PushNotification,
		key string,
	) (entity.PushDeliveryResult, error) {
		keys = append(keys, key)
		if len(keys) == 1 {
			return entity.PushDeliveryResult{
				Outcome:      entity.PushDeliveryFailed,
				ReasonCode:   "provider_unavailable",
				ReasonDetail: "temporary",
				Retryable:    true,
				Systemic:     true,
				RetryAfter:   2 * time.Minute,
			}, errTestProvider
		}

		return acceptedProviderResult(), nil
	}}

	first := newTestUseCase(repo, pusher, now)
	_, err := first.DispatchReminders(context.Background())
	require.Error(t, err)

	repo.candidates = entity.ReminderCandidatesResult{}
	repo.pendingRemindersFn = func(
		_ time.Time,
		_, _ string,
		leaseToken string,
		leaseExpiresAt time.Time,
		_ int,
	) ([]entity.NotificationDelivery, error) {
		stored.LeaseToken = leaseToken
		stored.LeaseExpiresAt = leaseExpiresAt

		if stored.Status == entity.NotificationStatusAccepted {
			return nil, nil
		}

		return []entity.NotificationDelivery{stored}, nil
	}

	assert.Equal(t, now.Add(2*time.Minute), retryNext, "Retry-After must survive in the durable next-attempt time")

	second := newTestUseCase(repo, pusher, now.Add(2*time.Minute))
	report, err := second.DispatchReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Accepted)
	require.Len(t, keys, 2)
	assert.Equal(t, keys[0], keys[1], "restart retry must reuse the persisted OneSignal idempotency key")
}

func TestDispatchRemindersUnauthorizedStopsBatchImmediately(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	candidates := []entity.ReminderCandidate{testReminderCandidate(now), testReminderCandidate(now)}
	candidates[1].UserID = "user-two"

	repo := &deliveryRepoStub{candidates: entity.ReminderCandidatesResult{Candidates: candidates}}
	calls := 0
	pusher := pusherStub{sendFn: func(
		context.Context,
		entity.PushNotification,
		string,
	) (entity.PushDeliveryResult, error) {
		calls++

		return entity.PushDeliveryResult{
			Outcome:      entity.PushDeliveryFailed,
			HTTPStatus:   401,
			ReasonCode:   "unauthorized",
			ReasonDetail: "provider rejected credentials",
			Retryable:    true,
			Systemic:     true,
		}, nil
	}}

	report, err := newTestUseCase(repo, pusher, now).DispatchReminders(context.Background())
	require.Error(t, err)
	assert.Zero(t, report.Failed, "the logical delivery remains retryable after credentials are fixed")
	assert.Equal(t, 1, calls, "auth failures must stop the batch before another user is attempted")
}

func TestDispatchRemindersRetryCrossingQuietBoundarySkipsProvider(t *testing.T) {
	t.Parallel()

	beforeQuietHours := time.Date(2026, time.July, 15, 13, 59, 59, 0, time.UTC)
	quietHoursStart := time.Date(2026, time.July, 15, 14, 0, 0, 0, time.UTC)
	clock := beforeQuietHours
	returned := false
	providerCalls := 0
	failCalls := 0

	repo := &deliveryRepoStub{
		pendingRemindersFn: func(
			_ time.Time,
			_, _ string,
			leaseToken string,
			leaseExpiresAt time.Time,
			_ int,
		) ([]entity.NotificationDelivery, error) {
			if returned {
				return nil, nil
			}

			returned = true
			clock = quietHoursStart

			return []entity.NotificationDelivery{{
				ID:                 "delivery-one",
				UserID:             "user-one",
				NotificationType:   entity.NotificationTypeStreakReminder,
				IdempotencyKey:     "f53db014-1d01-4c02-8f63-458e6f32b012",
				LeaseToken:         leaseToken,
				LeaseExpiresAt:     leaseExpiresAt,
				DeliveryDeadlineAt: quietHoursStart,
			}}, nil
		},
		failFn: func(_, _, reasonCode string, asOf time.Time) error {
			failCalls++

			assert.Equal(t, "delivery_window_expired", reasonCode)
			assert.Equal(t, quietHoursStart, asOf)

			return nil
		},
	}
	pusher := pusherStub{sendFn: func(
		context.Context,
		entity.PushNotification,
		string,
	) (entity.PushDeliveryResult, error) {
		providerCalls++

		return acceptedProviderResult(), nil
	}}
	uc := New(
		&accountReaderStub{},
		repo,
		pusher,
		Options{QuietStart: "21:00", QuietEnd: "07:00", Now: func() time.Time { return clock }},
		testLogger{},
	)

	report, err := uc.DispatchReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Failed)
	assert.Equal(t, 1, report.RecoveredReminders)
	assert.Equal(t, 1, failCalls)
	assert.Zero(t, providerCalls, "the provider must not be called at or after 21:00 local")
}

func TestDispatchRemindersReturnsDurableFutureRetryHint(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	repo := &deliveryRepoStub{nextRetryAt: now.Add(2 * time.Minute)}

	_, err := newTestUseCase(repo, pusherStub{}, now).DispatchReminders(context.Background())
	require.Error(t, err)

	var hinted interface{ RetryAfter() time.Duration }
	require.ErrorAs(t, err, &hinted)
	assert.Equal(t, 2*time.Minute, hinted.RetryAfter(),
		"a restarted supervisor must wake when the durable row becomes due")
}

func TestNotificationRetryDelayGrowsCapsAndHonorsProviderHint(t *testing.T) {
	t.Parallel()

	assertRetryDelayRange(t, notificationRetryDelay(1, 0), 24*time.Second, 36*time.Second)
	assertRetryDelayRange(t, notificationRetryDelay(2, 0), 48*time.Second, 72*time.Second)
	assertRetryDelayRange(t, notificationRetryDelay(20, 0), 12*time.Minute, 15*time.Minute)
	assert.Equal(t, 20*time.Minute, notificationRetryDelay(1, 20*time.Minute))
}

func TestDispatchStateReturnsLargestRetryHint(t *testing.T) {
	t.Parallel()

	state := dispatchState{report: &DispatchReport{}}
	require.NoError(t, state.observe(entity.NotificationStatusRetrying, &providerDeliveryError{
		reasonCode: "provider_unavailable",
		retryAfter: 30 * time.Second,
	}))
	require.NoError(t, state.observe(entity.NotificationStatusRetrying, &providerDeliveryError{
		reasonCode: "rate_limited",
		retryAfter: 2 * time.Minute,
	}))

	var hinted interface{ RetryAfter() time.Duration }
	require.ErrorAs(t, state.err(), &hinted)
	assert.Equal(t, 2*time.Minute, hinted.RetryAfter())
}

func TestDispatchRemindersDrainsRecoveryBeyondFormerBatchOfTen(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)

	pending := make([]entity.NotificationDelivery, 12)
	for index := range pending {
		pending[index] = entity.NotificationDelivery{
			ID:               fmt.Sprintf("event-%d", index),
			NotificationType: entity.NotificationTypeNewLogin,
			IdempotencyKey:   fmt.Sprintf("key-%d", index),
		}
	}

	repo := &deliveryRepoStub{pendingEventsFn: func(
		_ time.Time,
		leaseToken string,
		leaseExpiresAt time.Time,
		limit int,
	) ([]entity.NotificationDelivery, error) {
		assert.Equal(t, 1, limit, "one-at-a-time claims avoid leasing rows left behind by systemic stop")

		if len(pending) == 0 {
			return nil, nil
		}

		delivery := pending[0]
		pending = pending[1:]
		delivery.LeaseToken = leaseToken
		delivery.LeaseExpiresAt = leaseExpiresAt

		return []entity.NotificationDelivery{delivery}, nil
	}}
	providerCalls := 0
	pusher := pusherStub{sendFn: func(
		context.Context,
		entity.PushNotification,
		string,
	) (entity.PushDeliveryResult, error) {
		providerCalls++

		return acceptedProviderResult(), nil
	}}

	report, err := newTestUseCase(repo, pusher, now).DispatchReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 12, report.RecoveredEvents)
	assert.Equal(t, 12, report.Accepted)
	assert.Equal(t, 12, providerCalls)
}

func assertRetryDelayRange(t *testing.T, actual, minimum, maximum time.Duration) {
	t.Helper()

	assert.GreaterOrEqual(t, actual, minimum)
	assert.LessOrEqual(t, actual, maximum)
}

func TestDispatchRemindersPermanentFailureDoesNotStopOtherUsers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	candidates := []entity.ReminderCandidate{testReminderCandidate(now), testReminderCandidate(now)}
	candidates[1].UserID = "user-two"

	repo := &deliveryRepoStub{
		candidates: entity.ReminderCandidatesResult{Candidates: candidates},
		claimFn: func(claim *entity.ReminderDeliveryClaim, _ time.Time) (entity.NotificationDelivery, bool, string, error) {
			return deliveryFromCreate(&claim.Delivery), true, "", nil
		},
	}
	calls := 0
	pusher := pusherStub{sendFn: func(
		context.Context,
		entity.PushNotification,
		string,
	) (entity.PushDeliveryResult, error) {
		calls++
		if calls == 1 {
			return entity.PushDeliveryResult{
				Outcome:      entity.PushDeliveryFailed,
				HTTPStatus:   200,
				ReasonCode:   "no_subscribers",
				ReasonDetail: "no active subscriptions",
			}, nil
		}

		return acceptedProviderResult(), nil
	}}

	report, err := newTestUseCase(repo, pusher, now).DispatchReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Accepted)
	assert.Equal(t, 1, report.Failed)
	assert.Equal(t, 2, calls)
}

func TestDispatchRemindersPersistsTimezoneSkipCounts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	repo := &deliveryRepoStub{candidates: entity.ReminderCandidatesResult{
		MissingTimezoneSkipped: 2,
		InvalidTimezoneSkipped: 3,
	}}

	report, err := newTestUseCase(repo, pusherStub{}, now).DispatchReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), report.Skipped["missing_timezone"])
	assert.Equal(t, int64(3), report.Skipped["invalid_timezone"])
	assert.Equal(t, report.Skipped, repo.recordedSkips())
}

func TestEventNotificationsPersistEveryTypeBeforeProviderSend(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	created := make(chan entity.NotificationDeliveryCreate, 3)
	attempted := make(chan entity.NotificationDeliveryAttempt, 3)
	repo := &deliveryRepoStub{
		createFn: func(create *entity.NotificationDeliveryCreate, _ time.Time) (entity.NotificationDelivery, error) {
			created <- *create

			return deliveryFromCreate(create), nil
		},
		recordFn: func(attempt *entity.NotificationDeliveryAttempt) error {
			attempted <- *attempt

			return nil
		},
	}
	accounts := accountReaderStub{account: entity.UserAccount{Preferences: entity.UserPreferences{
		PreferredUILang:        "id",
		NotifyKhatamMilestones: true,
	}}}
	uc := New(&accounts, repo, pusherStub{sendFn: func(
		context.Context,
		entity.PushNotification,
		string,
	) (entity.PushDeliveryResult, error) {
		return acceptedProviderResult(), nil
	}}, Options{QuietStart: "21:00", QuietEnd: "07:00", Now: func() time.Time { return now }}, testLogger{})

	uc.NotifyKhatamCompleted(context.Background(), "user-one")
	uc.NotifyKhatamMilestone(context.Background(), "user-one", 10)
	uc.NotifyNewLogin(context.Background(), "user-one", "Safari", "127.0.0.1")

	want := map[string]bool{
		entity.NotificationTypeKhatamCompleted: false,
		entity.NotificationTypeKhatamMilestone: false,
		entity.NotificationTypeNewLogin:        false,
	}

	for range 3 {
		select {
		case create := <-created:
			_, known := want[create.NotificationType]
			require.True(t, known)

			want[create.NotificationType] = true
			assert.Empty(t, create.LocalDate, "event push must not use reminder daily dedupe")
			assert.NotEmpty(t, create.IdempotencyKey)
		case <-time.After(2 * time.Second):
			t.Fatal("event delivery was not persisted")
		}
	}

	for range 3 {
		select {
		case attempt := <-attempted:
			assert.Equal(t, entity.PushDeliveryAccepted, attempt.Outcome)
		case <-time.After(2 * time.Second):
			t.Fatal("event provider result was not recorded")
		}
	}

	for notificationType, seen := range want {
		assert.Truef(t, seen, "%s was not routed through the delivery ledger", notificationType)
	}
}

func TestEventFailureWakesSupervisorAndRetriesAtDurableDueTime(t *testing.T) {
	t.Parallel()

	clock := time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC)
	created := make(chan entity.NotificationDeliveryCreate, 1)
	attempted := make(chan entity.NotificationDeliveryAttempt, 2)
	providerKeys := make(chan string, 2)

	var providerCalls atomic.Int64

	var retryDelivery entity.NotificationDelivery

	claimedRetry := false
	retryAt := time.Time{}

	repo := &deliveryRepoStub{
		createFn: func(create *entity.NotificationDeliveryCreate, _ time.Time) (entity.NotificationDelivery, error) {
			created <- *create

			return deliveryFromCreate(create), nil
		},
		recordFn: func(attempt *entity.NotificationDeliveryAttempt) error {
			attempted <- *attempt

			return nil
		},
		pendingEventsFn: func(
			asOf time.Time,
			leaseToken string,
			leaseExpiresAt time.Time,
			_ int,
		) ([]entity.NotificationDelivery, error) {
			if claimedRetry || retryDelivery.ID == "" || asOf.Before(retryAt) {
				return nil, nil
			}

			claimedRetry = true
			retryDelivery.LeaseToken = leaseToken
			retryDelivery.LeaseExpiresAt = leaseExpiresAt

			return []entity.NotificationDelivery{retryDelivery}, nil
		},
	}
	pusher := pusherStub{sendFn: func(
		_ context.Context,
		_ entity.PushNotification,
		idempotencyKey string,
	) (entity.PushDeliveryResult, error) {
		providerKeys <- idempotencyKey

		if providerCalls.Add(1) == 1 {
			return entity.PushDeliveryResult{
				Outcome:      entity.PushDeliveryFailed,
				ReasonCode:   "provider_unavailable",
				ReasonDetail: "temporary outage",
				Retryable:    true,
				Systemic:     true,
			}, errTestProvider
		}

		return acceptedProviderResult(), nil
	}}
	uc := New(
		&accountReaderStub{},
		repo,
		pusher,
		Options{QuietStart: "21:00", QuietEnd: "07:00", Now: func() time.Time { return clock }},
		testLogger{},
	)

	// The F1-C loop may currently be in its ordinary one-hour polling sleep when this event fails.
	uc.NotifyNewLogin(context.Background(), "user-one", "Safari", "127.0.0.1")

	var create entity.NotificationDeliveryCreate
	select {
	case create = <-created:
	case <-time.After(2 * time.Second):
		t.Fatal("event delivery was not persisted before the provider call")
	}

	var firstAttempt entity.NotificationDeliveryAttempt
	select {
	case firstAttempt = <-attempted:
	case <-time.After(2 * time.Second):
		t.Fatal("retryable event attempt was not recorded")
	}

	retryAt = firstAttempt.NextAttemptAt
	repo.nextRetryAt = retryAt
	retryDelivery = deliveryFromCreate(&create)
	retryDelivery.Status = entity.NotificationStatusRetrying
	retryDelivery.AttemptCount = 1

	select {
	case <-uc.RetryWakeups():
	case <-time.After(2 * time.Second):
		t.Fatal("retryable event did not wake the F1-C supervisor")
	}

	_, err := uc.DispatchReminders(context.Background())
	require.Error(t, err)

	var hinted interface{ RetryAfter() time.Duration }
	require.ErrorAs(t, err, &hinted)
	assertRetryDelayRange(t, hinted.RetryAfter(), 24*time.Second, 36*time.Second)
	assert.Equal(t, int64(1), providerCalls.Load(), "wake must schedule, not send before the durable due time")

	clock = firstAttempt.NextAttemptAt
	repo.nextRetryAt = time.Time{}

	report, err := uc.DispatchReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Accepted)
	assert.Equal(t, 1, report.RecoveredEvents)
	assert.Equal(t, int64(2), providerCalls.Load())

	firstKey := <-providerKeys
	secondKey := <-providerKeys
	assert.Equal(t, firstKey, secondKey, "event retry must reuse the durable OneSignal idempotency UUID")

	select {
	case secondAttempt := <-attempted:
		assert.Equal(t, entity.PushDeliveryAccepted, secondAttempt.Outcome)
	case <-time.After(2 * time.Second):
		t.Fatal("accepted event retry was not recorded")
	}
}

func TestEventPersistErrorWakesAndFailsNextSupervisorPass(t *testing.T) {
	t.Parallel()

	repo := &deliveryRepoStub{createFn: func(
		*entity.NotificationDeliveryCreate,
		time.Time,
	) (entity.NotificationDelivery, error) {
		return entity.NotificationDelivery{}, errTestPersist
	}}
	uc := newTestUseCase(repo, pusherStub{}, time.Date(2026, time.July, 15, 13, 0, 0, 0, time.UTC))

	uc.NotifyNewLogin(context.Background(), "user-one", "Safari", "127.0.0.1")

	select {
	case <-uc.RetryWakeups():
	case <-time.After(2 * time.Second):
		t.Fatal("event persistence error did not wake the F1-C supervisor")
	}

	_, err := uc.DispatchReminders(context.Background())
	require.ErrorIs(t, err, errTestPersist,
		"the awakened pass must fail so last-success is not advanced during a persistence outage")
}

func testReminderCandidate(now time.Time) entity.ReminderCandidate {
	return entity.ReminderCandidate{
		UserID:             "user-one",
		Lang:               "id",
		Timezone:           "Asia/Jakarta",
		LocalDate:          "2026-07-15",
		DeliveryDeadlineAt: now.Add(2 * time.Hour),
	}
}

func acceptedProviderResult() entity.PushDeliveryResult {
	return entity.PushDeliveryResult{
		Outcome:                entity.PushDeliveryAccepted,
		ProviderNotificationID: "provider-id",
		HTTPStatus:             200,
	}
}

func newTestUseCase(repo DeliveryRepo, pusher Pusher, now time.Time) *UseCase {
	return New(
		&accountReaderStub{},
		repo,
		pusher,
		Options{QuietStart: "21:00", QuietEnd: "07:00", Now: func() time.Time { return now }},
		testLogger{},
	)
}

type accountReaderStub struct {
	account entity.UserAccount
	err     error
}

func (s *accountReaderStub) GetAccount(context.Context, string) (entity.UserAccount, error) {
	return s.account, s.err
}

type pusherStub struct {
	sendFn func(context.Context, entity.PushNotification, string) (entity.PushDeliveryResult, error)
}

func (s pusherStub) Send(
	ctx context.Context,
	message entity.PushNotification,
	idempotencyKey string,
) (entity.PushDeliveryResult, error) {
	if s.sendFn == nil {
		return acceptedProviderResult(), nil
	}

	return s.sendFn(ctx, message, idempotencyKey)
}

type deliveryRepoStub struct {
	mu sync.Mutex

	candidates         entity.ReminderCandidatesResult
	claimFn            func(*entity.ReminderDeliveryClaim, time.Time) (entity.NotificationDelivery, bool, string, error)
	createFn           func(*entity.NotificationDeliveryCreate, time.Time) (entity.NotificationDelivery, error)
	recordFn           func(*entity.NotificationDeliveryAttempt) error
	pendingEventsFn    func(time.Time, string, time.Time, int) ([]entity.NotificationDelivery, error)
	pendingRemindersFn func(time.Time, string, string, string, time.Time, int) ([]entity.NotificationDelivery, error)
	failFn             func(string, string, string, time.Time) error
	nextRetryAt        time.Time
	skips              map[string]int64
}

func (s *deliveryRepoStub) ReminderCandidates(
	context.Context,
	time.Time,
	string,
	string,
) (entity.ReminderCandidatesResult, error) {
	return s.candidates, nil
}

func (s *deliveryRepoStub) ClaimReminderDelivery(
	_ context.Context,
	claim *entity.ReminderDeliveryClaim,
	asOf time.Time,
) (delivery entity.NotificationDelivery, claimed bool, reason string, err error) {
	if s.claimFn != nil {
		return s.claimFn(claim, asOf)
	}

	return deliveryFromCreate(&claim.Delivery), true, "", nil
}

func (s *deliveryRepoStub) CreateEventDelivery(
	_ context.Context,
	create *entity.NotificationDeliveryCreate,
	asOf time.Time,
) (entity.NotificationDelivery, error) {
	if s.createFn != nil {
		return s.createFn(create, asOf)
	}

	return deliveryFromCreate(create), nil
}

func (s *deliveryRepoStub) ClaimPendingEventDeliveries(
	_ context.Context,
	asOf time.Time,
	leaseToken string,
	leaseExpiresAt time.Time,
	limit int,
) ([]entity.NotificationDelivery, error) {
	if s.pendingEventsFn != nil {
		return s.pendingEventsFn(asOf, leaseToken, leaseExpiresAt, limit)
	}

	return nil, nil
}

func (s *deliveryRepoStub) ClaimPendingReminderDeliveries(
	_ context.Context,
	asOf time.Time,
	quietStart,
	quietEnd,
	leaseToken string,
	leaseExpiresAt time.Time,
	limit int,
) ([]entity.NotificationDelivery, error) {
	if s.pendingRemindersFn != nil {
		return s.pendingRemindersFn(asOf, quietStart, quietEnd, leaseToken, leaseExpiresAt, limit)
	}

	return nil, nil
}

func (*deliveryRepoStub) ExpireNotificationDeliveries(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func (s *deliveryRepoStub) FailNotificationDelivery(
	_ context.Context,
	deliveryID,
	leaseToken,
	reasonCode string,
	asOf time.Time,
) error {
	if s.failFn != nil {
		return s.failFn(deliveryID, leaseToken, reasonCode, asOf)
	}

	return nil
}

func (s *deliveryRepoStub) NextNotificationRetryAt(context.Context, time.Time) (time.Time, error) {
	return s.nextRetryAt, nil
}

func (s *deliveryRepoStub) RecordNotificationDeliveryAttempt(
	_ context.Context,
	attempt *entity.NotificationDeliveryAttempt,
) error {
	if s.recordFn != nil {
		return s.recordFn(attempt)
	}

	return nil
}

func (s *deliveryRepoStub) RecordReminderSkips(
	_ context.Context,
	skips map[string]int64,
	_ time.Time,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.skips = make(map[string]int64, len(skips))
	maps.Copy(s.skips, skips)

	return nil
}

func (s *deliveryRepoStub) recordedSkips() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	copyOfSkips := make(map[string]int64, len(s.skips))
	maps.Copy(copyOfSkips, s.skips)

	return copyOfSkips
}

func deliveryFromCreate(create *entity.NotificationDeliveryCreate) entity.NotificationDelivery {
	return entity.NotificationDelivery{
		ID:                 create.ID,
		UserID:             create.UserID,
		NotificationType:   create.NotificationType,
		LocalDate:          create.LocalDate,
		Payload:            create.Payload,
		IdempotencyKey:     create.IdempotencyKey,
		Status:             entity.NotificationStatusPending,
		LeaseToken:         create.LeaseToken,
		LeaseExpiresAt:     create.LeaseExpiresAt,
		DeliveryDeadlineAt: create.DeliveryDeadlineAt,
	}
}

type testLogger struct{}

func (testLogger) Debug(any, ...any)                        {}
func (testLogger) Info(string, ...any)                      {}
func (testLogger) Warn(string, ...any)                      {}
func (testLogger) Error(any, ...any)                        {}
func (testLogger) Fatal(any, ...any)                        {}
func (l testLogger) WithField(string, any) logger.Interface { return l }
