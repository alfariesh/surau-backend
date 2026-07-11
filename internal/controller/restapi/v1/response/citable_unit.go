package response

import (
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// EditorialCitableUnit is the protected, provenance-complete Citable Unit
// representation. Public Anchor resolution intentionally remains locator-only.
type EditorialCitableUnit struct {
	ID                     string                     `json:"id"`
	Corpus                 string                     `json:"corpus"`
	BookID                 int                        `json:"book_id"`
	HeadingID              *int                       `json:"heading_id,omitempty"`
	PageID                 *int                       `json:"page_id,omitempty"`
	Kind                   string                     `json:"kind"`
	Ordinal                int                        `json:"ordinal"`
	Position               int                        `json:"position"`
	ParentUnitID           *string                    `json:"parent_unit_id,omitempty"`
	Anchor                 string                     `json:"anchor"`
	Marker                 *string                    `json:"marker,omitempty"`
	Text                   string                     `json:"text"`
	HTML                   *string                    `json:"html,omitempty"`
	TextNormalized         string                     `json:"text_normalized"`
	NormalizationVersion   int                        `json:"normalization_version"`
	Occurrence             int                        `json:"occurrence"`
	Language               string                     `json:"language"`
	ProvenanceClass        string                     `json:"provenance_class"`
	ProvenanceDetail       map[string]any             `json:"provenance_detail,omitempty"`
	Generation             *entity.GenerationIdentity `json:"generation,omitempty"`
	LicenseStatus          *string                    `json:"license_status,omitempty"`
	EffectiveLicenseStatus *string                    `json:"effective_license_status,omitempty"`
	LicenseSource          *string                    `json:"license_source,omitempty"`
	Lifecycle              string                     `json:"lifecycle"`
	RetiredAt              *time.Time                 `json:"retired_at,omitempty"`
	CreatedAt              time.Time                  `json:"created_at"`
	UpdatedAt              time.Time                  `json:"updated_at"`
} // @name v1.EditorialCitableUnit

// EditorialCitableUnitResolution includes active lineage successors for a
// superseded unit, preserving the B-1/B-2 no-dead-anchor contract.
type EditorialCitableUnitResolution struct {
	Unit       EditorialCitableUnit   `json:"unit"`
	Successors []EditorialCitableUnit `json:"successors"`
} // @name v1.EditorialCitableUnitResolution

// NewEditorialCitableUnitResolution maps the internal registry model to its
// stable curation response.
func NewEditorialCitableUnitResolution(value *entity.UnitResolution) EditorialCitableUnitResolution {
	result := EditorialCitableUnitResolution{
		Unit:       editorialCitableUnit(&value.Unit),
		Successors: make([]EditorialCitableUnit, 0, len(value.Successors)),
	}
	for i := range value.Successors {
		result.Successors = append(result.Successors, editorialCitableUnit(&value.Successors[i]))
	}

	return result
}

func editorialCitableUnit(value *entity.CitableUnit) EditorialCitableUnit {
	return EditorialCitableUnit{
		ID:                     value.ID,
		Corpus:                 value.Corpus,
		BookID:                 value.BookID,
		HeadingID:              value.HeadingID,
		PageID:                 value.PageID,
		Kind:                   value.Kind,
		Ordinal:                value.Ordinal,
		Position:               value.Position,
		ParentUnitID:           value.ParentUnitID,
		Anchor:                 value.Anchor,
		Marker:                 value.Marker,
		Text:                   value.Text,
		HTML:                   value.HTML,
		TextNormalized:         value.TextNormalized,
		NormalizationVersion:   value.NormalizationVersion,
		Occurrence:             value.Occurrence,
		Language:               value.Language,
		ProvenanceClass:        value.ProvenanceClass,
		ProvenanceDetail:       value.ProvenanceDetail,
		Generation:             value.Generation,
		LicenseStatus:          value.LicenseStatus,
		EffectiveLicenseStatus: value.EffectiveLicenseStatus,
		LicenseSource:          value.LicenseSource,
		Lifecycle:              value.Lifecycle,
		RetiredAt:              value.RetiredAt,
		CreatedAt:              value.CreatedAt,
		UpdatedAt:              value.UpdatedAt,
	}
}
