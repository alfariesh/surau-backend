package request

// Register -.
type Register struct {
	DisplayName string `json:"display_name" validate:"omitempty,max=255"`
	Name        string `json:"name"         validate:"omitempty,max=255"`
	Username    string `json:"username"     validate:"omitempty,max=255"`
	Email       string `json:"email"        validate:"required,email"`
	Password    string `json:"password"     validate:"required,min=8,max=72"`
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

// RequestEmailChange -.
type RequestEmailChange struct {
	CurrentPassword string `json:"current_password" validate:"required,min=8,max=72"`
	NewEmail        string `json:"new_email"        validate:"required,email"`
}

// VerifyEmailChange -.
type VerifyEmailChange struct {
	Token string `json:"token" validate:"required"`
}

// DeleteAccount -.
type DeleteAccount struct {
	CurrentPassword string `json:"current_password" validate:"required,min=8,max=72"`
}
