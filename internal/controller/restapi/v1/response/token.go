package response

import (
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
)

// Token carries the access/refresh pair issued at login and refresh. Token
// mirrors AccessToken for clients that predate the refresh-token flow.
type Token struct {
	Token        string `json:"token" example:"eyJhbGciOiJIUzI1NiIs..."`
	AccessToken  string `json:"access_token,omitempty" example:"eyJhbGciOiJIUzI1NiIs..."`
	RefreshToken string `json:"refresh_token,omitempty" example:"3q2-7w8X9yZ0aB1cD2eF3g..."`
	TokenType    string `json:"token_type,omitempty" example:"Bearer"`
	ExpiresIn    int64  `json:"expires_in,omitempty" example:"900"`
	SessionID    string `json:"session_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
} // @name v1.Token

// NewToken builds the token payload from a usecase login result.
func NewToken(result entity.LoginResult) Token {
	expiresIn := int64(time.Until(result.AccessExpiresAt).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}

	return Token{
		Token:        result.AccessToken,
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		TokenType:    "Bearer",
		ExpiresIn:    expiresIn,
		SessionID:    result.SessionID,
	}
}

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

// PasswordChanged includes a fresh token pair: changing the password revokes
// all previous sessions, so the client must switch to these tokens.
type PasswordChanged struct {
	PasswordChanged bool   `json:"password_changed" example:"true"`
	Token           string `json:"token,omitempty" example:"eyJhbGciOiJIUzI1NiIs..."`
	AccessToken     string `json:"access_token,omitempty" example:"eyJhbGciOiJIUzI1NiIs..."`
	RefreshToken    string `json:"refresh_token,omitempty" example:"3q2-7w8X9yZ0aB1cD2eF3g..."`
	TokenType       string `json:"token_type,omitempty" example:"Bearer"`
	ExpiresIn       int64  `json:"expires_in,omitempty" example:"900"`
	SessionID       string `json:"session_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
} // @name v1.PasswordChanged

// EmailChanged includes a fresh token pair: changing the email revokes all
// previous sessions, so the client must switch to these tokens.
type EmailChanged struct {
	EmailChanged bool   `json:"email_changed" example:"true"`
	Token        string `json:"token,omitempty" example:"eyJhbGciOiJIUzI1NiIs..."`
	AccessToken  string `json:"access_token,omitempty" example:"eyJhbGciOiJIUzI1NiIs..."`
	RefreshToken string `json:"refresh_token,omitempty" example:"3q2-7w8X9yZ0aB1cD2eF3g..."`
	TokenType    string `json:"token_type,omitempty" example:"Bearer"`
	ExpiresIn    int64  `json:"expires_in,omitempty" example:"900"`
	SessionID    string `json:"session_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
} // @name v1.EmailChanged

// AccountDeleted -.
type AccountDeleted struct {
	AccountDeleted bool `json:"account_deleted" example:"true"`
} // @name v1.AccountDeleted

// LoggedOut -.
type LoggedOut struct {
	LoggedOut bool `json:"logged_out" example:"true"`
} // @name v1.LoggedOut

// SessionsRevoked -.
type SessionsRevoked struct {
	SessionsRevoked bool `json:"sessions_revoked" example:"true"`
} // @name v1.SessionsRevoked
