package importer

import (
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveEditorialLicense pins the linchpin of the un-publish fix: override is
// true ONLY for an explicit, valid, non-needs_review status. The enrichment skill
// always emits needs_review (or omits the field), so a re-import never sets
// override → a human's reviewed 'permitted' survives every re-run.
func TestResolveEditorialLicense(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		raw          *string
		wantStatus   string
		wantOverride bool
		wantErr      bool
	}{
		{name: "absent defaults to needs_review, no override", raw: nil, wantStatus: entity.LicenseStatusNeedsReview},
		{name: "empty string defaults to needs_review", raw: new(""), wantStatus: entity.LicenseStatusNeedsReview},
		{name: "whitespace defaults to needs_review", raw: new("   "), wantStatus: entity.LicenseStatusNeedsReview},
		{name: "explicit needs_review is NOT an override", raw: new("needs_review"), wantStatus: entity.LicenseStatusNeedsReview},
		{name: "permitted overrides", raw: new("permitted"), wantStatus: entity.LicenseStatusPermitted, wantOverride: true},
		{name: "public_domain overrides", raw: new("public_domain"), wantStatus: entity.LicenseStatusPublicDomain, wantOverride: true},
		{name: "restricted overrides", raw: new("restricted"), wantStatus: entity.LicenseStatusRestricted, wantOverride: true},
		{name: "unknown overrides", raw: new("unknown"), wantStatus: entity.LicenseStatusUnknown, wantOverride: true},
		{name: "trims surrounding whitespace", raw: new("  permitted  "), wantStatus: entity.LicenseStatusPermitted, wantOverride: true},
		{name: "invalid value errors", raw: new("published"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, override, err := resolveEditorialLicense(tt.raw)
			if tt.wantErr {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, status)
			assert.Equal(t, tt.wantOverride, override)
		})
	}
}
