package jwt

import (
	"errors"
	"fmt"
	"strings"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ErrUnexpectedSigningMethod is returned when the JWT signing method is not expected.
var ErrUnexpectedSigningMethod = errors.New("unexpected signing method")

// ErrEmptySubject is returned when a JWT has no subject.
var ErrEmptySubject = errors.New("empty subject")

const (
	// DefaultIssuer is used when no JWT issuer is configured.
	DefaultIssuer = "surau-backend"
	// DefaultAudience is used when no JWT audience is configured.
	DefaultAudience = "surau-api"
)

// Manager handles JWT token generation and parsing.
type Manager struct {
	secret   string
	duration time.Duration
	issuer   string
	audience string
}

// TokenClaims contains the identity fields needed by auth middleware.
type TokenClaims struct {
	UserID       string
	TokenVersion int64
	SessionID    string
}

type registeredClaims struct {
	TokenVersion int64  `json:"token_version"`
	SessionID    string `json:"sid,omitempty"`
	jwtlib.RegisteredClaims
}

// New -.
func New(secret string, duration time.Duration, issuer, audience string) *Manager {
	if issuer == "" {
		issuer = DefaultIssuer
	}
	if audience == "" {
		audience = DefaultAudience
	}

	return &Manager{
		secret:   secret,
		duration: duration,
		issuer:   issuer,
		audience: audience,
	}
}

// GenerateToken creates a new JWT token for the given user ID.
func (m *Manager) GenerateToken(userID string, tokenVersion ...int64) (string, error) {
	version := int64(0)
	if len(tokenVersion) > 0 {
		version = tokenVersion[0]
	}

	token, _, err := m.GenerateSessionToken(userID, version, "")

	return token, err
}

// GenerateSessionToken creates an access token bound to a session family and
// returns the token with its expiry time.
func (m *Manager) GenerateSessionToken(userID string, tokenVersion int64, sessionID string) (string, time.Time, error) {
	if strings.TrimSpace(userID) == "" {
		return "", time.Time{}, ErrEmptySubject
	}

	now := time.Now().UTC()
	expiresAt := now.Add(m.duration)
	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, registeredClaims{
		TokenVersion: tokenVersion,
		SessionID:    sessionID,
		RegisteredClaims: jwtlib.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwtlib.NewNumericDate(expiresAt),
			IssuedAt:  jwtlib.NewNumericDate(now),
			ID:        uuid.NewString(),
			Issuer:    m.issuer,
			Audience:  jwtlib.ClaimStrings{m.audience},
		},
	})

	tokenString, err := token.SignedString([]byte(m.secret))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("jwt - GenerateSessionToken - token.SignedString: %w", err)
	}

	return tokenString, expiresAt, nil
}

// ParseToken validates a JWT token and returns the user ID.
func (m *Manager) ParseToken(tokenString string) (string, error) {
	claims, err := m.ParseTokenClaims(tokenString)
	if err != nil {
		return "", err
	}

	return claims.UserID, nil
}

// ParseTokenClaims validates a JWT token and returns identity claims.
func (m *Manager) ParseTokenClaims(tokenString string) (TokenClaims, error) {
	claims := &registeredClaims{}
	token, err := jwtlib.ParseWithClaims(
		strings.TrimSpace(tokenString),
		claims,
		func(token *jwtlib.Token) (any, error) {
			if token.Method != jwtlib.SigningMethodHS256 {
				return nil, fmt.Errorf("%w: %v", ErrUnexpectedSigningMethod, token.Header["alg"])
			}

			return []byte(m.secret), nil
		},
		jwtlib.WithValidMethods([]string{jwtlib.SigningMethodHS256.Alg()}),
		jwtlib.WithExpirationRequired(),
		jwtlib.WithIssuedAt(),
		jwtlib.WithIssuer(m.issuer),
		jwtlib.WithAudience(m.audience),
	)
	if err != nil {
		return TokenClaims{}, fmt.Errorf("jwt - ParseTokenClaims - jwtlib.Parse: %w", err)
	}
	if !token.Valid {
		return TokenClaims{}, errors.New("jwt - ParseTokenClaims - invalid token")
	}

	sub := strings.TrimSpace(claims.Subject)
	if sub == "" {
		return TokenClaims{}, fmt.Errorf("jwt - ParseTokenClaims - %w", ErrEmptySubject)
	}

	return TokenClaims{
		UserID:       sub,
		TokenVersion: claims.TokenVersion,
		SessionID:    claims.SessionID,
	}, nil
}
