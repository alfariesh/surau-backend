package entity

// ReadingActivityDay is one daily activity bucket for heatmaps.
type ReadingActivityDay struct {
	Date           string `json:"date"             example:"2026-06-12"`
	QuranAyahsRead int    `json:"quran_ayahs_read" example:"25"`
	KitabPagesRead int    `json:"kitab_pages_read" example:"4"`
	QuranEvents    int    `json:"quran_events"     example:"6"`
	KitabEvents    int    `json:"kitab_events"     example:"2"`
} // @name entity.ReadingActivityDay

// ReadingActivitySummary aggregates a date range of reading activity.
type ReadingActivitySummary struct {
	From            string               `json:"from"              example:"2026-05-14"`
	To              string               `json:"to"                example:"2026-06-12"`
	ActiveDays      int                  `json:"active_days"       example:"12"`
	QuranAyahsRead  int                  `json:"quran_ayahs_read"  example:"230"`
	KitabPagesRead  int                  `json:"kitab_pages_read"  example:"45"`
	QuranActiveDays int                  `json:"quran_active_days" example:"10"`
	KitabActiveDays int                  `json:"kitab_active_days" example:"5"`
	Days            []ReadingActivityDay `json:"days"`
} // @name entity.ReadingActivitySummary

// ReadingStreak reports consecutive-day reading streaks. Today is the
// client's local date; the streak counts runs ending today or yesterday.
type ReadingStreak struct {
	CurrentStreakDays int     `json:"current_streak_days" example:"5"`
	LongestStreakDays int     `json:"longest_streak_days" example:"12"`
	TotalActiveDays   int     `json:"total_active_days"   example:"40"`
	LastActiveDate    *string `json:"last_active_date,omitempty" example:"2026-06-12"`
	Today             string  `json:"today"               example:"2026-06-12"`
	ActiveToday       bool    `json:"active_today"        example:"true"`
} // @name entity.ReadingStreak
