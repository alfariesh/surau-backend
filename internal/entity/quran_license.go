package entity

// Quran content license_status values, mirroring the *_license_status_check DB
// constraints on quran_surah_editorial / quran_ayah_editorial / quran_*_sources.
// Only LicenseStatusPermitted content is served to the public API.
const (
	LicenseStatusUnknown      = "unknown"
	LicenseStatusNeedsReview  = "needs_review"
	LicenseStatusPermitted    = "permitted"
	LicenseStatusRestricted   = "restricted"
	LicenseStatusPublicDomain = "public_domain"
)

// editorialLicenseStatuses is the allow-list shared by the editorial importers,
// mirroring the *_license_status_check DB constraints so a bad value fails with a
// clear message instead of an opaque 23514.
//
//nolint:gochecknoglobals // immutable allow-list mirroring the DB CHECK constraint
var editorialLicenseStatuses = map[string]bool{
	LicenseStatusUnknown:      true,
	LicenseStatusNeedsReview:  true,
	LicenseStatusPermitted:    true,
	LicenseStatusRestricted:   true,
	LicenseStatusPublicDomain: true,
}

// IsValidEditorialLicenseStatus reports whether s is an allowed license_status.
func IsValidEditorialLicenseStatus(s string) bool {
	return editorialLicenseStatuses[s]
}
