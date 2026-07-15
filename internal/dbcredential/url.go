// Package dbcredential centralizes the explicit A-2 owner-credential overlap.
package dbcredential

import (
	"os"
	"strings"
)

// ImporterURL prefers the dedicated least-privilege login. The old owner URL
// is visible only while the operator deliberately enables the overlap flag.
func ImporterURL() string {
	if dedicated := strings.TrimSpace(os.Getenv("IMPORTER_PG_URL")); dedicated != "" {
		return dedicated
	}

	if !legacyAllowed() {
		return ""
	}

	return strings.TrimSpace(os.Getenv("PG_URL"))
}

func legacyAllowed() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("ALLOW_LEGACY_DB_CREDENTIALS"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
