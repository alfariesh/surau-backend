package persistent

import "github.com/alfariesh/surau-backend/internal/repo"

// Use cases discover these optional capabilities with a runtime type
// assertion, so their unit-test fakes may implement only the core interface.
// These assertions turn the production adapters' support into a compile-time
// fact: if a signature drifts, the build fails instead of the feature silently
// degrading to "not configured" at runtime.
var (
	_ repo.LicenseRepo            = (*EditorialRepo)(nil)
	_ repo.QuranSourceLicenseRepo = (*EditorialRepo)(nil)
	_ repo.QuranEditorialRepo     = (*EditorialRepo)(nil)
	_ repo.QuranCitableUnitRepo   = (*CitableUnitRepo)(nil)
	_ repo.CitableUnitCatalogRepo = (*CitableUnitRepo)(nil)
	_ repo.QuranLocatorAnchorRepo = (*AnchorRepo)(nil)
)
