package persistent

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	q6QuietStart = "21:00"
	q6QuietEnd   = "07:00"
)

// TestLiveNotificationReminderClaimSurvivesConcurrencyAndRestart proves the daily database key,
// the independent 20-hour cooldown, and lease-based restart recovery against real PostgreSQL.
//
//nolint:paralleltest // serial live-DB invariant over dedicated throwaway rows
func TestLiveNotificationReminderClaimSurvivesConcurrencyAndRestart(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL, postgres.MaxPoolSize(16))
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	repository := NewPersonalRepo(pg)
	userID := seedQ6NotificationUser(t, pg, new("Asia/Jakarta"))
	asOf := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	localDate := "2026-07-16"
	cooldownKey := strings.Repeat("a", 64)
	legacyCooldownKey := strings.Repeat("b", 64)

	t.Cleanup(func() {
		_, cleanupErr := pg.Pool.Exec(
			context.Background(),
			`DELETE FROM auth_notification_cooldowns WHERE event = $1 AND key_hash = ANY($2)`,
			entity.NotificationTypeStreakReminder,
			[]string{cooldownKey, legacyCooldownKey},
		)
		if cleanupErr != nil {
			t.Logf("cleanup Q-6 cooldowns: %v", cleanupErr)
		}
	})

	type claimResult struct {
		delivery entity.NotificationDelivery
		claimed  bool
		reason   string
		err      error
	}

	const workers = 12

	results := make(chan claimResult, workers)

	var waitGroup sync.WaitGroup
	for range workers {
		waitGroup.Go(func() {
			delivery, claimed, reason, claimErr := repository.ClaimReminderDelivery(
				ctx,
				q6ReminderClaim(userID, localDate, cooldownKey, legacyCooldownKey, asOf),
				asOf,
			)
			results <- claimResult{delivery: delivery, claimed: claimed, reason: reason, err: claimErr}
		})
	}

	waitGroup.Wait()
	close(results)

	claimedCount := 0

	var firstDelivery entity.NotificationDelivery

	for result := range results {
		require.NoError(t, result.err)

		if result.claimed {
			claimedCount++
			firstDelivery = result.delivery

			continue
		}

		assert.Contains(t, []string{"cooldown", "leased"}, result.reason)
	}

	require.Equal(t, 1, claimedCount, "exactly one concurrent worker may create the logical reminder")

	var rowCount int
	require.NoError(t, pg.Pool.QueryRow(
		ctx, `
SELECT count(*)
FROM notification_deliveries
WHERE user_id = $1 AND notification_type = $2 AND local_date = $3::date`,
		userID,
		entity.NotificationTypeStreakReminder,
		localDate,
	).Scan(&rowCount))
	assert.Equal(t, 1, rowCount)

	// Simulate a process restart after the provider request but before final persistence. A fresh
	// repository reclaims the expired lease and must retain the original OneSignal idempotency key.
	restartedPG, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(restartedPG.Close)
	restartedRepository := NewPersonalRepo(restartedPG)
	restartAt := asOf.Add(2 * time.Minute)
	reclaimed, claimed, reason, err := restartedRepository.ClaimReminderDelivery(
		ctx,
		q6ReminderClaim(userID, localDate, cooldownKey, legacyCooldownKey, restartAt),
		restartAt,
	)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.Empty(t, reason)
	assert.Equal(t, firstDelivery.ID, reclaimed.ID)
	assert.Equal(t, firstDelivery.IdempotencyKey, reclaimed.IdempotencyKey)
	assert.NotEqual(t, firstDelivery.LeaseToken, reclaimed.LeaseToken)

	// Mark the reclaimed logical delivery accepted without adding metrics; delivery-attempt
	// transaction behavior is covered separately below.
	_, err = restartedPG.Pool.Exec(ctx, `
UPDATE notification_deliveries
SET status = 'accepted',
    provider_notification_id = 'live-restart-accepted',
    accepted_at = $2,
    lease_token = NULL,
    lease_expires_at = NULL,
    updated_at = $2
WHERE id = $1`, reclaimed.ID, restartAt)
	require.NoError(t, err)

	_, err = restartedPG.Pool.Exec(
		ctx, `
UPDATE auth_notification_cooldowns
SET expires_at = $3
WHERE event = $1 AND key_hash = ANY($2)`,
		entity.NotificationTypeStreakReminder,
		[]string{cooldownKey, legacyCooldownKey},
		restartAt.Add(-time.Second),
	)
	require.NoError(t, err)

	_, claimed, reason, err = restartedRepository.ClaimReminderDelivery(
		ctx,
		q6ReminderClaim(userID, localDate, cooldownKey, legacyCooldownKey, restartAt),
		restartAt,
	)
	require.NoError(t, err)
	assert.False(t, claimed, "an expired cooldown must not weaken the permanent daily key")
	assert.Equal(t, "daily_duplicate", reason)

	nextDayAt := asOf.Add(25 * time.Hour)
	nextDay, claimed, reason, err := restartedRepository.ClaimReminderDelivery(
		ctx,
		q6ReminderClaim(userID, "2026-07-17", cooldownKey, legacyCooldownKey, nextDayAt),
		nextDayAt,
	)
	require.NoError(t, err)
	require.True(t, claimed, "the next local day may create a new reminder after the 20-hour cooldown")
	assert.Empty(t, reason)
	assert.NotEqual(t, reclaimed.ID, nextDay.ID)

	require.NoError(t, restartedPG.Pool.QueryRow(
		ctx, `
SELECT count(*)
FROM notification_deliveries
WHERE user_id = $1 AND notification_type = $2`,
		userID,
		entity.NotificationTypeStreakReminder,
	).Scan(&rowCount))
	assert.Equal(t, 2, rowCount)
}

