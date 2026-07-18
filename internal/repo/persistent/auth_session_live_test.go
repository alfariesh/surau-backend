package persistent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	repoContract "github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:paralleltest // serial lifecycle assertions over one throwaway user
func TestLiveAuthSessionSlidingValidityIsAtomic(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	store := NewUserRepo(pg)
	ctx := context.Background()
	userID := uuid.NewString()
	seedLiveUser(t, pg, userID, "auth-session-"+userID[:8])

	const refreshTTL = 336 * time.Hour

	now := time.Now().UTC().Truncate(time.Microsecond)
	validity := repoContract.AuthSessionValidity{Now: now, IdleCutoff: now.Add(-refreshTTL)}

	idle := liveAuthSession(userID, now, "idle")
	idle.LastUsedAt = validity.IdleCutoff
	require.NoError(t, store.CreateAuthSession(ctx, idle))

	idleNext := liveAuthSession(userID, now, "idle-next")
	idleNext.FamilyID = idle.FamilyID
	err = store.RotateAuthSession(ctx, idle.ID, &idleNext, validity)
	require.ErrorIs(t, err, entity.ErrRefreshSessionExpired)

	var (
		idleRevokedAt *time.Time
		idleNextCount int
	)
	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT revoked_at FROM auth_sessions WHERE id = $1`, idle.ID).Scan(&idleRevokedAt))
	require.Nil(t, idleRevokedAt, "expiry must not mutate or revoke the family")
	require.NoError(t, pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM auth_sessions WHERE id = $1`, idleNext.ID).Scan(&idleNextCount))
	assert.Zero(t, idleNextCount)

	active := liveAuthSession(userID, now, "active")
	active.LastUsedAt = validity.IdleCutoff.Add(time.Second)
	require.NoError(t, store.CreateAuthSession(ctx, active))

	activeNext := liveAuthSession(userID, now, "active-next")
	activeNext.FamilyID = active.FamilyID
	require.NoError(t, store.RotateAuthSession(ctx, active.ID, &activeNext, validity))

	replay := liveAuthSession(userID, now, "replay")
	require.ErrorIs(
		t,
		store.RotateAuthSession(ctx, active.ID, &replay, validity),
		entity.ErrInvalidRefreshToken,
		"the already-spent predecessor remains classified as reuse",
	)

	listed, err := store.ListActiveAuthSessions(ctx, userID, validity)
	require.NoError(t, err)
	require.Len(t, listed, 1, "the dormant legacy row is hidden from manage-devices")
	assert.Equal(t, activeNext.ID, listed[0].ID)
	require.ErrorIs(
		t,
		store.RevokeAuthSessionByID(ctx, userID, idle.ID, validity),
		entity.ErrAuthSessionNotFound,
	)
}

func liveAuthSession(userID string, now time.Time, seed string) entity.AuthSession {
	digest := sha256.Sum256([]byte(seed + userID))
	id := uuid.NewString()

	return entity.AuthSession{
		ID:               id,
		FamilyID:         id,
		UserID:           userID,
		RefreshTokenHash: hex.EncodeToString(digest[:]),
		CreatedAt:        now,
		LastUsedAt:       now,
		ExpiresAt:        now.Add(30 * 24 * time.Hour),
	}
}
