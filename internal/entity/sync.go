package entity

import "time"

// PersonalSyncSnapshot is one delta payload for offline-first clients.
// With since set, the slices contain rows changed at or after since (with a
// server-side overlap window, so delivery is at-least-once and clients must
// upsert idempotently by key). Without since, it is a full snapshot.
// SavedItemIDs always lists every current saved-item ID so clients can
// reconcile hard deletions.
type PersonalSyncSnapshot struct {
	ServerTime      time.Time              `json:"server_time"     example:"2026-06-12T03:00:00Z"`
	Since           *time.Time             `json:"since,omitempty" example:"2026-06-11T00:00:00Z"`
	ReadingProgress []ReadingProgress      `json:"reading_progress"`
	QuranProgress   []QuranReadingProgress `json:"quran_progress"`
	SavedItems      []SavedItem            `json:"saved_items"`
	SavedItemIDs    []string               `json:"saved_item_ids"`
	KhatamCycles    []QuranKhatamCycle     `json:"khatam_cycles"`
} // @name entity.PersonalSyncSnapshot
