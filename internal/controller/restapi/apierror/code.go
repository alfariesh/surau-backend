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

// Code returns the stable machine-readable code for an error message: the
// FROZEN compatibility table first (registry.go — the F1-D contract), then
// the legacy snake_case derivation for messages not yet registered. Register
// new messages in frozenCodes; never repurpose an existing entry.
func Code(msg string) string {
	msg = strings.ToLower(strings.TrimSpace(msg))
	if msg == "" {
		return "error"
	}

	if code, ok := frozenCodes[msg]; ok {
		return code
	}

	return derive(msg)
}

// derive is the legacy fallback: lowercase message → snake_case code. Kept
// ONLY for unregistered (new) messages; the frozen table snapshots what this
// produced for every historical message, so changing this algorithm cannot
// move shipped codes (guarded by registry_test.go).
func derive(msg string) string {
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
