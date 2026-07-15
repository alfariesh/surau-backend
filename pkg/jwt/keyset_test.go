package jwt_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	suraujwt "github.com/alfariesh/surau-backend/pkg/jwt"
	jwtlib "github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	oldKeyID    = "old"
	newKeyID    = "new"
	oldSecret   = "old-secret-0123456789-abcdefghijkl"
	newSecret   = "new-secret-0123456789-abcdefghijkl"
	thirdSecret = "third-secret-01234567-abcdefghijkl"
)

var errUnexpectedKeyCount = errors.New("unexpected JWT key count")

func rotationKeyset(activeKeyID string) suraujwt.Keyset {
	return suraujwt.Keyset{
		Version:   suraujwt.KeysetVersion,
		ActiveKID: activeKeyID,
		LegacyKID: oldKeyID,
		Keys: map[string]string{
			oldKeyID: oldSecret,
			newKeyID: newSecret,
		},
	}
}

func oldOnlyKeyset() suraujwt.Keyset {
	return suraujwt.Keyset{
		Version:   suraujwt.KeysetVersion,
		ActiveKID: oldKeyID,
		LegacyKID: oldKeyID,
		Keys:      map[string]string{oldKeyID: oldSecret},
	}
}

func retiredKeyset() suraujwt.Keyset {
	return suraujwt.Keyset{
		Version:   suraujwt.KeysetVersion,
		ActiveKID: newKeyID,
		Keys:      map[string]string{newKeyID: newSecret},
	}
}

func signTokenWithKeyID(t *testing.T, secret string, keyID any, includeKeyID bool) string {
	t.Helper()

	token := jwtlib.NewWithClaims(jwtlib.SigningMethodHS256, baseClaims())
	if includeKeyID {
		token.Header["kid"] = keyID
	}

	tokenString, err := token.SignedString([]byte(secret))
	require.NoError(t, err)

	return tokenString
}

func tokenKeyID(t *testing.T, tokenString string) any {
	t.Helper()

	token, _, err := jwtlib.NewParser().ParseUnverified(tokenString, &jwtlib.RegisteredClaims{})
	require.NoError(t, err)

	return token.Header["kid"]
}

func tokenExpiry(t *testing.T, tokenString string) time.Time {
	t.Helper()

	claims := &jwtlib.RegisteredClaims{}
	_, _, err := jwtlib.NewParser().ParseUnverified(tokenString, claims)
	require.NoError(t, err)
	require.NotNil(t, claims.ExpiresAt)

	return claims.ExpiresAt.Time
}

func writeKeysetFile(t *testing.T, path string, keyset suraujwt.Keyset) {
	t.Helper()

	require.NoError(t, writeKeysetFileResult(path, keyset))
}

func writeRawFile(t *testing.T, path string, contents []byte) {
	t.Helper()

	temporaryPath := path + ".tmp"
	require.NoError(t, os.WriteFile(temporaryPath, contents, 0o600))
	require.NoError(t, os.Rename(temporaryPath, path))
}

func writeKeysetFileResult(path string, keyset suraujwt.Keyset) error {
	contents, err := json.Marshal(keyset)
	if err != nil {
		return fmt.Errorf("marshal keyset: %w", err)
	}

	temporaryPath := path + ".tmp"
	if err = os.WriteFile(temporaryPath, contents, 0o600); err != nil {
		return fmt.Errorf("write keyset: %w", err)
	}

	if err = os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace keyset: %w", err)
	}

	return nil
}

func TestJWT_NewMintsKeyIDAndAcceptsLegacyToken(t *testing.T) {
	t.Parallel()

	manager := suraujwt.New(testSecret, time.Hour, "", "")
	token, err := manager.GenerateToken("user-123")
	require.NoError(t, err)
	assert.Equal(t, suraujwt.DefaultKeyID, tokenKeyID(t, token))
	assert.Equal(t, suraujwt.KeysetStatus{
		Version:   suraujwt.KeysetVersion,
		ActiveKID: suraujwt.DefaultKeyID,
		LegacyKID: suraujwt.DefaultKeyID,
		KeyIDs:    []string{suraujwt.DefaultKeyID},
	}, manager.Status())

	legacyToken := signTokenWithKeyID(t, testSecret, nil, false)
	userID, err := manager.ParseToken(legacyToken)
	require.NoError(t, err)
	assert.Equal(t, "user-123", userID)
}

