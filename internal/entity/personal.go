package entity

import "time"

const (
	SavedItemTypeBookPage    = "book_page"
	SavedItemTypeBookHeading = "book_heading"
	SavedItemTypeQuranAyah   = "quran_ayah"
	SavedItemTypeQuranRange  = "quran_range"
)

// SavedItem stores one private saved Quran or kitab location.
type SavedItem struct {
	ID             string    `json:"id"               example:"550e8400-e29b-41d4-a716-446655440000"`
	UserID         string    `json:"user_id"          example:"550e8400-e29b-41d4-a716-446655440000"`
	ItemType       string    `json:"item_type"        example:"quran_ayah"`
	BookID         *int      `json:"book_id,omitempty" example:"797"`
	PageID         *int      `json:"page_id,omitempty" example:"12"`
	HeadingID      *int      `json:"heading_id,omitempty" example:"10"`
	SurahID        *int      `json:"surah_id,omitempty" example:"73"`
	AyahKey        *string   `json:"ayah_key,omitempty" example:"73:4"`
	FromAyahNumber *int      `json:"from_ayah_number,omitempty" example:"4"`
	ToAyahNumber   *int      `json:"to_ayah_number,omitempty" example:"6"`
	Label          *string   `json:"label,omitempty"`
	Note           *string   `json:"note,omitempty"`
	Tags           []string  `json:"tags"`
	CreatedAt      time.Time `json:"created_at"       example:"2026-01-01T00:00:00Z"`
	UpdatedAt      time.Time `json:"updated_at"       example:"2026-01-01T00:00:00Z"`
} // @name entity.SavedItem

// SavedItemPatch carries partial saved-item metadata updates. The Set flags
// distinguish absent fields (unchanged) from explicit nulls (cleared).
type SavedItemPatch struct {
	Label    *string
	LabelSet bool
	Note     *string
	NoteSet  bool
	Tags     []string
	TagsSet  bool
}

// QuranReadingProgress stores one private resume position for a Quran surah.
// PageNumber, JuzNumber, and HizbNumber are resolved from the ayah's mushaf
// metadata at read time so clients can resume by page or juz directly.
type QuranReadingProgress struct {
	UserID          string    `json:"user_id"               example:"550e8400-e29b-41d4-a716-446655440000"`
	SurahID         int       `json:"surah_id"              example:"73"`
	AyahNumber      int       `json:"ayah_number"           example:"4"`
	AyahKey         string    `json:"ayah_key"              example:"73:4"`
	PositionPercent float64   `json:"position_percent"      example:"25.00"`
	PageNumber      *int      `json:"page_number,omitempty" example:"574"`
	JuzNumber       *int      `json:"juz_number,omitempty"  example:"29"`
	HizbNumber      *int      `json:"hizb_number,omitempty" example:"57"`
	ObservedAt      time.Time `json:"observed_at"           example:"2026-01-01T00:00:00Z"`
	UpdatedAt       time.Time `json:"updated_at"            example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranReadingProgress
