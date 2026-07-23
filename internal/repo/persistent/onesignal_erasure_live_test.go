package persistent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const liveOneSignalAppID = "7a650cae-1c1e-4b19-a7fe-393c14b894f0"

//nolint:paralleltest // destructive schema replay is restricted to the dedicated serial live database
func TestLiveOneSignalErasureMigrationRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	up, err := os.ReadFile("../../../migrations/20260723000001_onesignal_user_erasures.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../../migrations/20260723000001_onesignal_user_erasures.down.sql")
	require.NoError(t, err)

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)
	t.Cleanup(func() {
		var tableName *string

		cleanupErr := pg.Pool.QueryRow(
			context.Background(),
			`SELECT to_regclass('public.onesignal_user_erasures')::text`,
		).Scan(&tableName)
		if cleanupErr == nil && tableName == nil {
			_, cleanupErr = pg.Pool.Exec(context.Background(), string(up))
		}

		assert.NoError(t, cleanupErr)
	})

	ctx := context.Background()
	_, err = pg.Pool.Exec(ctx, string(down))
	require.NoError(t, err)

	var erasedTable *string
	require.NoError(t, pg.Pool.QueryRow(
		ctx,
		`SELECT to_regclass('public.onesignal_user_erasures')::text`,
	).Scan(&erasedTable))
	assert.Nil(t, erasedTable)

	_, err = pg.Pool.Exec(ctx, string(up))
	require.NoError(t, err)

	var constraints, indexes int
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM pg_constraint
WHERE conrelid = 'onesignal_user_erasures'::regclass
  AND conname IN (
    'onesignal_user_erasures_status_check',
    'onesignal_user_erasures_verified_check',
    'onesignal_user_erasures_lease_check'
  )`).Scan(&constraints))
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*)
FROM pg_indexes
WHERE tablename = 'onesignal_user_erasures'
  AND indexname IN (
    'onesignal_user_erasures_due_idx',
    'onesignal_user_erasures_verified_retention_idx'
  )`).Scan(&indexes))
	assert.Equal(t, 3, constraints)
	assert.Equal(t, 2, indexes)
}

//nolint:paralleltest // serial transaction assertions over dedicated throwaway users
func TestLiveDeleteAccountWritesOneSignalErasureAtomically(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	store := NewUserRepo(pg)
	userID := uuid.NewString()
	seedLiveUser(t, pg, userID, "erasure-commit-"+userID[:8])
	session := liveAuthSession(userID, time.Now().UTC(), "erasure-commit")
	require.NoError(t, store.CreateAuthSession(ctx, session))

	erasure := liveOneSignalErasure(userID, "encrypted-external-id")

	t.Cleanup(func() {
		_, cleanupErr := pg.Pool.Exec(
			context.Background(),
			`DELETE FROM onesignal_user_erasures WHERE id = $1`,
			erasure.ID,
		)
		assert.NoError(t, cleanupErr)
	})

	require.NoError(t, store.DeleteAccount(ctx, userID, &erasure))

	var (
		deletedAt    *time.Time
		tokenVersion int64
		sessionCount int
		status       string
		ciphertext   string
	)
	require.NoError(t, pg.Pool.QueryRow(
		ctx,
		`SELECT deleted_at, token_version FROM users WHERE id = $1`,
		userID,
	).Scan(&deletedAt, &tokenVersion))
	require.NoError(t, pg.Pool.QueryRow(
		ctx,
		`SELECT count(*) FROM auth_sessions WHERE user_id = $1`,
		userID,
	).Scan(&sessionCount))
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT status, external_id_ciphertext
FROM onesignal_user_erasures
WHERE id = $1`, erasure.ID).Scan(&status, &ciphertext))

	require.NotNil(t, deletedAt)
	assert.Positive(t, tokenVersion)
	assert.Zero(t, sessionCount)
	assert.Equal(t, entity.OneSignalErasureStatusPending, status)
	assert.Equal(t, erasure.ExternalIDCiphertext, ciphertext)

	rollbackUserID := uuid.NewString()
	rollbackSuffix := "erasure-rollback-" + rollbackUserID[:8]
	seedLiveUser(t, pg, rollbackUserID, rollbackSuffix)
	rollbackSession := liveAuthSession(rollbackUserID, time.Now().UTC(), "erasure-rollback")
	require.NoError(t, store.CreateAuthSession(ctx, rollbackSession))

	brokenErasure := liveOneSignalErasure(rollbackUserID, "encrypted-rollback-id")
	brokenErasure.AppID = "not-a-uuid"

	require.Error(t, store.DeleteAccount(ctx, rollbackUserID, &brokenErasure))

	var (
		rollbackDeletedAt *time.Time
		rollbackUsername  string
		rollbackSessions  int
		rollbackOutbox    int
	)
	require.NoError(t, pg.Pool.QueryRow(
		ctx,
		`SELECT deleted_at, username FROM users WHERE id = $1`,
		rollbackUserID,
	).Scan(&rollbackDeletedAt, &rollbackUsername))
	require.NoError(t, pg.Pool.QueryRow(
		ctx,
		`SELECT count(*) FROM auth_sessions WHERE user_id = $1`,
		rollbackUserID,
	).Scan(&rollbackSessions))
	require.NoError(t, pg.Pool.QueryRow(
		ctx,
		`SELECT count(*) FROM onesignal_user_erasures WHERE id = $1`,
		brokenErasure.ID,
	).Scan(&rollbackOutbox))

	assert.Nil(t, rollbackDeletedAt)
	assert.Equal(t, "live-"+rollbackSuffix, rollbackUsername)
	assert.Equal(t, 1, rollbackSessions)
	assert.Zero(t, rollbackOutbox)
}

//nolint:paralleltest // serial lease/concurrency assertions over one durable row
func TestLiveOneSignalErasureLeaseVerificationAndRetention(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	store := NewUserRepo(pg)
	now := time.Now().UTC().Truncate(time.Microsecond)
	erasure := liveOneSignalErasure(uuid.NewString(), "encrypted-lease-id")
	_, err = pg.Pool.Exec(
		ctx, `
INSERT INTO onesignal_user_erasures (
    id, app_id, external_id_ciphertext, external_id_hash, status, next_attempt_at
) VALUES ($1, $2, $3, $4, 'pending', $5)`,
		erasure.ID,
		erasure.AppID,
		erasure.ExternalIDCiphertext,
		erasure.ExternalIDHash,
		now,
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := pg.Pool.Exec(
			context.Background(),
			`DELETE FROM onesignal_user_erasures WHERE id = $1`,
			erasure.ID,
		)
		assert.NoError(t, cleanupErr)
	})

	var (
		start   = make(chan struct{})
		results = make(chan []entity.OneSignalErasure, 2)
		errs    = make(chan error, 2)
		workers sync.WaitGroup
	)

	for _, lease := range []string{uuid.NewString(), uuid.NewString()} {
		workers.Add(1)

		go func(leaseToken string) {
			defer workers.Done()

			<-start

			rows, claimErr := store.ClaimDueOneSignalErasures(
				ctx,
				now,
				leaseToken,
				now.Add(time.Minute),
				1,
			)
			results <- rows

			errs <- claimErr
		}(lease)
	}

	close(start)
	workers.Wait()
	close(results)
	close(errs)

	totalClaimed := 0

	for claimErr := range errs {
		require.NoError(t, claimErr)
	}

	for claimed := range results {
		totalClaimed += len(claimed)
	}

	assert.Equal(t, 1, totalClaimed, "two instances must not claim the same durable row")

	reclaimed, err := store.ClaimDueOneSignalErasures(
		ctx,
		now.Add(2*time.Minute),
		uuid.NewString(),
		now.Add(3*time.Minute),
		1,
	)
	require.NoError(t, err)
	require.Len(t, reclaimed, 1, "an expired lease must be reclaimable after restart")

	verifiedAt := now.Add(2 * time.Minute)
	require.NoError(t, store.RecordOneSignalErasureAttempt(ctx, &entity.OneSignalErasureAttempt{
		ID:                  uuid.NewString(),
		ErasureID:           erasure.ID,
		LeaseToken:          reclaimed[0].LeaseToken,
		Operation:           "verify",
		Status:              entity.OneSignalErasureStatusVerified,
		HTTPStatus:          404,
		ReasonCode:          "not_found",
		ReasonDetail:        "[redacted-id]",
		NextAttemptAt:       verifiedAt,
		VerifiedAt:          &verifiedAt,
		ClearExternalID:     true,
		AttemptedAt:         verifiedAt,
		ProviderCallOutcome: "not_found",
	}))

	var (
		ciphertext *string
		status     string
		evidence   string
	)
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT external_id_ciphertext, status
FROM onesignal_user_erasures
WHERE id = $1`, erasure.ID).Scan(&ciphertext, &status))
	require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT reason_detail
FROM onesignal_user_erasure_attempts
WHERE erasure_id = $1`, erasure.ID).Scan(&evidence))
	assert.Nil(t, ciphertext)
	assert.Equal(t, entity.OneSignalErasureStatusVerified, status)
	assert.NotContains(t, evidence, erasure.ExternalIDHash)
	assert.NotContains(t, evidence, "eyJ")

	cleaned, err := store.CleanupVerifiedOneSignalErasures(ctx, verifiedAt.Add(time.Second))
	require.NoError(t, err)
	assert.EqualValues(t, 1, cleaned)
}

func liveOneSignalErasure(externalID, ciphertext string) entity.OneSignalErasureCreate {
	digest := sha256.Sum256([]byte(externalID))

	return entity.OneSignalErasureCreate{
		ID:                   uuid.NewString(),
		AppID:                liveOneSignalAppID,
		ExternalIDCiphertext: ciphertext,
		ExternalIDHash:       hex.EncodeToString(digest[:]),
		NextAttemptAt:        time.Now().UTC(),
	}
}
