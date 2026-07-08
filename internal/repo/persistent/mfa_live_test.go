package persistent

import (
	"context"
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

// TestLiveMFAEnrollmentLifecycle covers the user_mfa row through its whole
// life: pending upsert (re-enroll rotates in place), confirm, the
// already-enabled guard, the monotonic TOTP-step replay guard, and delete.
// Gated on SURAU_LIVE_PG.
//
//nolint:paralleltest // serial live-DB checks over one throwaway user
func TestLiveMFAEnrollmentLifecycle(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	repo := NewUserRepo(pg)
	ctx := context.Background()
	userID := uuid.NewString()
	seedLiveUser(t, pg, userID, "mfa-lifecycle")

	_, err = repo.GetMFA(ctx, userID)
	require.ErrorIs(t, err, entity.ErrMFANotEnabled, "no row yet")

	require.NoError(t, repo.UpsertPendingMFA(ctx, userID, "enc-secret-1"))
	require.NoError(t, repo.UpsertPendingMFA(ctx, userID, "enc-secret-2"), "pending re-enroll rotates in place")

	mfa, err := repo.GetMFA(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "enc-secret-2", mfa.TOTPSecretEnc)
	assert.Nil(t, mfa.ConfirmedAt, "still pending")

	require.NoError(t, repo.ConfirmMFA(ctx, userID))
	require.ErrorIs(t, repo.ConfirmMFA(ctx, userID), entity.ErrMFAEnrollmentNotStarted, "second confirm has nothing pending")

	mfa, err = repo.GetMFA(ctx, userID)
	require.NoError(t, err)
	require.NotNil(t, mfa.ConfirmedAt)

	require.ErrorIs(t, repo.UpsertPendingMFA(ctx, userID, "enc-secret-3"), entity.ErrMFAAlreadyEnabled,
		"a confirmed enrollment is never overwritten")

	// Replay guard: step must strictly advance.
	require.NoError(t, repo.AdvanceMFATOTPStep(ctx, userID, 100))
	require.ErrorIs(t, repo.AdvanceMFATOTPStep(ctx, userID, 100), entity.ErrInvalidMFACode, "same step = replay")
	require.ErrorIs(t, repo.AdvanceMFATOTPStep(ctx, userID, 99), entity.ErrInvalidMFACode, "older step = replay")
	require.NoError(t, repo.AdvanceMFATOTPStep(ctx, userID, 101))

	require.NoError(t, repo.DeleteMFA(ctx, userID))

	_, err = repo.GetMFA(ctx, userID)
	require.ErrorIs(t, err, entity.ErrMFANotEnabled)
}

// TestLiveMFARecoveryCodeSingleConsume is AC-3 at the database level: a
// recovery code spends exactly once, including under a concurrent double-spend.
//
//nolint:paralleltest // serial live-DB checks over one throwaway user
func TestLiveMFARecoveryCodeSingleConsume(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	repo := NewUserRepo(pg)
	ctx := context.Background()
	userID := uuid.NewString()
	seedLiveUser(t, pg, userID, "mfa-recovery")

	hashes := []string{
		"1111111111111111111111111111111111111111111111111111111111111111",
		"2222222222222222222222222222222222222222222222222222222222222222",
		"3333333333333333333333333333333333333333333333333333333333333333",
	}
	require.NoError(t, repo.ReplaceRecoveryCodes(ctx, userID, hashes))

	count, err := repo.CountUnusedRecoveryCodes(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	require.NoError(t, repo.ConsumeRecoveryCode(ctx, userID, hashes[0]))
	require.ErrorIs(t, repo.ConsumeRecoveryCode(ctx, userID, hashes[0]), entity.ErrInvalidMFACode,
		"second use of the same code must fail (AC-3)")
	require.ErrorIs(t, repo.ConsumeRecoveryCode(ctx, userID, "ffff"), entity.ErrInvalidMFACode, "unknown code")

	// Concurrent double-spend: exactly one goroutine wins.
	var (
		wg        sync.WaitGroup
		successes int
		mu        sync.Mutex
	)

	for range 2 {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := repo.ConsumeRecoveryCode(ctx, userID, hashes[1]); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, 1, successes, "concurrent double-spend must yield exactly one success")

	count, err = repo.CountUnusedRecoveryCodes(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Replacing the set invalidates everything unspent.
	require.NoError(t, repo.ReplaceRecoveryCodes(ctx, userID, hashes[:1]))
	require.ErrorIs(t, repo.ConsumeRecoveryCode(ctx, userID, hashes[2]), entity.ErrInvalidMFACode,
		"old set gone after regenerate")
	require.NoError(t, repo.ConsumeRecoveryCode(ctx, userID, hashes[0]))
}

// TestLiveMFAChallengeConsumeOnce proves login/reset challenges are one-shot
// and expiry-scoped.
//
//nolint:paralleltest // serial live-DB checks over one throwaway user
func TestLiveMFAChallengeConsumeOnce(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	repo := NewUserRepo(pg)
	ctx := context.Background()
	userID := uuid.NewString()
	seedLiveUser(t, pg, userID, "mfa-challenge")

	live := entity.MFAChallenge{
		ID:        uuid.NewString(),
		UserID:    userID,
		Purpose:   entity.MFAChallengePurposeLogin,
		TokenHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
		ClientIP:  "127.0.0.1",
		UserAgent: "live-test",
	}
	require.NoError(t, repo.CreateMFAChallenge(ctx, live))

	got, err := repo.GetMFAChallengeByTokenHash(ctx, live.TokenHash, entity.MFAChallengePurposeLogin)
	require.NoError(t, err)
	assert.Equal(t, live.ID, got.ID)

	_, err = repo.GetMFAChallengeByTokenHash(ctx, live.TokenHash, entity.MFAChallengePurposeReset)
	require.ErrorIs(t, err, entity.ErrInvalidMFAChallenge, "purpose mismatch is invalid")

	require.NoError(t, repo.ConsumeMFAChallenge(ctx, live.ID))
	require.ErrorIs(t, repo.ConsumeMFAChallenge(ctx, live.ID), entity.ErrInvalidMFAChallenge, "one-shot")

	_, err = repo.GetMFAChallengeByTokenHash(ctx, live.TokenHash, entity.MFAChallengePurposeLogin)
	require.ErrorIs(t, err, entity.ErrInvalidMFAChallenge, "consumed challenge no longer live")

	expired := entity.MFAChallenge{
		ID:        uuid.NewString(),
		UserID:    userID,
		Purpose:   entity.MFAChallengePurposeLogin,
		TokenHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ExpiresAt: time.Now().UTC().Add(5 * time.Minute),
	}
	require.NoError(t, repo.CreateMFAChallenge(ctx, expired))

	// Age it via SQL so the check is exact regardless of the host timezone
	// (columns are naive TIMESTAMP; prod and CI run in UTC).
	_, err = pg.Pool.Exec(ctx, `UPDATE mfa_challenges SET expires_at = now() - interval '1 minute' WHERE id = $1`, expired.ID)
	require.NoError(t, err)

	_, err = repo.GetMFAChallengeByTokenHash(ctx, expired.TokenHash, entity.MFAChallengePurposeLogin)
	require.ErrorIs(t, err, entity.ErrInvalidMFAChallenge, "expired challenge no longer live")
	require.ErrorIs(t, repo.ConsumeMFAChallenge(ctx, expired.ID), entity.ErrInvalidMFAChallenge)
}

// TestLiveMFASessionStampGateAndRotation proves the step-up anchor: stamping
// the active family row, reading it back through the gate query (with the
// users.mfa_enforced_from anchor), and the stamp surviving refresh rotation.
//
//nolint:paralleltest // serial live-DB checks over one throwaway user
func TestLiveMFASessionStampGateAndRotation(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	repo := NewUserRepo(pg)
	ctx := context.Background()
	userID := uuid.NewString()
	seedLiveUser(t, pg, userID, "mfa-gate")

	familyID := uuid.NewString()
	first := entity.AuthSession{
		ID:               familyID,
		FamilyID:         familyID,
		UserID:           userID,
		RefreshTokenHash: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		CreatedAt:        time.Now(),
		LastUsedAt:       time.Now(),
		ExpiresAt:        time.Now().Add(time.Hour),
	}
	require.NoError(t, repo.CreateAuthSession(ctx, first))

	gate, err := repo.GetMFAGateData(ctx, userID, familyID)
	require.NoError(t, err)
	assert.False(t, gate.Confirmed)
	assert.False(t, gate.Pending)
	assert.Nil(t, gate.EnforcedFrom)
	assert.Nil(t, gate.MFAVerifiedAt)

	// Enrollment states surface through the same single query.
	require.NoError(t, repo.UpsertPendingMFA(ctx, userID, "enc"))

	gate, err = repo.GetMFAGateData(ctx, userID, familyID)
	require.NoError(t, err)
	assert.True(t, gate.Pending)
	assert.False(t, gate.Confirmed)

	require.NoError(t, repo.ConfirmMFA(ctx, userID))

	stamp := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, repo.SetSessionMFAVerified(ctx, userID, familyID, stamp))

	gate, err = repo.GetMFAGateData(ctx, userID, familyID)
	require.NoError(t, err)
	assert.True(t, gate.Confirmed)
	require.NotNil(t, gate.MFAVerifiedAt)
	assert.WithinDuration(t, stamp, *gate.MFAVerifiedAt, time.Second)

	// The grace anchor flows from users.mfa_enforced_from (stamped by the
	// role-change path).
	_, err = pg.Pool.Exec(ctx, `UPDATE users SET mfa_enforced_from = now() WHERE id = $1`, userID)
	require.NoError(t, err)

	gate, err = repo.GetMFAGateData(ctx, userID, familyID)
	require.NoError(t, err)
	require.NotNil(t, gate.EnforcedFrom)

	// Rotation: the successor row carries the copied stamp (the usecase
	// closure copies it; here we prove the column round-trips).
	fetched, err := repo.GetAuthSessionByTokenHash(ctx, first.RefreshTokenHash)
	require.NoError(t, err)
	require.NotNil(t, fetched.MFAVerifiedAt)

	next := entity.AuthSession{
		ID:               uuid.NewString(),
		FamilyID:         familyID,
		UserID:           userID,
		RefreshTokenHash: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
		CreatedAt:        time.Now(),
		LastUsedAt:       time.Now(),
		ExpiresAt:        time.Now().Add(time.Hour),
		MFAVerifiedAt:    fetched.MFAVerifiedAt,
	}
	require.NoError(t, repo.RotateAuthSession(ctx, first.ID, next))

	gate, err = repo.GetMFAGateData(ctx, userID, familyID)
	require.NoError(t, err)
	require.NotNil(t, gate.MFAVerifiedAt, "stamp survives rotation on the active row")
	assert.WithinDuration(t, stamp, *gate.MFAVerifiedAt, time.Second)

	// Stamping an all-revoked family reports no active session.
	_, err = repo.RevokeAuthSessionFamily(ctx, familyID)
	require.NoError(t, err)
	require.ErrorIs(t, repo.SetSessionMFAVerified(ctx, userID, familyID, time.Now()), entity.ErrAuthSessionNotFound)
}
