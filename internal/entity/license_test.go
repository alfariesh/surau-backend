package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlatformLicenseVocabularyIsExact(t *testing.T) {
	t.Parallel()

	valid := []string{
		LicenseStatusUnknown,
		LicenseStatusNeedsReview,
		LicenseStatusPermitted,
		LicenseStatusRestricted,
		LicenseStatusPublicDomain,
	}
	for _, status := range valid {
		assert.True(t, IsValidLicenseStatus(status), status)
		assert.True(t, IsValidEditorialLicenseStatus(status), "Quran compatibility alias: "+status)
	}

	for _, invalid := range []string{"", "copyrighted", "PERMITTED", " permitted "} {
		assert.False(t, IsValidLicenseStatus(invalid), invalid)
	}
}
