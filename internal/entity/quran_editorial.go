package entity

import "time"

const (
	QuranEditorialAssetSurah = "surah"
	QuranEditorialAssetAyah  = "ayah"
)

// QuranSurahEditorialEdit is the protected workflow representation of one
// language-specific surah editorial row. Public Quran responses deliberately
// keep using QuranSurahEditorial so workflow fields never leak there.
type QuranSurahEditorialEdit struct {
	SurahID         int        `json:"surah_id" example:"73"`
	Lang            string     `json:"lang" example:"id"`
	Status          string     `json:"status" example:"draft"`
	MetaTitle       *string    `json:"meta_title,omitempty"`
	MetaDescription *string    `json:"meta_description,omitempty"`
	ArtiNama        *string    `json:"arti_nama,omitempty"`
	Keutamaan       *string    `json:"keutamaan_html,omitempty"`
	AsbabunNuzul    *string    `json:"asbabun_nuzul_html,omitempty"`
	PokokKandungan  *string    `json:"pokok_kandungan_html,omitempty"`
	AuthorName      *string    `json:"author_name,omitempty"`
	ReviewedBy      *string    `json:"reviewed_by,omitempty"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	LicenseStatus   string     `json:"license_status" example:"needs_review"`
	Checksum        *string    `json:"checksum,omitempty"`
	Metadata        RawJSON    `json:"metadata,omitempty" swaggertype:"object"`
	UpdatedBy       *string    `json:"updated_by,omitempty"`
	CreatedAt       time.Time  `json:"created_at" example:"2026-01-01T00:00:00Z"`
	UpdatedAt       time.Time  `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	PublishedAt     *time.Time `json:"published_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranSurahEditorialEdit

// QuranAyahEditorialEdit is the protected workflow representation of one
// language-specific ayah editorial row.
type QuranAyahEditorialEdit struct {
	SurahID         int                     `json:"surah_id" example:"2"`
	AyahNumber      int                     `json:"ayah_number" example:"255"`
	AyahKey         string                  `json:"ayah_key" example:"2:255"`
	Lang            string                  `json:"lang" example:"id"`
	Status          string                  `json:"status" example:"draft"`
	MetaTitle       *string                 `json:"meta_title,omitempty"`
	MetaDescription *string                 `json:"meta_description,omitempty"`
	Intisari        *string                 `json:"intisari_html,omitempty"`
	Keutamaan       *string                 `json:"keutamaan_html,omitempty"`
	FAQ             []QuranAyahEditorialFAQ `json:"faq"`
	TafsirRange     *string                 `json:"tafsir_range,omitempty" example:"255"`
	AuthorName      *string                 `json:"author_name,omitempty"`
	ReviewedBy      *string                 `json:"reviewed_by,omitempty"`
	ReviewedAt      *time.Time              `json:"reviewed_at,omitempty" example:"2026-01-01T00:00:00Z"`
	LicenseStatus   string                  `json:"license_status" example:"needs_review"`
	Checksum        *string                 `json:"checksum,omitempty"`
	Metadata        RawJSON                 `json:"metadata,omitempty" swaggertype:"object"`
	UpdatedBy       *string                 `json:"updated_by,omitempty"`
	CreatedAt       time.Time               `json:"created_at" example:"2026-01-01T00:00:00Z"`
	UpdatedAt       time.Time               `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	PublishedAt     *time.Time              `json:"published_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranAyahEditorialEdit

// QuranSurahEditorialWorkspace exposes draft and published states together to
// protected editors. At least one side exists for a successful GET.
type QuranSurahEditorialWorkspace struct {
	Draft     *QuranSurahEditorialEdit `json:"draft"`
	Published *QuranSurahEditorialEdit `json:"published"`
} // @name entity.QuranSurahEditorialWorkspace

// QuranAyahEditorialWorkspace exposes draft and published states together to
// protected editors.
type QuranAyahEditorialWorkspace struct {
	Draft     *QuranAyahEditorialEdit `json:"draft"`
	Published *QuranAyahEditorialEdit `json:"published"`
} // @name entity.QuranAyahEditorialWorkspace

// CurrentUpdatedAt is the optimistic-lock token source for a surah workspace.
func (w QuranSurahEditorialWorkspace) CurrentUpdatedAt() time.Time {
	if w.Draft != nil {
		return w.Draft.UpdatedAt
	}

	if w.Published != nil {
		return w.Published.UpdatedAt
	}

	return time.Time{}
}

// CurrentUpdatedAt is the optimistic-lock token source for an ayah workspace.
func (w QuranAyahEditorialWorkspace) CurrentUpdatedAt() time.Time {
	if w.Draft != nil {
		return w.Draft.UpdatedAt
	}

	if w.Published != nil {
		return w.Published.UpdatedAt
	}

	return time.Time{}
}

// QuranEditorialRevision is one immutable workflow snapshot. A restore always
// replays the snapshot into draft and appends another revision.
type QuranEditorialRevision struct {
	ID         string    `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	AssetType  string    `json:"asset_type" example:"ayah"`
	SurahID    int       `json:"surah_id" example:"2"`
	AyahNumber *int      `json:"ayah_number,omitempty" example:"255"`
	AyahKey    *string   `json:"ayah_key,omitempty" example:"2:255"`
	Lang       string    `json:"lang" example:"id"`
	Version    int       `json:"version" example:"3"`
	Status     string    `json:"status" example:"draft"`
	ActorID    *string   `json:"actor_id,omitempty"`
	Origin     string    `json:"origin" example:"rest"`
	Snapshot   RawJSON   `json:"snapshot" swaggertype:"object"`
	CreatedAt  time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.QuranEditorialRevision
