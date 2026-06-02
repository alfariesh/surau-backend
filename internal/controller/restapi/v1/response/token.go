package response

// Token -.
type Token struct {
	Token string `json:"token" example:"eyJhbGciOiJIUzI1NiIs..."`
} // @name v1.Token

// EmailVerification -.
type EmailVerification struct {
	EmailVerified bool `json:"email_verified" example:"true"`
} // @name v1.EmailVerification

// Accepted -.
type Accepted struct {
	Accepted bool `json:"accepted" example:"true"`
} // @name v1.Accepted

// PasswordReset -.
type PasswordReset struct {
	PasswordReset bool `json:"password_reset" example:"true"`
} // @name v1.PasswordReset

// PasswordChanged -.
type PasswordChanged struct {
	PasswordChanged bool `json:"password_changed" example:"true"`
} // @name v1.PasswordChanged

// EmailChanged -.
type EmailChanged struct {
	EmailChanged bool `json:"email_changed" example:"true"`
} // @name v1.EmailChanged

// AccountDeleted -.
type AccountDeleted struct {
	AccountDeleted bool `json:"account_deleted" example:"true"`
} // @name v1.AccountDeleted
