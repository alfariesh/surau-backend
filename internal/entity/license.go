package entity

import "time"

// Platform-wide license_status values, adopted verbatim from the Quran
// vocabulary. Only LicenseStatusPermitted allows a new public publication;
// existing kitab publications may remain visible through the separately
// recorded grandfather policy until an audit marks them restricted.
const (
	LicenseStatusUnknown      = "unknown"
	LicenseStatusNeedsReview  = "needs_review"
	LicenseStatusPermitted    = "permitted"
	LicenseStatusRestricted   = "restricted"
	LicenseStatusPublicDomain = "public_domain"

	LicenseAuditStatusUnresolved = "unresolved"
	LicenseAuditStatusAll        = "all"
)

// licenseStatuses mirrors every platform license CHECK constraint. Keep this
// allow-list in the entity layer so importers, editorial APIs, and publishers
// reject invalid values before PostgreSQL returns an opaque 23514 error.
//
//nolint:gochecknoglobals // immutable allow-list mirroring DB CHECK constraints
var licenseStatuses = map[string]bool{
	LicenseStatusUnknown:      true,
	LicenseStatusNeedsReview:  true,
	LicenseStatusPermitted:    true,
	LicenseStatusRestricted:   true,
	LicenseStatusPublicDomain: true,
}

// IsValidLicenseStatus reports whether s is an allowed platform license_status.
func IsValidLicenseStatus(s string) bool {
	return licenseStatuses[s]
}

// IsValidEditorialLicenseStatus is kept as a compatibility alias for the Quran
// importers that predate the platform-wide license vocabulary.
func IsValidEditorialLicenseStatus(s string) bool {
	return IsValidLicenseStatus(s)
}

// BookLicense is the audited license state of one kitab Edition. In B-4 the
// books row is the Edition boundary and also the temporary Work boundary until
// K-2 introduces the full Work/Edition registry.
type BookLicense struct {
	BookID            int        `json:"book_id" example:"797"`
	BookTitle         string     `json:"book_title"`
	LicenseStatus     string     `json:"license_status" example:"needs_review"`
	Reason            *string    `json:"reason,omitempty"`
	EvidenceURL       *string    `json:"evidence_url,omitempty"`
	UpdatedBy         *string    `json:"updated_by,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at" example:"2026-01-01T00:00:00Z"`
	PublicationStatus *string    `json:"publication_status,omitempty" example:"published"`
	Grandfathered     bool       `json:"grandfathered" example:"true"`
	GrandfatheredAt   *time.Time `json:"grandfathered_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookLicense

// BookLicenseAuditItem adds real usage signals to a license record so the
// unresolved queue is ordered by the works readers rely on most.
type BookLicenseAuditItem struct {
	BookLicense
	RegisteredReaderCount int        `json:"registered_reader_count" example:"120"`
	SavedItemCount        int        `json:"saved_item_count" example:"35"`
	LastActivityAt        *time.Time `json:"last_activity_at,omitempty" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookLicenseAuditItem

// BookLicenseAuditCounts is the complete coverage snapshot, independent of
// pagination and the active queue filter.
type BookLicenseAuditCounts struct {
	Total         int `json:"total" example:"1000"`
	Unresolved    int `json:"unresolved" example:"900"`
	Unknown       int `json:"unknown" example:"850"`
	NeedsReview   int `json:"needs_review" example:"50"`
	Permitted     int `json:"permitted" example:"80"`
	Restricted    int `json:"restricted" example:"10"`
	PublicDomain  int `json:"public_domain" example:"10"`
	Grandfathered int `json:"grandfathered" example:"700"`
} // @name entity.BookLicenseAuditCounts

// BookLicenseAuditReport is the protected coverage report Salman uses to
// watch the unresolved queue shrink over time.
type BookLicenseAuditReport struct {
	Items       []BookLicenseAuditItem `json:"items"`
	Total       int                    `json:"total" example:"900"`
	Counts      BookLicenseAuditCounts `json:"counts"`
	GeneratedAt time.Time              `json:"generated_at" example:"2026-01-01T00:00:00Z"`
} // @name entity.BookLicenseAuditReport

// BookLicenseUpdate is the actor-attributed input persisted atomically with a
// book_license_audits history row.
type BookLicenseUpdate struct {
	BookID        int
	LicenseStatus string
	Reason        string
	EvidenceURL   *string
}