func TestJWT_KeysetRotationOverlapAndRetirement(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "jwt-keyset.json")
	writeKeysetFile(t, path, oldOnlyKeyset())
	manager, err := suraujwt.NewFromKeysetFile(path, time.Hour, "", "")
	require.NoError(t, err)

	oldToken, err := manager.GenerateToken("old-token-user")
	require.NoError(t, err)
	legacyToken := signTokenWithKeyID(t, oldSecret, nil, false)
	assert.Equal(t, oldKeyID, tokenKeyID(t, oldToken))

	writeKeysetFile(t, path, rotationKeyset(newKeyID))
	require.NoError(t, manager.Reload())
	newToken, err := manager.GenerateToken("new-token-user")
	require.NoError(t, err)
	assert.Equal(t, newKeyID, tokenKeyID(t, newToken))

	for _, tokenString := range []string{oldToken, legacyToken, newToken} {
		_, parseErr := manager.ParseToken(tokenString)
		require.NoError(t, parseErr)
	}

	writeKeysetFile(t, path, retiredKeyset())
	require.NoError(t, manager.Reload())
	require.True(t, tokenExpiry(t, oldToken).After(time.Now()), "old token must still be unexpired at retirement")

	_, err = manager.ParseToken(oldToken)
	require.ErrorIs(t, err, suraujwt.ErrUnknownKeyID)
	_, err = manager.ParseToken(legacyToken)
	require.ErrorIs(t, err, suraujwt.ErrMissingKeyID)
	_, err = manager.ParseToken(newToken)
	require.NoError(t, err)
}

func TestJWT_SignerRollbackIsSafeDuringOverlap(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "jwt-keyset.json")
	writeKeysetFile(t, path, rotationKeyset(newKeyID))
	manager, err := suraujwt.NewFromKeysetFile(path, time.Hour, "", "")
	require.NoError(t, err)

	newToken, err := manager.GenerateToken("new-signer-user")
	require.NoError(t, err)
	writeKeysetFile(t, path, rotationKeyset(oldKeyID))
	require.NoError(t, manager.Reload())
	rollbackToken, err := manager.GenerateToken("rollback-user")
	require.NoError(t, err)

	assert.Equal(t, oldKeyID, tokenKeyID(t, rollbackToken))

	for _, tokenString := range []string{newToken, rollbackToken} {
		_, parseErr := manager.ParseToken(tokenString)
		require.NoError(t, parseErr)
	}

	writeKeysetFile(t, path, rotationKeyset(newKeyID))
	require.NoError(t, manager.Reload())

	for _, tokenString := range []string{newToken, rollbackToken} {
		_, parseErr := manager.ParseToken(tokenString)
		require.NoError(t, parseErr)
	}
}

func TestJWT_StrictKeyIDLookup(t *testing.T) {
	t.Parallel()

	manager, err := suraujwt.NewWithKeyset(oldOnlyKeyset(), time.Hour, "", "")
	require.NoError(t, err)

	testCases := []struct {
		name     string
		keyID    any
		secret   string
		expected error
	}{
		{name: "unknown", keyID: newKeyID, secret: oldSecret, expected: suraujwt.ErrUnknownKeyID},
		{name: "case-sensitive", keyID: "OLD", secret: oldSecret, expected: suraujwt.ErrUnknownKeyID},
		{name: "number", keyID: 42, secret: oldSecret, expected: suraujwt.ErrInvalidKeyID},
		{name: "blank", keyID: "", secret: oldSecret, expected: suraujwt.ErrInvalidKeyID},
		{name: "whitespace", keyID: " old ", secret: oldSecret, expected: suraujwt.ErrInvalidKeyID},
		{name: "too-long", keyID: strings.Repeat("a", suraujwt.MaxKeyIDBytes+1), secret: oldSecret, expected: suraujwt.ErrInvalidKeyID},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			token := signTokenWithKeyID(t, testCase.secret, testCase.keyID, true)
			_, parseErr := manager.ParseToken(token)
			require.ErrorIs(t, parseErr, testCase.expected)
		})
	}

	t.Run("known kid never tries another key", func(t *testing.T) {
		t.Parallel()

		token := signTokenWithKeyID(t, newSecret, oldKeyID, true)
		_, parseErr := manager.ParseToken(token)
		require.Error(t, parseErr)
		assert.False(t, errors.Is(parseErr, suraujwt.ErrUnknownKeyID))
		assert.False(t, errors.Is(parseErr, suraujwt.ErrInvalidKeyID))
	})
}

func TestJWT_InvalidReloadKeepsLastValidSnapshot(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "jwt-keyset.json")
	writeKeysetFile(t, path, oldOnlyKeyset())
	manager, err := suraujwt.NewFromKeysetFile(path, time.Hour, "", "")
	require.NoError(t, err)

	before := manager.Status()
	oldToken, err := manager.GenerateToken("user-123")
	require.NoError(t, err)

	redactionMarker := strings.Repeat("z", suraujwt.MinHS256KeyBytes-1)
	invalid := suraujwt.Keyset{
		Version:   suraujwt.KeysetVersion,
		ActiveKID: newKeyID,
		Keys:      map[string]string{newKeyID: redactionMarker},
	}
	writeKeysetFile(t, path, invalid)

	err = manager.Reload()
	require.ErrorIs(t, err, suraujwt.ErrInvalidKeyset)
	assert.NotContains(t, err.Error(), redactionMarker)
	assert.Equal(t, before, manager.Status())

	afterToken, generateErr := manager.GenerateToken("user-456")
	require.NoError(t, generateErr)
	assert.Equal(t, oldKeyID, tokenKeyID(t, afterToken))

	_, err = manager.ParseToken(oldToken)
	require.NoError(t, err)
}