// TestLiveReminderCandidatesUseUserLocalBoundaries proves the scheduler and fail-closed timezone
// policy with fixed UTC instants, including seasonal New York offsets.
//
//nolint:paralleltest // serial live-DB timezone matrix over dedicated throwaway users
func TestLiveReminderCandidatesUseUserLocalBoundaries(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	repository := NewPersonalRepo(pg)
	baseline, err := repository.ReminderCandidates(
		ctx,
		time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
		q6QuietStart,
		q6QuietEnd,
	)
	require.NoError(t, err)

	jakartaUser := seedQ6NotificationUser(t, pg, new("Asia/Jakarta"), "2026-07-15")
	newYorkUser := seedQ6NotificationUser(
		t,
		pg,
		new("America/New_York"),
		"2026-01-14",
		"2026-07-15",
	)
	invalidUser := seedQ6NotificationUser(t, pg, new("Mars/Olympus"), "2026-07-15")
	missingUser := seedQ6NotificationUser(t, pg, nil, "2026-07-15")

	tests := []struct {
		name       string
		asOf       time.Time
		wantUserID string
		wantDate   string
	}{
		{
			name: "Jakarta 18:59 is before scheduler window",
			asOf: time.Date(2026, time.July, 16, 11, 59, 0, 0, time.UTC),
		},
		{
			name:       "Jakarta 19:00 is inclusive",
			asOf:       time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
			wantUserID: jakartaUser,
			wantDate:   "2026-07-16",
		},
		{
			name:       "Jakarta 20:59 remains inside scheduler window",
			asOf:       time.Date(2026, time.July, 16, 13, 59, 0, 0, time.UTC),
			wantUserID: jakartaUser,
			wantDate:   "2026-07-16",
		},
		{
			name: "Jakarta 21:00 is quiet and scheduler end",
			asOf: time.Date(2026, time.July, 16, 14, 0, 0, 0, time.UTC),
		},
	}

	for _, test := range tests {
		result, candidateErr := repository.ReminderCandidates(ctx, test.asOf, q6QuietStart, q6QuietEnd)
		require.NoError(t, candidateErr, test.name)

		candidate, found := q6FindReminderCandidate(result.Candidates, jakartaUser)
		if test.wantUserID == "" {
			assert.False(t, found, test.name)
		} else {
			require.True(t, found, test.name)
			assert.Equal(t, test.wantUserID, candidate.UserID)
			assert.Equal(t, test.wantDate, candidate.LocalDate)
			assert.Equal(t, "Asia/Jakarta", candidate.Timezone)
		}

		assert.False(t, q6HasReminderCandidate(result.Candidates, invalidUser), test.name)
		assert.False(t, q6HasReminderCandidate(result.Candidates, missingUser), test.name)
	}

	atJakartaEvening := time.Date(2026, time.July, 16, 12, 30, 0, 0, time.UTC)
	result, err := repository.ReminderCandidates(ctx, atJakartaEvening, q6QuietStart, q6QuietEnd)
	require.NoError(t, err)
	assert.True(t, q6HasReminderCandidate(result.Candidates, jakartaUser))
	assert.False(t, q6HasReminderCandidate(result.Candidates, newYorkUser))
	assert.GreaterOrEqual(t, result.InvalidTimezoneSkipped, baseline.InvalidTimezoneSkipped+1)
	assert.GreaterOrEqual(t, result.MissingTimezoneSkipped, baseline.MissingTimezoneSkipped+1)

	// The same UTC day can mean a different date and clock time per profile. At 23:30 UTC New
	// York is 19:30 on July 16 (EDT), while Jakarta is already 06:30 on July 17.
	atNewYorkEvening := time.Date(2026, time.July, 16, 23, 30, 0, 0, time.UTC)
	result, err = repository.ReminderCandidates(ctx, atNewYorkEvening, q6QuietStart, q6QuietEnd)
	require.NoError(t, err)

	newYorkCandidate, found := q6FindReminderCandidate(result.Candidates, newYorkUser)
	require.True(t, found)
	assert.Equal(t, "2026-07-16", newYorkCandidate.LocalDate)
	assert.Equal(t, "America/New_York", newYorkCandidate.Timezone)
	assert.False(t, q6HasReminderCandidate(result.Candidates, jakartaUser))

	// In January, the same 19:30 New York wall clock is five hours behind UTC instead of four.
	// This catches accidental fixed-offset handling across daylight-saving seasons.
	atNewYorkWinterEvening := time.Date(2026, time.January, 16, 0, 30, 0, 0, time.UTC)
	result, err = repository.ReminderCandidates(ctx, atNewYorkWinterEvening, q6QuietStart, q6QuietEnd)
	require.NoError(t, err)

	newYorkCandidate, found = q6FindReminderCandidate(result.Candidates, newYorkUser)
	require.True(t, found)
	assert.Equal(t, "2026-01-15", newYorkCandidate.LocalDate)
	assert.Equal(t, "2026-03-08 01:59:00", q6LocalTimestamp(
		t,
		pg,
		"America/New_York",
		time.Date(2026, time.March, 8, 6, 59, 0, 0, time.UTC),
	))
	assert.Equal(t, "2026-03-08 03:00:00", q6LocalTimestamp(
		t,
		pg,
		"America/New_York",
		time.Date(2026, time.March, 8, 7, 0, 0, 0, time.UTC),
	), "PostgreSQL must skip the nonexistent 02:00 hour at the DST transition")

	// Quiet-hours are [21:00,07:00): start inclusive, end exclusive. The reminder scheduler only
	// runs at 19:00-21:00, so exercise the general policy predicate directly in PostgreSQL.
	assert.False(t, q6LocalClockAllowed(t, pg, "06:59"))
	assert.True(t, q6LocalClockAllowed(t, pg, "07:00"))
	assert.True(t, q6LocalClockAllowed(t, pg, "20:59"))
	assert.False(t, q6LocalClockAllowed(t, pg, "21:00"))

	// The repository's quiet predicate also observes inclusive-start/exclusive-end semantics at a
	// clock value inside the scheduler window.
	result, err = repository.ReminderCandidates(
		ctx,
		time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
		"19:00",
		"07:00",
	)
	require.NoError(t, err)
	assert.False(t, q6HasReminderCandidate(result.Candidates, jakartaUser), "quiet start is inclusive")

	result, err = repository.ReminderCandidates(
		ctx,
		time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
		"21:00",
		"19:00",
	)
	require.NoError(t, err)
	assert.True(t, q6HasReminderCandidate(result.Candidates, jakartaUser), "quiet end is exclusive")

	legacyCooldownUser := seedQ6NotificationUser(
		t,
		pg,
		new("Asia/Jakarta"),
		"2026-07-15",
	)

	var legacyCooldownKey string

	err = pg.Pool.QueryRow(ctx, `
SELECT encode(sha256(
    convert_to('streak_reminder', 'UTF8') || '\x00'::bytea ||
    convert_to($1::text, 'UTF8') || '\x00'::bytea ||
    convert_to('2026-07-16', 'UTF8') || '\x00'::bytea
), 'hex')`, legacyCooldownUser).Scan(&legacyCooldownKey)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO auth_notification_cooldowns (event, key_hash, expires_at)
VALUES ('streak_reminder', $1, $2)
ON CONFLICT (event, key_hash) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		legacyCooldownKey, atJakartaEvening.Add(20*time.Hour))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := pg.Pool.Exec(context.Background(), `
DELETE FROM auth_notification_cooldowns
WHERE event = 'streak_reminder' AND key_hash = $1`, legacyCooldownKey)
		if cleanupErr != nil {
			t.Logf("cleanup legacy Q-6 cooldown: %v", cleanupErr)
		}
	})

	result, err = repository.ReminderCandidates(ctx, atJakartaEvening, q6QuietStart, q6QuietEnd)
	require.NoError(t, err)
	assert.False(t, q6HasReminderCandidate(result.Candidates, legacyCooldownUser),
		"a rolling-deploy legacy cooldown must not consume the 5,000-candidate budget")
}

