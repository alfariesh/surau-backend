package response

import (
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// MFAChallenge is the login response when a second factor is required: no
// tokens yet, just the short-lived challenge to present to /auth/mfa/verify.
type MFAChallenge struct {
	MFARequired bool   `json:"mfa_required" example:"true"`
	MFAToken    string `json:"mfa_token"    example:"pXW1..."`
	ExpiresIn   int64  `json:"expires_in"   example:"300"`
} // @name v1.MFAChallenge

// NewMFAChallenge builds the challenge body from a diverted login result.
func NewMFAChallenge(result *entity.LoginResult) MFAChallenge {
	expiresIn := max(int64(time.Until(result.MFATokenExpiresAt).Seconds()), 0)

	return MFAChallenge{MFARequired: true, MFAToken: result.MFAToken, ExpiresIn: expiresIn}
}

// MFAEnrollment carries the provisioning material (rendered as a QR by FE).
type MFAEnrollment struct {
	Secret     string `json:"secret"      example:"JBSWY3DPEHPK3PXP"`
	OTPAuthURL string `json:"otpauth_url" example:"otpauth://totp/Surau:user@example.com?..."`
} // @name v1.MFAEnrollment

// MFARecoveryCodes returns the one-time recovery codes — shown exactly once.
type MFARecoveryCodes struct {
	RecoveryCodes []string `json:"recovery_codes" example:"AAAA-BBBB-CCCC-DDDD"`
} // @name v1.MFARecoveryCodes

// MFAStepUp reports a successful step-up and its freshness deadline.
type MFAStepUp struct {
	SteppedUp bool      `json:"stepped_up" example:"true"`
	ExpiresAt time.Time `json:"expires_at" example:"2026-01-01T00:10:00Z"`
} // @name v1.MFAStepUp

// MFAStatus is the FE-facing MFA state for the settings screen.
type MFAStatus struct {
	Enabled                bool       `json:"enabled"                     example:"true"`
	Pending                bool       `json:"pending"                     example:"false"`
	Required               bool       `json:"required"                    example:"true"`
	EnforcedFrom           *time.Time `json:"enforced_from,omitempty"     example:"2026-01-01T00:00:00Z"`
	GraceEndsAt            *time.Time `json:"grace_ends_at,omitempty"     example:"2026-01-08T00:00:00Z"`
	StepUpVerifiedAt       *time.Time `json:"step_up_verified_at,omitempty"`
	StepUpExpiresAt        *time.Time `json:"step_up_expires_at,omitempty"`
	RecoveryCodesRemaining int        `json:"recovery_codes_remaining"    example:"10"`
} // @name v1.MFAStatus

// NewMFAStatus maps the entity status.
func NewMFAStatus(status *entity.MFAStatus) MFAStatus {
	return MFAStatus{
		Enabled:                status.Enabled,
		Pending:                status.Pending,
		Required:               status.Required,
		EnforcedFrom:           status.EnforcedFrom,
		GraceEndsAt:            status.GraceEndsAt,
		StepUpVerifiedAt:       status.StepUpVerifiedAt,
		StepUpExpiresAt:        status.StepUpExpiresAt,
		RecoveryCodesRemaining: status.RecoveryCodesRemaining,
	}
}

// MFAResetChallenge acknowledges a reset request (OTP emailed).
type MFAResetChallenge struct {
	ResetToken string `json:"reset_token" example:"q7Zr..."`
	ExpiresIn  int64  `json:"expires_in"  example:"900"`
} // @name v1.MFAResetChallenge

// MFAResetDone acknowledges a completed reset (MFA removed, sessions revoked).
type MFAResetDone struct {
	MFAReset bool `json:"mfa_reset" example:"true"`
} // @name v1.MFAResetDone
