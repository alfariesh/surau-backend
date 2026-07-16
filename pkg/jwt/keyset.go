package jwt

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

const (
	// KeysetVersion is the only keyset file format understood by this package.
	KeysetVersion = 1
	// DefaultKeyID keeps the legacy New constructor source-compatible while
	// ensuring every newly minted token has a kid header.
	DefaultKeyID = "legacy"
	// MinHS256KeyBytes is the minimum accepted key length for file-backed and
	// explicitly constructed keysets.
	MinHS256KeyBytes = 32
	// MaxKeyIDBytes bounds kid values in both keyset files and token headers.
	MaxKeyIDBytes = 64
	// MaxKeysetKeys permits one stable key or exactly the old/new overlap pair.
	MaxKeysetKeys = 2

	maxKeysetFileBytes = 64 * 1024
)

var (
	// ErrInvalidKeyset is returned when a keyset fails schema or safety checks.
	ErrInvalidKeyset = errors.New("invalid JWT keyset")
	// ErrKeysetFileNotConfigured is returned when Reload is called on a Manager
	// that was not constructed from a keyset file.
	ErrKeysetFileNotConfigured = errors.New("JWT keyset file is not configured")

	keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Keyset is the versioned on-disk contract for HS256 signing and verification.
// Keys contains literal secret values; callers must never log or serialize a
// Keyset outside the protected runtime configuration file.
type Keyset struct {
	Version   int               `json:"version"`
	ActiveKID string            `json:"active_kid"`
	LegacyKID string            `json:"legacy_kid,omitempty"`
	Keys      map[string]string `json:"keys"`
}

// KeysetStatus is safe to expose in logs and operational status output. It
// intentionally contains no key material.
type KeysetStatus struct {
	Version   int      `json:"version"`
	ActiveKID string   `json:"active_kid"`
	LegacyKID string   `json:"legacy_kid,omitempty"`
	KeyIDs    []string `json:"key_ids"`
}

type keysetSnapshot struct {
	version   int
	activeKID string
	legacyKID string
	keys      map[string][]byte
}

// LoadKeysetFile reads and validates a strict version-1 keyset JSON file.
func LoadKeysetFile(path string) (Keyset, error) {
	if strings.TrimSpace(path) == "" {
		return Keyset{}, fmt.Errorf("%w: empty file path", ErrInvalidKeyset)
	}

	file, err := os.Open(path) // #nosec G304 -- the operator-selected secret file is the intended input.
	if err != nil {
		return Keyset{}, fmt.Errorf("jwt - LoadKeysetFile - os.Open: %w", err)
	}
	defer file.Close()

	contents, err := io.ReadAll(io.LimitReader(file, maxKeysetFileBytes+1))
	if err != nil {
		return Keyset{}, fmt.Errorf("jwt - LoadKeysetFile - io.ReadAll: %w", err)
	}

	if len(contents) > maxKeysetFileBytes {
		return Keyset{}, fmt.Errorf("%w: file exceeds size limit", ErrInvalidKeyset)
	}

	keyset, err := decodeKeyset(contents)
	if err != nil {
		return Keyset{}, err
	}

	if _, err = newKeysetSnapshot(keyset); err != nil {
		return Keyset{}, err
	}

	return keyset, nil
}

func decodeKeyset(contents []byte) (Keyset, error) {
	hasLegacyKeyID, err := validateExactKeysetFields(contents)
	if err != nil {
		return Keyset{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()

	var keyset Keyset
	if err = decoder.Decode(&keyset); err != nil {
		return Keyset{}, fmt.Errorf("%w: decode: %w", ErrInvalidKeyset, err)
	}

	if err := requireJSONEOF(decoder); err != nil {
		return Keyset{}, err
	}

	if hasLegacyKeyID && keyset.LegacyKID == "" {
		return Keyset{}, fmt.Errorf("%w: legacy_kid must be omitted or valid", ErrInvalidKeyset)
	}

	return keyset, nil
}

func validateExactKeysetFields(contents []byte) (bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(contents, &fields); err != nil {
		return false, fmt.Errorf("%w: decode fields: %w", ErrInvalidKeyset, err)
	}

	if fields == nil {
		return false, fmt.Errorf("%w: top-level value must be an object", ErrInvalidKeyset)
	}

	for field := range fields {
		switch field {
		case "version", "active_kid", "legacy_kid", "keys":
		default:
			return false, fmt.Errorf("%w: unknown or incorrectly cased field", ErrInvalidKeyset)
		}
	}

	_, hasLegacyKeyID := fields["legacy_kid"]

	return hasLegacyKeyID, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any

	err := decoder.Decode(&trailing)

	if errors.Is(err, io.EOF) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("%w: trailing JSON: %w", ErrInvalidKeyset, err)
	}

	return fmt.Errorf("%w: multiple JSON values", ErrInvalidKeyset)
}

func newKeysetSnapshot(keyset Keyset) (*keysetSnapshot, error) {
	if err := validateKeysetHeader(keyset); err != nil {
		return nil, err
	}

	keys, err := validateAndCopyKeys(keyset.Keys)
	if err != nil {
		return nil, err
	}

	if _, ok := keys[keyset.ActiveKID]; !ok {
		return nil, fmt.Errorf("%w: active_kid is absent from keys", ErrInvalidKeyset)
	}

	if keyset.LegacyKID != "" {
		if _, ok := keys[keyset.LegacyKID]; !ok {
			return nil, fmt.Errorf("%w: legacy_kid is absent from keys", ErrInvalidKeyset)
		}
	}

	return &keysetSnapshot{
		version:   keyset.Version,
		activeKID: keyset.ActiveKID,
		legacyKID: keyset.LegacyKID,
		keys:      keys,
	}, nil
}

func validateKeysetHeader(keyset Keyset) error {
	if keyset.Version != KeysetVersion {
		return fmt.Errorf("%w: unsupported or missing version", ErrInvalidKeyset)
	}

	if !validKeyID(keyset.ActiveKID) {
		return fmt.Errorf("%w: active_kid has an invalid format", ErrInvalidKeyset)
	}

	if keyset.LegacyKID != "" && !validKeyID(keyset.LegacyKID) {
		return fmt.Errorf("%w: legacy_kid has an invalid format", ErrInvalidKeyset)
	}

	if len(keyset.Keys) == 0 || len(keyset.Keys) > MaxKeysetKeys {
		return fmt.Errorf("%w: keys must contain one stable key or two overlap keys", ErrInvalidKeyset)
	}

	return nil
}

func validateAndCopyKeys(source map[string]string) (map[string][]byte, error) {
	keys := make(map[string][]byte, len(source))
	seenSecrets := make(map[string]struct{}, len(source))

	for keyID, secret := range source {
		if !validKeyID(keyID) {
			return nil, fmt.Errorf("%w: keys contains an invalid key ID", ErrInvalidKeyset)
		}

		if len(secret) < MinHS256KeyBytes {
			return nil, fmt.Errorf("%w: key is shorter than %d bytes", ErrInvalidKeyset, MinHS256KeyBytes)
		}

		if _, duplicate := seenSecrets[secret]; duplicate {
			return nil, fmt.Errorf("%w: keys must contain distinct secrets", ErrInvalidKeyset)
		}

		keys[keyID] = []byte(secret)
		seenSecrets[secret] = struct{}{}
	}

	return keys, nil
}

func validKeyID(keyID string) bool {
	return len(keyID) <= MaxKeyIDBytes && keyIDPattern.MatchString(keyID)
}

func (snapshot *keysetSnapshot) status() KeysetStatus {
	keyIDs := make([]string, 0, len(snapshot.keys))
	for keyID := range snapshot.keys {
		keyIDs = append(keyIDs, keyID)
	}

	sort.Strings(keyIDs)

	return KeysetStatus{
		Version:   snapshot.version,
		ActiveKID: snapshot.activeKID,
		LegacyKID: snapshot.legacyKID,
		KeyIDs:    keyIDs,
	}
}