// TestLiveReminderRetryIsSeparatedFromNewCandidates proves pending rows do not consume the 5,000
// new-candidate budget and are reclaimed only before the user's local 21:00 boundary.
//
//nolint:paralleltest // serial live-DB invariant over a dedicated throwaway user
func TestLiveReminderRetryIsSeparatedFromNewCandidates(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	repository := NewPersonalRepo(pg)
	userID := seedQ6NotificationUser(t, pg, new("Asia/Jakarta"), "2026-07-15")
	asOf := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	keySuffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	cooldownKeys := []string{
		keySuffix + "a",
		keySuffix + "b",
		keySuffix + "c",
		keySuffix + "d",
	}

	t.Cleanup(func() {
		_, cleanupErr := pg.Pool.Exec(
			context.Background(),
			`DELETE FROM auth_notification_cooldowns WHERE event = $1 AND key_hash = ANY($2)`,
			entity.NotificationTypeStreakReminder,
			cooldownKeys,
		)
		if cleanupErr != nil {
			t.Logf("cleanup Q-6 retry cooldowns: %v", cleanupErr)
		}
	})

	claim := q6ReminderClaim(userID, "2026-07-16", cooldownKeys[0], cooldownKeys[1], asOf)
	claim.Delivery.DeliveryDeadlineAt = time.Date(2026, time.July, 16, 14, 0, 0, 0, time.UTC)

	created, claimed, reason, err := repository.ClaimReminderDelivery(ctx, claim, asOf)
	require.NoError(t, err)
	require.True(t, claimed)
	assert.Empty(t, reason)

	candidates, err := repository.ReminderCandidates(ctx, asOf, q6QuietStart, q6QuietEnd)
	require.NoError(t, err)
	assert.False(t, q6HasReminderCandidate(candidates.Candidates, userID),
		"an existing pending row must not consume the new-candidate budget")

	beforeQuietHours := time.Date(2026, time.July, 16, 13, 59, 0, 0, time.UTC)
	recovered, err := repository.ClaimPendingReminderDeliveries(
		ctx,
		beforeQuietHours,
		q6QuietStart,
		q6QuietEnd,
		uuid.NewString(),
		beforeQuietHours.Add(time.Minute),
		10,
	)
	require.NoError(t, err)
	require.Len(t, recovered, 1)
	assert.Equal(t, created.ID, recovered[0].ID)
	assert.Equal(t, created.IdempotencyKey, recovered[0].IdempotencyKey)

	readUserID := seedQ6NotificationUser(t, pg, new("Asia/Jakarta"), "2026-07-15")
	readClaim := q6ReminderClaim(
		readUserID,
		"2026-07-16",
		cooldownKeys[2],
		cooldownKeys[3],
		asOf,
	)
	readClaim.Delivery.DeliveryDeadlineAt = time.Date(2026, time.July, 16, 14, 0, 0, 0, time.UTC)
	_, claimed, _, err = repository.ClaimReminderDelivery(ctx, readClaim, asOf)
	require.NoError(t, err)
	require.True(t, claimed)

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO reading_activity (user_id, activity_date, quran_ayahs_read, quran_events)
VALUES ($1, '2026-07-16', 1, 1)`, readUserID)
	require.NoError(t, err)

	recovered, err = repository.ClaimPendingReminderDeliveries(
		ctx,
		beforeQuietHours,
		q6QuietStart,
		q6QuietEnd,
		uuid.NewString(),
		beforeQuietHours.Add(time.Minute),
		10,
	)
	require.NoError(t, err)
	assert.Empty(t, recovered, "a user who already read today must not receive a retry")

	atQuietHours := time.Date(2026, time.July, 16, 14, 0, 0, 0, time.UTC)
	recovered, err = repository.ClaimPendingReminderDeliveries(
		ctx,
		atQuietHours,
		q6QuietStart,
		q6QuietEnd,
		uuid.NewString(),
		atQuietHours.Add(time.Minute),
		10,
	)
	require.NoError(t, err)
	assert.Empty(t, recovered, "21:00 local must never be retried")
}

// TestLiveNotificationAttemptStateAndMetricsAreAtomic proves provider evidence, delivery state,
// and durable metrics commit together, and all remain unchanged when an attempt is rejected.
//
//nolint:paralleltest // serial live-DB transaction invariant over dedicated throwaway rows
func TestLiveNotificationAttemptStateAndMetricsAreAtomic(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	repository := NewPersonalRepo(pg)
	userID := seedQ6NotificationUser(t, pg, new("Asia/Jakarta"))
	asOf := time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC)
	reasonSuffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	acceptedReason := "live_q6_accept_" + reasonSuffix
	failedReason := "live_q6_fail_" + reasonSuffix

	metricDeltas := []q6NotificationMetricDelta{
		{kind: "delivery_attempt", result: entity.PushDeliveryAccepted, reason: acceptedReason},
		{kind: "delivery_attempt", result: entity.PushDeliveryFailed, reason: failedReason},
		{kind: "delivery", result: entity.NotificationStatusAccepted},
		{kind: "delivery", result: entity.NotificationStatusFailed},
	}
	acceptedDeliveryMetricBaseline := q6NotificationMetricTotal(
		t,
		pg,
		"delivery",
		entity.NotificationStatusAccepted,
		"",
	)
	failedDeliveryMetricBaseline := q6NotificationMetricTotal(
		t,
		pg,
		"delivery",
		entity.NotificationStatusFailed,
		"",
	)
	q6CleanupNotificationMetricDeltas(t, pg, metricDeltas)

	acceptedDelivery, err := repository.CreateEventDelivery(
		ctx,
		q6EventDelivery(userID, entity.NotificationTypeNewLogin, asOf),
		asOf,
	)
	require.NoError(t, err)

	invalidAttempt := entity.NotificationDeliveryAttempt{
		ID:                     uuid.NewString(),
		DeliveryID:             acceptedDelivery.ID,
		LeaseToken:             acceptedDelivery.LeaseToken,
		Outcome:                entity.PushDeliveryAccepted,
		Terminal:               true,
		HTTPStatus:             200,
		ProviderNotificationID: "live-accepted-" + reasonSuffix,
		ReasonCode:             acceptedReason,
		ReasonDetail:           strings.Repeat("x", 2001),
		OccurredAt:             asOf.Add(time.Second),
	}
	require.Error(t, repository.RecordNotificationDeliveryAttempt(ctx, &invalidAttempt))

	q6AssertDeliveryState(t, pg, acceptedDelivery.ID, entity.NotificationStatusPending, 0, "")
	assert.Equal(t, int64(0), q6NotificationAttemptCount(t, pg, acceptedDelivery.ID))
	assert.Equal(t, int64(0), q6NotificationMetricTotal(
		t,
		pg,
		"delivery_attempt",
		entity.PushDeliveryAccepted,
		acceptedReason,
	))

	validAccepted := invalidAttempt
	validAccepted.ID = uuid.NewString()
	validAccepted.ReasonDetail = "OneSignal accepted the live-test fixture"
	require.NoError(t, repository.RecordNotificationDeliveryAttempt(ctx, &validAccepted))

	q6AssertDeliveryState(
		t,
		pg,
		acceptedDelivery.ID,
		entity.NotificationStatusAccepted,
		1,
		validAccepted.ProviderNotificationID,
	)
	assert.Equal(t, int64(1), q6NotificationAttemptCount(t, pg, acceptedDelivery.ID))
	assert.Equal(t, int64(1), q6NotificationMetricTotal(
		t,
		pg,
		"delivery_attempt",
		entity.PushDeliveryAccepted,
		acceptedReason,
	))

	failedDelivery, err := repository.CreateEventDelivery(
		ctx,
		q6EventDelivery(userID, entity.NotificationTypeNewLogin, asOf.Add(2*time.Second)),
		asOf.Add(2*time.Second),
	)
	require.NoError(t, err)

	failedAttempt := entity.NotificationDeliveryAttempt{
		ID:           uuid.NewString(),
		DeliveryID:   failedDelivery.ID,
		LeaseToken:   failedDelivery.LeaseToken,
		Outcome:      entity.PushDeliveryFailed,
		Terminal:     true,
		HTTPStatus:   400,
		ReasonCode:   failedReason,
		ReasonDetail: "provider rejected the live-test fixture",
		OccurredAt:   asOf.Add(3 * time.Second),
	}
	require.NoError(t, repository.RecordNotificationDeliveryAttempt(ctx, &failedAttempt))

	q6AssertDeliveryState(t, pg, failedDelivery.ID, entity.NotificationStatusFailed, 1, "")
	assert.Equal(t, int64(1), q6NotificationAttemptCount(t, pg, failedDelivery.ID))
	assert.Equal(t, int64(1), q6NotificationMetricTotal(
		t,
		pg,
		"delivery_attempt",
		entity.PushDeliveryFailed,
		failedReason,
	))
	assert.Equal(t, acceptedDeliveryMetricBaseline+1, q6NotificationMetricTotal(
		t,
		pg,
		"delivery",
		entity.NotificationStatusAccepted,
		"",
	))
	assert.Equal(t, failedDeliveryMetricBaseline+1, q6NotificationMetricTotal(
		t,
		pg,
		"delivery",
		entity.NotificationStatusFailed,
		"",
	))
}

// TestLiveNotificationExpirerDoesNotRaceActiveProviderRequest proves a request started before its
// deadline cannot be marked failed by another instance while its lease is still active.
//
//nolint:paralleltest // serial live-DB lease invariant with metric cleanup
func TestLiveNotificationExpirerDoesNotRaceActiveProviderRequest(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	repository := NewPersonalRepo(pg)
	userID := seedQ6NotificationUser(t, pg, new("Asia/Jakarta"))
	asOf := time.Now().UTC().Truncate(time.Second)
	reason := "lease_race_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	q6CleanupNotificationMetricDeltas(t, pg, []q6NotificationMetricDelta{
		{kind: "delivery_attempt", result: entity.PushDeliveryAccepted, reason: reason},
		{kind: "delivery", result: entity.NotificationStatusAccepted},
	})

	create := q6EventDelivery(userID, entity.NotificationTypeNewLogin, asOf)
	create.LeaseExpiresAt = asOf.Add(time.Minute)
	create.DeliveryDeadlineAt = asOf.Add(10 * time.Second)
	delivery, err := repository.CreateEventDelivery(ctx, create, asOf)
	require.NoError(t, err)

	expired, err := repository.ExpireNotificationDeliveries(ctx, asOf.Add(20*time.Second))
	require.NoError(t, err)
	assert.Zero(t, expired, "an active provider lease must protect the in-flight request")

	attempt := entity.NotificationDeliveryAttempt{
		ID:                     uuid.NewString(),
		DeliveryID:             delivery.ID,
		LeaseToken:             delivery.LeaseToken,
		Outcome:                entity.PushDeliveryAccepted,
		ProviderNotificationID: "accepted-after-deadline",
		ReasonCode:             reason,
		OccurredAt:             asOf.Add(20 * time.Second),
	}
	require.NoError(t, repository.RecordNotificationDeliveryAttempt(ctx, &attempt))
	q6AssertDeliveryState(
		t,
		pg,
		delivery.ID,
		entity.NotificationStatusAccepted,
		1,
		"accepted-after-deadline",
	)
}

func q6ReminderClaim(
	userID,
	localDate,
	cooldownKey,
	legacyCooldownKey string,
	asOf time.Time,
) *entity.ReminderDeliveryClaim {
	return &entity.ReminderDeliveryClaim{
		Delivery: entity.NotificationDeliveryCreate{
			ID:               uuid.NewString(),
			UserID:           userID,
			NotificationType: entity.NotificationTypeStreakReminder,
			LocalDate:        localDate,
			Payload: entity.PushNotification{
				ExternalIDs: []string{userID},
				Contents:    map[string]string{"en": "Live reminder invariant"},
			},
			IdempotencyKey:     uuid.NewString(),
			LeaseToken:         uuid.NewString(),
			LeaseExpiresAt:     asOf.Add(time.Minute),
			DeliveryDeadlineAt: asOf.Add(9 * time.Hour),
		},
		CooldownKeyHash:       cooldownKey,
		LegacyCooldownKeyHash: legacyCooldownKey,
		CooldownExpiresAt:     asOf.Add(20 * time.Hour),
	}
}

func q6EventDelivery(userID, notificationType string, asOf time.Time) *entity.NotificationDeliveryCreate {
	return &entity.NotificationDeliveryCreate{
		ID:               uuid.NewString(),
		UserID:           userID,
		NotificationType: notificationType,
		Payload: entity.PushNotification{
			ExternalIDs: []string{userID},
			Contents:    map[string]string{"en": "Live event invariant"},
		},
		IdempotencyKey:     uuid.NewString(),
		LeaseToken:         uuid.NewString(),
		LeaseExpiresAt:     asOf.Add(time.Minute),
		DeliveryDeadlineAt: asOf.Add(24 * time.Hour),
	}
}

func seedQ6NotificationUser(
	t *testing.T,
	pg *postgres.Postgres,
	timezone *string,
	activityDates ...string,
) string {
	t.Helper()

	ctx := context.Background()
	userID := uuid.NewString()
	suffix := strings.ReplaceAll(userID, "-", "")
	_, err := pg.Pool.Exec(ctx, `
INSERT INTO users (id, username, email, password_hash)
VALUES ($1, $2, $3, 'live-q6')`, userID, "live-q6-"+suffix, "live-q6-"+suffix+"@example.test")
	require.NoError(t, err)

	_, err = pg.Pool.Exec(ctx, `
INSERT INTO user_profiles (user_id, timezone) VALUES ($1, $2)`, userID, timezone)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `
INSERT INTO user_preferences (user_id, preferred_ui_lang, notify_streak_reminders)
VALUES ($1, 'id', TRUE)`, userID)
	require.NoError(t, err)

	for _, activityDate := range activityDates {
		_, err = pg.Pool.Exec(ctx, `
INSERT INTO reading_activity (user_id, activity_date, quran_ayahs_read, quran_events)
VALUES ($1, $2::date, 1, 1)`, userID, activityDate)
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		if _, cleanupErr := pg.Pool.Exec(
			context.Background(),
			`DELETE FROM users WHERE id = $1`,
			userID,
		); cleanupErr != nil {
			t.Logf("cleanup Q-6 user %s: %v", userID, cleanupErr)
		}
	})

	return userID
}

func q6FindReminderCandidate(
	candidates []entity.ReminderCandidate,
	userID string,
) (entity.ReminderCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.UserID == userID {
			return candidate, true
		}
	}

	return entity.ReminderCandidate{}, false
}

func q6HasReminderCandidate(candidates []entity.ReminderCandidate, userID string) bool {
	_, found := q6FindReminderCandidate(candidates, userID)

	return found
}

func q6LocalClockAllowed(
	t *testing.T,
	pg *postgres.Postgres,
	localClock string,
) bool {
	t.Helper()

	var allowed bool

	err := pg.Pool.QueryRow(context.Background(), `
SELECT NOT CASE
    WHEN $2::time < $3::time
        THEN $1::time >= $2::time AND $1::time < $3::time
    ELSE $1::time >= $2::time OR $1::time < $3::time
END`, localClock, q6QuietStart, q6QuietEnd).Scan(&allowed)
	require.NoError(t, err)

	return allowed
}

func q6LocalTimestamp(
	t *testing.T,
	pg *postgres.Postgres,
	timezone string,
	instant time.Time,
) string {
	t.Helper()

	var localTimestamp string

	err := pg.Pool.QueryRow(context.Background(), `
SELECT to_char(timezone($1, $2::timestamptz), 'YYYY-MM-DD HH24:MI:SS')`,
		timezone, instant).Scan(&localTimestamp)
	require.NoError(t, err)

	return localTimestamp
}

func q6AssertDeliveryState(
	t *testing.T,
	pg *postgres.Postgres,
	deliveryID,
	wantStatus string,
	wantAttemptCount int,
	wantProviderID string,
) {
	t.Helper()

	var status, providerID string

	var attemptCount int

	err := pg.Pool.QueryRow(context.Background(), `
SELECT status, attempt_count, COALESCE(provider_notification_id, '')
FROM notification_deliveries
WHERE id = $1`, deliveryID).Scan(&status, &attemptCount, &providerID)
	require.NoError(t, err)
	assert.Equal(t, wantStatus, status)
	assert.Equal(t, wantAttemptCount, attemptCount)
	assert.Equal(t, wantProviderID, providerID)
}

func q6NotificationAttemptCount(t *testing.T, pg *postgres.Postgres, deliveryID string) int64 {
	t.Helper()

	var count int64

	err := pg.Pool.QueryRow(context.Background(), `
SELECT count(*) FROM notification_delivery_attempts WHERE delivery_id = $1`, deliveryID).Scan(&count)
	require.NoError(t, err)

	return count
}

func q6NotificationMetricTotal(
	t *testing.T,
	pg *postgres.Postgres,
	kind,
	result,
	reason string,
) int64 {
	t.Helper()

	var total int64

	err := pg.Pool.QueryRow(context.Background(), `
SELECT COALESCE((
    SELECT total
    FROM notification_delivery_metric_totals
    WHERE metric_kind = $1
      AND notification_type = $2
      AND result = $3
      AND reason_code = $4
), 0)`, kind, entity.NotificationTypeNewLogin, result, reason).Scan(&total)
	require.NoError(t, err)

	return total
}

type q6NotificationMetricDelta struct {
	kind   string
	result string
	reason string
	before int64
}

func q6CleanupNotificationMetricDeltas(
	t *testing.T,
	pg *postgres.Postgres,
	deltas []q6NotificationMetricDelta,
) {
	t.Helper()

	for index := range deltas {
		deltas[index].before = q6NotificationMetricTotal(
			t,
			pg,
			deltas[index].kind,
			deltas[index].result,
			deltas[index].reason,
		)
	}

	t.Cleanup(func() {
		ctx := context.Background()

		for _, delta := range deltas {
			var err error
			if delta.before == 0 {
				_, err = pg.Pool.Exec(
					ctx, `
DELETE FROM notification_delivery_metric_totals
WHERE metric_kind = $1
  AND notification_type = $2
  AND result = $3
  AND reason_code = $4`,
					delta.kind,
					entity.NotificationTypeNewLogin,
					delta.result,
					delta.reason,
				)
			} else {
				_, err = pg.Pool.Exec(
					ctx, `
UPDATE notification_delivery_metric_totals
SET total = $5
WHERE metric_kind = $1
  AND notification_type = $2
  AND result = $3
  AND reason_code = $4`,
					delta.kind,
					entity.NotificationTypeNewLogin,
					delta.result,
					delta.reason,
					delta.before,
				)
			}

			if err != nil {
				t.Logf("cleanup Q-6 metric %s/%s/%s: %v", delta.kind, delta.result, delta.reason, err)
			}
		}
	})
}
