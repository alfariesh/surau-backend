package apierror

import (
	"strings"
	"unicode"
)

const (
	CodeAuthHeaderMissing   = "AUTH_HEADER_MISSING"
	CodeAuthHeaderInvalid   = "AUTH_HEADER_INVALID"
	CodeAuthTokenInvalid    = "AUTH_TOKEN_INVALID"
	CodeAuthUnauthorized    = "AUTH_UNAUTHORIZED"
	CodeAuthCredentials     = "AUTH_INVALID_CREDENTIALS"
	CodeAuthEmailUnverified = "AUTH_EMAIL_NOT_VERIFIED"
	CodeAuthRateLimited     = "AUTH_RATE_LIMITED"
)

var messageCodes = map[string]string{
	"missing authorization header":        CodeAuthHeaderMissing,
	"invalid authorization header format": CodeAuthHeaderInvalid,
	"invalid or expired token":            CodeAuthTokenInvalid,
	"unauthorized":                        CodeAuthUnauthorized,
	"invalid credentials":                 CodeAuthCredentials,
	"email not verified":                  CodeAuthEmailUnverified,
	"too many auth attempts":              CodeAuthRateLimited,
}

// Code returns a stable machine-readable code while preserving legacy
// snake_case fallback codes for messages without an explicit mapping.
func Code(msg string) string {
	msg = strings.ToLower(strings.TrimSpace(msg))
	if msg == "" {
		return "error"
	}
	if code, ok := messageCodes[msg]; ok {
		return code
	}

	var out strings.Builder

	lastUnderscore := false

	for _, r := range msg {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)

			lastUnderscore = false

			continue
		}

		if !lastUnderscore {
			out.WriteByte('_')

			lastUnderscore = true
		}
	}

	code := strings.Trim(out.String(), "_")
	if code == "" {
		return "error"
	}

	return code
}
