package entity

import "time"

// KhatamJuzTotal is the number of juz needed to complete one khatam cycle.
const KhatamJuzTotal = 30

// QuranKhatamCycle is one Quran completion cycle tracked by juz-level marks.
// A user has at most one active (uncompleted) cycle at a time.
type QuranKhatamCycle struct {
	ID           string     `json:"id"                     example:"550e8400-e29b-41d4-a716-446655440000"`
	UserID       string     `json:"user_id"                example:"550e8400-e29b-41d4-a716-446655440000"`
	StartedAt    time.Time  `json:"started_at"             example:"2026-01-01T00:00:00Z"`
	CompletedAt  *time.Time `json:"completed_at,omitempty" example:"2026-02-01T00:00:00Z"`
	Notes        *string    `json:"notes,omitempty"        example:"Khatam Ramadhan"`
	CompletedJuz []int      `json:"completed_juz"`
	JuzCount     int        `json:"juz_count"              example:"17"`
	Percent      float64    `json:"percent"                example:"56.67"`
	CreatedAt    time.Time  `json:"created_at"             example:"2026-01-01T00:00:00Z"`
	UpdatedAt    time.Time  `json:"updated_at"             example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranKhatamCycle
