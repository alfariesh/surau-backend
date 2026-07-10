package entity

import "time"

const (
	AnchorFormCanonical     = "canonical"
	AnchorFormLegacyAyahKey = "legacy_ayah_key"
	AnchorFormLegacyTOC     = "legacy_toc"
	AnchorFormLegacyPage    = "legacy_page"

	AnchorBoundaryPoint = "point"
	AnchorBoundaryStart = "start"
	AnchorBoundaryEnd   = "end"

	AnchorTargetCitableUnit = "citable_unit"
	AnchorTargetQuranAyah   = "quran_ayah"
	AnchorTargetBook        = "book"
	AnchorTargetBookHeading = "book_heading"
	AnchorTargetBookPage    = "book_page"

	AnchorRedirectLegacyAlias = "legacy_alias"
	AnchorRedirectLegacyPage  = "legacy_page"
)

// AnchorRecord is the compact persistence read model used by the resolver.
// It deliberately excludes display text/HTML so Anchor lookup cannot become
// a parallel content-read path or leak unpublished content.
type AnchorRecord struct {
	TargetType      string
	Corpus          string
	CanonicalAnchor *string
	UnitID          *string
	BookID          *int
	HeadingID       *int
	PageID          *int
	AyahKey         *string
	Lifecycle       string
	UpdatedAt       time.Time
}

// AnchorRedirect is one edge in a redirect graph. Although the public field
// remains redirect_chain for the roadmap contract, the list may branch when
// one predecessor is split into multiple successor units.
type AnchorRedirect struct {
	From   string `json:"from"   example:"kitab/797/h/11/u/42"`
	To     string `json:"to"     example:"kitab/797/h/11/u/43"`
	Reason string `json:"reason" example:"edit"`
	Depth  int    `json:"depth"  example:"1"`
} // @name entity.AnchorRedirect

// AnchorLookupResult is returned by persistence for one logical boundary.
// ActiveRecords are already visibility-filtered and deterministically sorted.
type AnchorLookupResult struct {
	CanonicalAnchor *string
	Status          string
	ActiveRecords   []AnchorRecord
	RedirectChain   []AnchorRedirect
	CycleDetected   bool
}

// AnchorRequested echoes the accepted input form without inventing a
// canonical Anchor for the physical legacy page locator.
type AnchorRequested struct {
	Form   string `json:"form"             example:"legacy_toc"`
	Anchor string `json:"anchor,omitempty" example:"toc-11"`
	BookID *int   `json:"book_id,omitempty" example:"797"`
	PageID *int   `json:"page_id,omitempty" example:"12"`
} // @name entity.AnchorRequested

// AnchorTarget is a compact active destination. NavigationURL intentionally
// points at an existing reader API; exact paragraph identity remains in
// canonical_anchor/unit_id until frontends add unit-level DOM deep links.
type AnchorTarget struct {
	TargetType      string    `json:"target_type"                example:"citable_unit"`
	Corpus          string    `json:"corpus"                     example:"kitab"`
	CanonicalAnchor *string   `json:"canonical_anchor,omitempty" example:"kitab/797/h/11/u/42"`
	UnitID          *string   `json:"unit_id,omitempty"          example:"550e8400-e29b-41d4-a716-446655440000"`
	BookID          *int      `json:"book_id,omitempty"          example:"797"`
	HeadingID       *int      `json:"heading_id,omitempty"       example:"11"`
	PageID          *int      `json:"page_id,omitempty"          example:"12"`
	AyahKey         *string   `json:"ayah_key,omitempty"         example:"73:4"`
	NavigationURL   string    `json:"navigation_url"             example:"/v1/books/797/toc/11/read"`
	UpdatedAt       time.Time `json:"updated_at"                 example:"2026-07-10T12:00:00Z"`
} // @name entity.AnchorTarget

// AnchorBoundary resolves either a point, or one edge of an inclusive range.
// Ranges resolve boundaries only; they never expand an unbounded content set.
type AnchorBoundary struct {
	Role            string           `json:"role"                       example:"point"`
	CanonicalAnchor *string          `json:"canonical_anchor"           example:"quran/73:4" extensions:"x-nullable"`
	Status          string           `json:"status"                     example:"active"`
	ActiveTargets   []AnchorTarget   `json:"active_targets"`
	RedirectChain   []AnchorRedirect `json:"redirect_chain"`
} // @name entity.AnchorBoundary

// AnchorResolution is the public B-2 capability result.
type AnchorResolution struct {
	Requested       AnchorRequested  `json:"requested"`
	CanonicalAnchor *string          `json:"canonical_anchor" example:"quran/73:4" extensions:"x-nullable"`
	Boundaries      []AnchorBoundary `json:"boundaries"`
} // @name entity.AnchorResolution
