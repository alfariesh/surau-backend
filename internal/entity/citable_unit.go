package entity

import "time"

// Citable Unit registry vocabulary (roadmap/phase-1b-content-backbone.md C1/C2),
// mirroring the CHECK constraints on citable_units / citable_unit_lineage.
const (
	UnitCorpusKitab  = "kitab"
	UnitCorpusQuran  = "quran"
	UnitCorpusHadith = "hadith"
	UnitCorpusWiki   = "wiki"

	UnitKindParagraph  = "paragraph"
	UnitKindHeading    = "heading"
	UnitKindQuranQuote = "quran_quote"
	UnitKindFootnote   = "footnote"
	UnitKindHTML       = "html"

	UnitLifecycleActive     = "active"
	UnitLifecycleSuperseded = "superseded"
	UnitLifecycleTombstoned = "tombstoned"

	ProvenanceClassSource    = "source"
	ProvenanceClassEditorial = "editorial"
	ProvenanceClassMachine   = "machine"

	UnitLineageReasonEdit        = "edit"
	UnitLineageReasonContentMove = "content_move"

	// Footnote parent linkage methods recorded in provenance_detail.
	FootnoteLinkMarker   = "marker"
	FootnoteLinkFallback = "fallback"
	FootnoteLinkUnlinked = "unlinked"
)

