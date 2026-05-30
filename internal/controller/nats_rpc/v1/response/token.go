package response

// Token -.
type Token struct {
	Token string `json:"token"`
}

// EmailVerification -.
type EmailVerification struct {
	EmailVerified bool `json:"email_verified"`
}

// Accepted -.
type Accepted struct {
	Accepted bool `json:"accepted"`
}

// PasswordReset -.
type PasswordReset struct {
	PasswordReset bool `json:"password_reset"`
}

// PasswordChanged -.
type PasswordChanged struct {
	PasswordChanged bool `json:"password_changed"`
}

// EmailChanged -.
type EmailChanged struct {
	EmailChanged bool `json:"email_changed"`
}

// AccountDeleted -.
type AccountDeleted struct {
	AccountDeleted bool `json:"account_deleted"`
}
