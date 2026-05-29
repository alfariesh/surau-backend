package request

// Register -.
type Register struct {
	Username string `json:"username" validate:"required,min=3,max=255" example:"johndoe"`
	Email    string `json:"email"    validate:"required,email"         example:"john@example.com"`
	Password string `json:"password" validate:"required,min=8,max=72"  example:"secret123"`
} // @name v1.Register

// Login -.
type Login struct {
	Email    string `json:"email"    validate:"required,email"        example:"john@example.com"`
	Password string `json:"password" validate:"required,min=8,max=72" example:"secret123"`
} // @name v1.Login

// VerifyEmail -.
type VerifyEmail struct {
	Token string `json:"token" validate:"required" example:"G66NnGZnWg4W88qGz3p9N0-GxjKuxEOHHsvWv3kBaBA"`
} // @name v1.VerifyEmail

// ResendVerification -.
type ResendVerification struct {
	Email string `json:"email" validate:"required,email" example:"john@example.com"`
} // @name v1.ResendVerification

// ForgotPassword -.
type ForgotPassword struct {
	Email string `json:"email" validate:"required,email" example:"john@example.com"`
} // @name v1.ForgotPassword

// ResetPassword -.
type ResetPassword struct {
	Token    string `json:"token"    validate:"required"     example:"G66NnGZnWg4W88qGz3p9N0-GxjKuxEOHHsvWv3kBaBA"`
	Password string `json:"password" validate:"required,min=8,max=72" example:"newsecret123"`
} // @name v1.ResetPassword

// ChangePassword -.
type ChangePassword struct {
	CurrentPassword string `json:"current_password" validate:"required,min=8,max=72" example:"oldsecret123"`
	NewPassword     string `json:"new_password"     validate:"required,min=8,max=72" example:"newsecret123"`
} // @name v1.ChangePassword
