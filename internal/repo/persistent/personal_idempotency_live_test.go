package persistent

import (
	"context"
	"os"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedLiveUser inserts a throwaway user (FK target for cycles/saved_items) and
// registers a cascading cleanup. Gated callers must already have SURAU_LIVE_PG.
func seedLiveUser(t *testing.T, pg *postgres.Postgres, userID, suffix string) {
	t.Helper()
	_, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO users (id, username, email, password_hash) VALUES ($1, $2, $3, 'x')
		 ON CONFLICT (id) DO NOTHING`,
		userID, "live-"+suffix, "live-"+suffix+"@example.test")
	require.NoError(t, err)
	t.Cleanup(func() { // ON DELETE CASCADE removes the cycle/marks/saved_items too
		if _, err := pg.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Logf("cleanup user %s: %v", userID, err)
		}
	})
}

// TestLiveKhatamMarkIdempotency proves F04: an idempotent re-mark/no-op unmark
// reports changed=false and does NOT bump the cycle's updated_at (so retries/offline
// replay neither churn the sync cursor nor re-fire the milestone notifier).
func TestLiveKhatamMarkIdempotency(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	t.Parallel()

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	repo := NewPersonalRepo(pg)
	ctx := context.Background()
	userID := "a1111111-1111-1111-1111-111111111111"
	cycleID := "a2222222-2222-2222-2222-222222222222"

	seedLiveUser(t, pg, userID, "khatam")

	_, err = pg.Pool.Exec(ctx, `INSERT INTO quran_khatam_cycles (id, user_id, started_at) VALUES ($1, $2, now())`, cycleID, userID)
	require.NoError(t, err)

	c1, ch1, err := repo.MarkKhatamJuz(ctx, userID, 5)
	require.NoError(t, err)
	assert.True(t, ch1, "first mark is a real change")

	c2, ch2, err := repo.MarkKhatamJuz(ctx, userID, 5)
	require.NoError(t, err)
	assert.False(t, ch2, "idempotent re-mark reports no change")
	assert.True(t, c1.UpdatedAt.Equal(c2.UpdatedAt), "no-op re-mark must NOT bump updated_at")

	c3, ch3, err := repo.UnmarkKhatamJuz(ctx, userID, 5)
	require.NoError(t, err)
	assert.True(t, ch3, "real unmark reports a change")
	assert.True(t, c3.UpdatedAt.After(c2.UpdatedAt), "real unmark bumps updated_at")

	c4, ch4, err := repo.UnmarkKhatamJuz(ctx, userID, 5)
	require.NoError(t, err)
	assert.False(t, ch4, "no-op unmark reports no change")
	assert.True(t, c3.UpdatedAt.Equal(c4.UpdatedAt), "no-op unmark must NOT bump updated_at")
}

// TestLiveSavedItemIdempotency proves F22: an idempotent re-upsert and a value-identical
// PATCH leave updated_at (and thus the sync cursor) untouched, while real changes bump it.
func TestLiveSavedItemIdempotency(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	t.Parallel()

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	repo := NewPersonalRepo(pg)
	ctx := context.Background()
	userID := "b1111111-1111-1111-1111-111111111111"

	seedLiveUser(t, pg, userID, "saveditem")

	_, err = pg.Pool.Exec(ctx, `INSERT INTO quran_surahs (surah_id, ayah_count) VALUES (2, 286) ON CONFLICT (surah_id) DO NOTHING`)
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, `INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key) VALUES (2, 255, '2:255') ON CONFLICT (surah_id, ayah_number) DO NOTHING`)
	require.NoError(t, err)

	surah, ayahKey, label := 2, "2:255", "my-label"
	item := entity.SavedItem{
		ID: "b2222222-0000-0000-0000-000000000001", UserID: userID,
		ItemType: "quran_ayah", SurahID: &surah, AyahKey: &ayahKey, Label: &label,
	}

	s1, created1, err := repo.UpsertSavedItem(ctx, item)
	require.NoError(t, err)
	assert.True(t, created1, "first upsert creates the row")

	// Re-upsert identical payload (fresh id, same natural key) → conflict path, no churn.
	item.ID = "b2222222-0000-0000-0000-000000000002"
	s2, created2, err := repo.UpsertSavedItem(ctx, item)
	require.NoError(t, err)
	assert.False(t, created2, "re-upsert hits the conflict path")
	assert.True(t, s1.UpdatedAt.Equal(s2.UpdatedAt), "no-op re-upsert must NOT bump updated_at")

	// Changing the label bumps updated_at.
	newLabel := "changed-label"
	item.Label = &newLabel
	item.ID = "b2222222-0000-0000-0000-000000000003"
	s3, _, err := repo.UpsertSavedItem(ctx, item)
	require.NoError(t, err)
	assert.True(t, s3.UpdatedAt.After(s2.UpdatedAt), "label change bumps updated_at")

	// PATCH re-sending the SAME label → value-identical, must NOT bump updated_at.
	sameLabel := newLabel
	s4, err := repo.UpdateSavedItem(ctx, userID, s3.ID, entity.SavedItemPatch{LabelSet: true, Label: &sameLabel})
	require.NoError(t, err)
	assert.True(t, s3.UpdatedAt.Equal(s4.UpdatedAt), "value-identical PATCH must NOT bump updated_at")

	// PATCH with a different note → bumps updated_at.
	note := "a fresh note"
	s5, err := repo.UpdateSavedItem(ctx, userID, s3.ID, entity.SavedItemPatch{NoteSet: true, Note: &note})
	require.NoError(t, err)
	assert.True(t, s5.UpdatedAt.After(s4.UpdatedAt), "note change bumps updated_at")
}