// CitableUnit is one registry row: a paragraph-granularity unit with stable
// identity, lifecycle, provenance, and license slots. Corpus tables remain the
// source of truth for display text (B-D1).
type CitableUnit struct {
	ID                     string
	Corpus                 string
	BookID                 int
	HeadingID              *int // nil = front-matter before the first heading anchor
	PageID                 *int // physical locator, secondary metadata (B-D2)
	Kind                   string
	Ordinal                int // minted once per scope, never recycled; part of the anchor
	Position               int // current display index within the scope; mutable
	ParentUnitID           *string
	Anchor                 string
	Marker                 *string
	Text                   string
	HTML                   *string
	TextNormalized         string
	NormalizationVersion   int
	ContentHash            []byte
	Occurrence             int
	Language               string
	ProvenanceClass        string
	ProvenanceDetail       map[string]any
	GenerationRunID        *string
	Generation             *GenerationIdentity
	LicenseStatus          *string // nil = inherit from the Edition/Work boundary (B-4)
	EffectiveLicenseStatus *string
	LicenseSource          *string // edition or unit_override
	Lifecycle              string
	RetiredAt              *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// CitableUnitLineage is one supersede edge (B-D3): a retired predecessor points
// at the successor that replaced it, so old anchors always resolve.
type CitableUnitLineage struct {
	PredecessorID string
	SuccessorID   string
	Reason        string
	CreatedAt     time.Time
}

// UnitResolution is the internal resolve result: the requested unit plus the
// lineage walk to the currently active successor(s), if any. The full anchor
// resolution capability (legacy forms, endpoint) is B-2; B-1 only guarantees
// the chain is walkable.
type UnitResolution struct {
	Unit       CitableUnit
	Successors []CitableUnit // active units reached by walking lineage; empty for active/tombstoned
}

// BookUnitSourcePage is one effective page fed to the kitab deriver:
// COALESCE(published page edit, imported source), with edit attribution.
type BookUnitSourcePage struct {
	PageID           int
	ContentHTML      string
	HasPublishedEdit bool
	EditActorID      string
}

// BookUnitSourceHeading is the heading skeleton used for scope segmentation.
type BookUnitSourceHeading struct {
	HeadingID int
	PageID    int
}

// BookUnitSource is everything the kitab deriver needs for one book, loaded at
// LoadedAt (stored as units_derived_at so later edits are never masked).
type BookUnitSource struct {
	BookID     int
	ReleaseKey string
	Pages      []BookUnitSourcePage
	Headings   []BookUnitSourceHeading
	LoadedAt   time.Time
}

// UnitReconcileReport summarizes one reconcile run over a book. A re-run on an
// unchanged source must report zero Minted/Superseded/Tombstoned/Updated
// (AC-1 determinism evidence).
type UnitReconcileReport struct {
	BookID     int
	Scopes     int
	Derived    int
	Matched    int
	Minted     int
	Updated    int // matched units whose position/page/parent moved
	Superseded int
	Tombstoned int
	EditEdges  int
	MoveEdges  int
	HTMLUnits  int // coarse fallback units (parser-quality signal for Fase 4)
	CappedGaps int // gaps that exceeded the M:N lineage cap
	Attempts   int // reconcile attempts (>1 = optimistic conflict retries)
}

// CitableAuditViolations are registry invariant breaches; any nonzero value
// fires the surau_citable_audit_violations alert (AC-3 "audit menggantung = 0").
type CitableAuditViolations struct {
	BookGone              int64 // active units on missing/is_deleted books
	SupersededNoSuccessor int64
	ActiveWithSuccessor   int64 // lineage edge whose predecessor is still active
	LineageCycle          int64 // directed cycle reachable in the redirect graph
	HashMismatch          int64 // recomputed content hash / normalized text drift
	AnchorMalformed       int64
	FootnoteParent        int64 // active footnote with missing/non-active parent
}

// CitableAuditInfo are dashboard-only observations (no alert): normal
// operational states and pre-registry legacy dangling owned by B-3.
type CitableAuditInfo struct {
	StaleBooks                 int64 // units_derived_at older than latest source change
	LegacyQuranBookReferences  int64 // rows pointing at is_deleted pages/headings
	LegacyKnowledgeMentions    int64
	LegacyKnowledgeSourceSpans int64
	LegacyKnowledgeRejections  int64
}

// CitableAuditReport is one scheduled audit pass over the registry.
type CitableAuditReport struct {
	Violations       CitableAuditViolations
	Info             CitableAuditInfo
	UnitsByLifecycle map[string]int64
	RanAt            time.Time
}

// UnitRegistryFingerprint is the optimistic-concurrency stamp for one book's
// registry slice: a reconcile plan built on a stale fingerprint is rejected at
// apply time and replanned.
type UnitRegistryFingerprint struct {
	ActiveCount  int64
	MaxOrdinal   int64 // across ALL lifecycles
	MaxUpdatedAt time.Time
}

// UnitRegistrySnapshot is the registry read model the planner works from.
type UnitRegistrySnapshot struct {
	Active            []CitableUnit // active units of the book, full rows
	MaxOrdinalByScope map[int]int   // COALESCE(heading_id,0) → max ordinal, any lifecycle
	ExistingIDs       map[string]struct{}
	Fingerprint       UnitRegistryFingerprint
}

// UnitPlanUpdate mutates locator metadata of a matched unit; identity fields
// never change. FootnoteLink, when set, refreshes provenance_detail.footnote_link
// for a footnote whose parent linkage changed (e.g. its body was deleted so it
// became unlinked) — otherwise the label would go stale and the audit's
// footnote_parent check would false-positive.
type UnitPlanUpdate struct {
	ID           string
	Position     int
	PageID       *int
	ParentUnitID *string
	FootnoteLink *string
}

// UnitPlanRetire retires one unit (lifecycle superseded or tombstoned).
type UnitPlanRetire struct {
	ID        string
	Lifecycle string
}

// UnitReconcilePlan is the full write set for one book, applied atomically by
// the registry repo inside the guarded transaction.
type UnitReconcilePlan struct {
	BookID         int
	LoadedAt       time.Time
	BasedOn        UnitRegistryFingerprint
	Mints          []CitableUnit
	Updates        []UnitPlanUpdate
	Retires        []UnitPlanRetire
	Edges          []CitableUnitLineage
	ExpectedActive int64
	Report         UnitReconcileReport
}