func TestJWT_StatusNeverExposesSecrets(t *testing.T) {
	t.Parallel()

	manager, err := suraujwt.NewWithKeyset(rotationKeyset(newKeyID), time.Hour, "", "")
	require.NoError(t, err)
	encoded, err := json.Marshal(manager.Status())
	require.NoError(t, err)

	assert.NotContains(t, string(encoded), oldSecret)
	assert.NotContains(t, string(encoded), newSecret)
	assert.JSONEq(t, `{"version":1,"active_kid":"new","legacy_kid":"old","key_ids":["new","old"]}`, string(encoded))
}

func TestJWT_NewWithKeysetCopiesCallerData(t *testing.T) {
	t.Parallel()

	keyset := oldOnlyKeyset()
	manager, err := suraujwt.NewWithKeyset(keyset, time.Hour, "", "")
	require.NoError(t, err)

	keyset.ActiveKID = newKeyID
	keyset.Keys[oldKeyID] = newSecret
	keyset.Keys[newKeyID] = newSecret

	token, err := manager.GenerateToken("user-123")
	require.NoError(t, err)
	assert.Equal(t, oldKeyID, tokenKeyID(t, token))
	_, err = manager.ParseToken(token)
	require.NoError(t, err)
}

func TestJWT_KeySecretsRemainLiteral(t *testing.T) {
	t.Parallel()

	literalSecret := " " + strings.Repeat("x", suraujwt.MinHS256KeyBytes) + " "
	literalKeyset := suraujwt.Keyset{
		Version:   suraujwt.KeysetVersion,
		ActiveKID: oldKeyID,
		Keys:      map[string]string{oldKeyID: literalSecret},
	}
	manager, err := suraujwt.NewWithKeyset(literalKeyset, time.Hour, "", "")
	require.NoError(t, err)
	token, err := manager.GenerateToken("user-123")
	require.NoError(t, err)

	trimmedKeyset := literalKeyset
	trimmedKeyset.Keys = map[string]string{oldKeyID: strings.TrimSpace(literalSecret)}
	trimmedManager, err := suraujwt.NewWithKeyset(trimmedKeyset, time.Hour, "", "")
	require.NoError(t, err)
	_, err = trimmedManager.ParseToken(token)
	require.Error(t, err)
}

func TestJWT_KeysetValidation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		mutate func(*suraujwt.Keyset)
	}{
		{name: "missing version", mutate: func(keyset *suraujwt.Keyset) { keyset.Version = 0 }},
		{name: "unsupported version", mutate: func(keyset *suraujwt.Keyset) { keyset.Version = 2 }},
		{name: "blank active kid", mutate: func(keyset *suraujwt.Keyset) { keyset.ActiveKID = "" }},
		{name: "invalid active kid", mutate: func(keyset *suraujwt.Keyset) { keyset.ActiveKID = "old/key" }},
		{name: "long active kid", mutate: func(keyset *suraujwt.Keyset) { keyset.ActiveKID = strings.Repeat("a", suraujwt.MaxKeyIDBytes+1) }},
		{name: "invalid legacy kid", mutate: func(keyset *suraujwt.Keyset) { keyset.LegacyKID = "old key" }},
		{name: "long legacy kid", mutate: func(keyset *suraujwt.Keyset) { keyset.LegacyKID = strings.Repeat("a", suraujwt.MaxKeyIDBytes+1) }},
		{name: "empty keys", mutate: func(keyset *suraujwt.Keyset) { keyset.Keys = nil }},
		{name: "active absent", mutate: func(keyset *suraujwt.Keyset) { delete(keyset.Keys, oldKeyID) }},
		{name: "legacy absent", mutate: func(keyset *suraujwt.Keyset) { keyset.LegacyKID = "absent" }},
		{name: "invalid map key", mutate: func(keyset *suraujwt.Keyset) { keyset.Keys["old/key"] = newSecret }},
		{name: "long map key", mutate: func(keyset *suraujwt.Keyset) { keyset.Keys[strings.Repeat("a", suraujwt.MaxKeyIDBytes+1)] = newSecret }},
		{name: "short secret", mutate: func(keyset *suraujwt.Keyset) { keyset.Keys[oldKeyID] = "too-short" }},
		{name: "duplicate secrets", mutate: func(keyset *suraujwt.Keyset) { keyset.Keys[newKeyID] = oldSecret }},
		{name: "more than two keys", mutate: func(keyset *suraujwt.Keyset) {
			keyset.Keys[newKeyID] = newSecret
			keyset.Keys["third"] = thirdSecret
		}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			keyset := oldOnlyKeyset()
			testCase.mutate(&keyset)
			_, err := suraujwt.NewWithKeyset(keyset, time.Hour, "", "")
			require.ErrorIs(t, err, suraujwt.ErrInvalidKeyset)
		})
	}
}

