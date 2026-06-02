package jwt_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	suraujwt "github.com/evrone/go-clean-template/pkg/jwt"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func newTestManager(duration time.Duration) *suraujwt.Manager {
	return suraujwt.New(testSecret, duration, suraujwt.DefaultIssuer, suraujwt.DefaultAudience)
}

func signRegisteredClaims(t *testing.T, claims jwtlib.RegisteredClaims, method jwtlib.SigningMethod) string {
	t.Helper()

	token := jwtlib.NewWithClaims(method, claims)
	tokenString, err := token.SignedString([]byte(testSecret))
	require.NoError(t, err)

	return tokenString
}

func baseClaims() jwtlib.RegisteredClaims {
	now := time.Now().UTC()

	return jwtlib.RegisteredClaims{
		Subject:   "user-123",
		ExpiresAt: jwtlib.NewNumericDate(now.Add(time.Hour)),
		IssuedAt:  jwtlib.NewNumericDate(now),
		ID:        "token-id",
		Issuer:    suraujwt.DefaultIssuer,
		Audience:  jwtlib.ClaimStrings{suraujwt.DefaultAudience},
	}
}

func TestJWT_GenerateAndParse(t *testing.T) {
	t.Parallel()

	j := newTestManager(time.Hour)

	token, err := j.GenerateToken("user-123", 7)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	userID, err := j.ParseToken(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", userID)

	claims, err := j.ParseTokenClaims(token)
	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID)
	assert.Equal(t, int64(7), claims.TokenVersion)
}

func TestJWT_ParseTokenClaims_MissingTokenVersionDefaultsToZero(t *testing.T) {
	t.Parallel()

	token := signRegisteredClaims(t, baseClaims(), jwtlib.SigningMethodHS256)

	claims, err := newTestManager(time.Hour).ParseTokenClaims(token)

	require.NoError(t, err)
	assert.Equal(t, "user-123", claims.UserID)
	assert.Zero(t, claims.TokenVersion)
}

func TestJWT_GenerateToken_EmptySubject(t *testing.T) {
	t.Parallel()

	j := newTestManager(time.Hour)

	_, err := j.GenerateToken(" ")
	require.ErrorIs(t, err, suraujwt.ErrEmptySubject)
}

func TestJWT_ParseToken_Invalid(t *testing.T) {
	t.Parallel()

	j := newTestManager(time.Hour)

	_, err := j.ParseToken("invalid-token")
	require.Error(t, err)
}

func TestJWT_ParseToken_WrongSecret(t *testing.T) {
	t.Parallel()

	j1 := suraujwt.New(testSecret, time.Hour, suraujwt.DefaultIssuer, suraujwt.DefaultAudience)
	j2 := suraujwt.New(strings.Repeat("x", 32), time.Hour, suraujwt.DefaultIssuer, suraujwt.DefaultAudience)

	token, err := j1.GenerateToken("user-123")
	require.NoError(t, err)

	_, err = j2.ParseToken(token)
	require.Error(t, err)
}

func TestJWT_ParseToken_Expired(t *testing.T) {
	t.Parallel()

	j := newTestManager(-time.Hour)

	token, err := j.GenerateToken("user-123")
	require.NoError(t, err)

	_, err = j.ParseToken(token)
	require.Error(t, err)
}

func TestJWT_ParseToken_MissingExpiration(t *testing.T) {
	t.Parallel()

	claims := baseClaims()
	claims.ExpiresAt = nil
	token := signRegisteredClaims(t, claims, jwtlib.SigningMethodHS256)

	_, err := newTestManager(time.Hour).ParseToken(token)
	require.Error(t, err)
}

func TestJWT_ParseToken_WrongAlgorithm(t *testing.T) {
	t.Parallel()

	token := signRegisteredClaims(t, baseClaims(), jwtlib.SigningMethodHS512)

	_, err := newTestManager(time.Hour).ParseToken(token)
	require.Error(t, err)
}

func TestJWT_ParseToken_WrongIssuer(t *testing.T) {
	t.Parallel()

	claims := baseClaims()
	claims.Issuer = "other-issuer"
	token := signRegisteredClaims(t, claims, jwtlib.SigningMethodHS256)

	_, err := newTestManager(time.Hour).ParseToken(token)
	require.Error(t, err)
}

func TestJWT_ParseToken_WrongAudience(t *testing.T) {
	t.Parallel()

	claims := baseClaims()
	claims.Audience = jwtlib.ClaimStrings{"other-audience"}
	token := signRegisteredClaims(t, claims, jwtlib.SigningMethodHS256)

	_, err := newTestManager(time.Hour).ParseToken(token)
	require.Error(t, err)
}

func TestJWT_ParseToken_EmptySubject(t *testing.T) {
	t.Parallel()

	claims := baseClaims()
	claims.Subject = ""
	token := signRegisteredClaims(t, claims, jwtlib.SigningMethodHS256)

	_, err := newTestManager(time.Hour).ParseToken(token)
	require.Error(t, err)
	assert.True(t, errors.Is(err, suraujwt.ErrEmptySubject))
}
