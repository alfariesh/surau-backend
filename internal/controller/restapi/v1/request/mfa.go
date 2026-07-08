package request

// MFAVerify completes an MFA login: the challenge token from the login
// response plus a 6-digit TOTP code or a recovery code.
type MFAVerify struct {
	MFAToken string `json:"mfa_token" validate:"required,min=16,max=512" example:"pXW1..."`
	Code     string `json:"code"      validate:"required,min=6,max=32"   example:"123456"`
} // @name v1.MFAVerify

// MFACode carries one second-factor code (TOTP or recovery).
type MFACode struct {
	Code string `json:"code" validate:"required,min=6,max=32" example:"123456"`
} // @name v1.MFACode

// MFAResetRequest starts the lost-device reset from the login-challenge state.
type MFAResetRequest struct {
	MFAToken string `json:"mfa_token" validate:"required,min=16,max=512" example:"pXW1..."`
} // @name v1.MFAResetRequest

// MFAResetConfirm removes MFA with the emailed OTP + one recovery code.
type MFAResetConfirm struct {
	ResetToken   string `json:"reset_token"   validate:"required,min=16,max=512" example:"q7Zr..."`
	OTP          string `json:"otp"           validate:"required,len=6"          example:"123456"`
	RecoveryCode string `json:"recovery_code" validate:"required,min=16,max=32"  example:"AAAA-BBBB-CCCC-DDDD"`
} // @name v1.MFAResetConfirm
