package importer

import (
	"fmt"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
)

// resolveEditorialLicense decides the license_status to write and whether it is an
// explicit override, shared by the surah and ayah editorial importers.
//
// An absent/empty status defaults to needs_review (used on INSERT only). override
// is true ONLY for an explicit, valid, non-needs_review status. Because the
// enrichment skill always emits needs_review, a re-import never overrides — so a
// human's reviewed 'permitted' survives every re-run. A genuine downgrade to
// needs_review is intentionally not expressible via the importer (use admin),
// preventing the un-publish footgun.
func resolveEditorialLicense(raw *string) (status string, override bool, err error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return entity.LicenseStatusNeedsReview, false, nil
	}
	s := strings.TrimSpace(*raw)
	if !entity.IsValidEditorialLicenseStatus(s) {
		return "", false, fmt.Errorf(
			"invalid license_status %q (expected unknown, needs_review, permitted, restricted, or public_domain)", s,
		)
	}
	return s, s != entity.LicenseStatusNeedsReview, nil
}