func TestJWT_LoadKeysetFileRejectsNonStrictJSON(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		contents string
	}{
		{name: "unknown field", contents: fmt.Sprintf(`{"version":1,"active_kid":"old","legacy_kid":"old","keys":{"old":%q},"extra":true}`, oldSecret)},
		{name: "trailing value", contents: fmt.Sprintf(`{"version":1,"active_kid":"old","legacy_kid":"old","keys":{"old":%q}} {}`, oldSecret)},
		{name: "missing version", contents: fmt.Sprintf(`{"active_kid":"old","legacy_kid":"old","keys":{"old":%q}}`, oldSecret)},
		{name: "wrong version type", contents: fmt.Sprintf(`{"version":"1","active_kid":"old","legacy_kid":"old","keys":{"old":%q}}`, oldSecret)},
		{name: "blank legacy kid", contents: fmt.Sprintf(`{"version":1,"active_kid":"old","legacy_kid":"","keys":{"old":%q}}`, oldSecret)},
		{name: "null legacy kid", contents: fmt.Sprintf(`{"version":1,"active_kid":"old","legacy_kid":null,"keys":{"old":%q}}`, oldSecret)},
		{name: "incorrectly cased field", contents: fmt.Sprintf(`{"Version":1,"active_kid":"old","legacy_kid":"old","keys":{"old":%q}}`, oldSecret)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "jwt-keyset.json")
			writeRawFile(t, path, []byte(testCase.contents))
			_, err := suraujwt.LoadKeysetFile(path)
			require.ErrorIs(t, err, suraujwt.ErrInvalidKeyset)
			assert.NotContains(t, err.Error(), oldSecret)
		})
	}

	_, err := suraujwt.LoadKeysetFile(" ")
	require.ErrorIs(t, err, suraujwt.ErrInvalidKeyset)
}

func TestJWT_ReloadRequiresFileBackedManager(t *testing.T) {
	t.Parallel()

	manager := suraujwt.New(testSecret, time.Hour, "", "")
	require.ErrorIs(t, manager.Reload(), suraujwt.ErrKeysetFileNotConfigured)
	manager, err := suraujwt.NewWithKeyset(oldOnlyKeyset(), time.Hour, "", "")
	require.NoError(t, err)
	require.ErrorIs(t, manager.Reload(), suraujwt.ErrKeysetFileNotConfigured)
}

func TestJWT_ConcurrentGenerateParseAndReload(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "jwt-keyset.json")
	writeKeysetFile(t, path, rotationKeyset(oldKeyID))
	manager, err := suraujwt.NewFromKeysetFile(path, time.Hour, "", "")
	require.NoError(t, err)

	const (
		readerCount = 8
		iterations  = 100
	)

	errorsFound := make(chan error, readerCount+1)

	var waitGroup sync.WaitGroup
	waitGroup.Add(readerCount + 1)

	go func() {
		defer waitGroup.Done()

		for iteration := range iterations {
			activeKeyID := oldKeyID
			if iteration%2 == 1 {
				activeKeyID = newKeyID
			}

			if writeErr := writeKeysetFileResult(path, rotationKeyset(activeKeyID)); writeErr != nil {
				errorsFound <- fmt.Errorf("write iteration %d: %w", iteration, writeErr)

				return
			}

			if reloadErr := manager.Reload(); reloadErr != nil {
				errorsFound <- fmt.Errorf("reload iteration %d: %w", iteration, reloadErr)

				return
			}
		}
	}()

	for reader := range readerCount {
		go func() {
			defer waitGroup.Done()

			for iteration := range iterations {
				token, generateErr := manager.GenerateToken(fmt.Sprintf("user-%d-%d", reader, iteration))
				if generateErr != nil {
					errorsFound <- fmt.Errorf("generate: %w", generateErr)

					return
				}

				if _, parseErr := manager.ParseToken(token); parseErr != nil {
					errorsFound <- fmt.Errorf("parse: %w", parseErr)

					return
				}

				status := manager.Status()
				if len(status.KeyIDs) != 2 {
					errorsFound <- fmt.Errorf("%w: got %d", errUnexpectedKeyCount, len(status.KeyIDs))

					return
				}
			}
		}()
	}

	waitGroup.Wait()
	close(errorsFound)

	for operationErr := range errorsFound {
		require.NoError(t, operationErr)
	}
}
