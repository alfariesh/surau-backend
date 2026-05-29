package request

// Register -.
type Register struct {
	Username string `json:"username" validate:"required,min=3,max=255"`
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required,min=8,max=72"`
}

// Login -.
type Login struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required,min=8,max=72"`
}

// VerifyEmail -.
type VerifyEmail struct {
	Token string `json:"token" validate:"required"`
}

// ResendVerification -.
type ResendVerification struct {
	Email string `json:"email" validate:"required,email"`
}

// ForgotPassword -.
type ForgotPassword struct {
	Email string `json:"email" validate:"required,email"`
}

// ResetPassword -.
type ResetPassword struct {
	Token    string `json:"token" validate:"required"`
	Password string `json:"password" validate:"required,min=8,max=72"`
}

// ChangePassword -.
type ChangePassword struct {
	CurrentPassword string `json:"current_password" validate:"required,min=8,max=72"`
	NewPassword     string `json:"new_password" validate:"required,min=8,max=72"`
}
