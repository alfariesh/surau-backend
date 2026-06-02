package request

// SetUserRole -.
type SetUserRole struct {
	Email string `json:"email" validate:"required,email" example:"editor@example.com"`
	Role  string `json:"role"  validate:"required"       example:"editor"`
} // @name v1.SetUserRole
