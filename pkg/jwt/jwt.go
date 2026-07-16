package jwt

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// ErrUnexpectedSigningMethod is returned when the JWT signing method is not expected.
var ErrUnexpectedSigningMethod = errors.New("unexpected signing method")

// ErrEmptySubject is returned when a JWT has no subject.
var ErrEmptySubject = errors.New("empty subject")

// ErrMissingKeyID is returned for a token without kid after legacy-token
// compatibility has been retired.
var ErrMissingKeyID = errors.New("missing JWT key ID")

// ErrInvalidKeyID is returned when a JWT kid header is not a valid string ID.
var ErrInvalidKeyID = errors.New("invalid JWT key ID")

// ErrUnknownKeyID is returned when a JWT names a key outside the active set.
var ErrUnknownKeyID = errors.New("unknown JWT key ID")

// ErrInvalidToken is returned when parsing succeeds without a valid token.
var ErrInvalidToken = errors.New("invalid JWT token")

const (
	// DefaultIssuer is used when no JWT issuer is configured.
	DefaultIssuer = "surau-backend"
	// DefaultAudience is used when no JWT audience is configured.
	DefaultAudience = "surau-api"
)

// Manager handles JWT token generation and parsing.
type Manager struct {
	keyset     atomic.Pointer[keysetSnapshot]
	reloadMu   sync.Mutex
	keysetFile string
	duration   time.Duration
	issuer     string
	audience   string
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
	snapshot := &keysetSnapshot{
		version:   KeysetVersion,
		activeKID: DefaultKeyID,
		legacyKID: DefaultKeyID,
		keys:      map[string][]byte{DefaultKeyID: []byte(secret)},
	}

	return newManager(snapshot, "", duration, issuer, audience)
}

// NewWithKeyset constructs a Manager from a validated immutable keyset copy.
func NewWithKeyset(keyset Keyset, duration time.Duration, issuer, audience string) (*Manager, error) {
	snapshot, err := newKeysetSnapshot(keyset)
	if err != nil {
		return nil, err
	}

	return newManager(snapshot, "", duration, issuer, audience), nil
}

// NewFromKeysetFile constructs a Manager whose keyset can later be atomically
// refreshed from the same file with Reload.
func NewFromKeysetFile(path string, duration time.Duration, issuer, audience string) (*Manager, error) {
	keyset, err := LoadKeysetFile(path)
	if err != nil {
		return nil, err
	}

	snapshot, err := newKeysetSnapshot(keyset)
	if err != nil {
		return nil, err
	}

	return newManager(snapshot, path, duration, issuer, audience), nil
}

func newManager(snapshot *keysetSnapshot, keysetFile string, duration time.Duration, issuer, audience string) *Manager {
	if issuer == "" {
		issuer = DefaultIssuer
	}

	if audience == "" {
		audience = DefaultAudience
	}

	manager := &Manager{
		keysetFile: keysetFile,
		duration:   duration,
		issuer:     issuer,
		audience:   audience,
	}
	manager.keyset.Store(snapshot)

	return manager
}

// Reload validates the configured keyset file completely before atomically
// publishing it. On error, all readers continue using the last valid snapshot.
func (m *Manager) Reload() error {
	if strings.TrimSpace(m.keysetFile) == "" {
		return ErrKeysetFileNotConfigured
	}

	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()

	keyset, err := LoadKeysetFile(m.keysetFile)
	if err != nil {
		return err
	}

	snapshot, err := newKeysetSnapshot(keyset)
	if err != nil {
		return err
	}

	m.keyset.Store(snapshot)

	return nil
}

// Status returns key identifiers and roles without exposing secret values.
func (m *Manager) Status() KeysetStatus {
	return m.keyset.Load().status()
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
	snapshot := m.keyset.Load()
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
	token.Header["kid"] = snapshot.activeKID

	tokenString, err := token.SignedString(snapshot.keys[snapshot.activeKID])
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
	snapshot := m.keyset.Load()

	token, err := jwtlib.ParseWithClaims(
		strings.TrimSpace(tokenString),
		claims,
		func(token *jwtlib.Token) (any, error) {
			return verificationKey(token, snapshot)
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
		return TokenClaims{}, ErrInvalidToken
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

func verificationKey(token *jwtlib.Token, snapshot *keysetSnapshot) ([]byte, error) {
	if token.Method != jwtlib.SigningMethodHS256 {
		return nil, fmt.Errorf("%w: %v", ErrUnexpectedSigningMethod, token.Header["alg"])
	}

	keyID, err := tokenKeyID(token, snapshot.legacyKID)
	if err != nil {
		return nil, err
	}

	key, ok := snapshot.keys[keyID]
	if !ok {
		return nil, ErrUnknownKeyID
	}

	return key, nil
}

func tokenKeyID(token *jwtlib.Token, legacyKeyID string) (string, error) {
	rawKeyID, exists := token.Header["kid"]
	if !exists {
		if legacyKeyID == "" {
			return "", ErrMissingKeyID
		}

		return legacyKeyID, nil
	}

	keyID, ok := rawKeyID.(string)
	if !ok || !validKeyID(keyID) {
		return "", ErrInvalidKeyID
	}

	return keyID, nil
}
