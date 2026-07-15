package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testOldKID    = "dev-old-20260715"
	testNewKID    = "dev-new-20260715"
	testOldSecret = " old-secret-0123456789abcdef0123456789 "
)

func TestRotationLifecycle(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "jwt-keyset.json")
	now := time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)
	randomBytes := bytes.Repeat([]byte{0x5a}, generatedKeyBytes)
	deps := testDependencies(&now, bytes.NewReader(randomBytes), testOldSecret)

	stdout, stderr, code := runCommand(t, deps, "bootstrap", "--file", path, "--kid", testOldKID)
	require.Equal(t, 0, code, stderr)
	assert.NotContains(t, stdout, testOldSecret)
	assert.Empty(t, stderr)
	assertSecretFileMode(t, path)

	value := mustLoadKeyset(t, path)
	assert.Equal(t, testOldKID, value.ActiveKID)
	assert.Equal(t, testOldKID, value.LegacyKID)
	assert.Equal(t, testOldSecret, value.Keys[testOldKID], "bootstrap must not normalize JWT_SECRET bytes")

	stdout, stderr, code = runCommand(t, deps, "prepare", "--file", path, "--new-kid", testNewKID)
	require.Equal(t, 0, code, stderr)
	assert.NotContains(t, stdout, testOldSecret)
	assertSecretFileMode(t, metadataPath(path))

	value = mustLoadKeyset(t, path)
	generatedSecret := base64.RawURLEncoding.EncodeToString(randomBytes)

	assert.Equal(t, testOldKID, value.ActiveKID, "prepare must not switch the signer")
	assert.Equal(t, testOldKID, value.LegacyKID, "no-kid compatibility must remain mapped to the old key")
	assert.Equal(t, generatedSecret, value.Keys[testNewKID])
	assert.NotEqual(t, value.Keys[testOldKID], value.Keys[testNewKID])
	assert.Equal(t, statePrepared, mustLoadMetadata(t, path).State)

	now = now.Add(time.Minute)
	firstActivation := now
	stdout, stderr, code = runCommand(t, deps, "activate", "--file", path, "--overlap", "30m")
	require.Equal(t, 0, code, stderr)
	assert.NotContains(t, stdout, generatedSecret)

	value = mustLoadKeyset(t, path)
	metadata := mustLoadMetadata(t, path)
	assert.Equal(t, testNewKID, value.ActiveKID, "activate must switch issuance immediately")
	assert.Len(t, value.Keys, 2, "both verification keys must remain during overlap")
	assert.Equal(t, stateActive, metadata.State)
	assert.Equal(t, firstActivation, *metadata.ActivatedAt)
	assert.Equal(t, firstActivation.Add(30*time.Minute), *metadata.RetireNotBefore)

	now = firstActivation.Add(10 * time.Minute)
	_, stderr, code = runCommand(t, deps, "retire", "--file", path)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "cannot retire before")
	assert.Len(t, mustLoadKeyset(t, path).Keys, 2)

	stdout, stderr, code = runCommand(t, deps, "rollback", "--file", path)
	require.Equal(t, 0, code, stderr)
	assert.NotContains(t, stdout, testOldSecret)
	value = mustLoadKeyset(t, path)
	assert.Equal(t, testOldKID, value.ActiveKID)
	assert.Len(t, value.Keys, 2, "rollback must not remove the new verifier")
	assert.Equal(t, stateRolledBack, mustLoadMetadata(t, path).State)

	now = firstActivation.Add(20 * time.Minute)
	secondActivation := now
	_, stderr, code = runCommand(t, deps, "activate", "--file", path, "--overlap", "45m")
	require.Equal(t, 0, code, stderr)
	metadata = mustLoadMetadata(t, path)
	assert.Equal(t, secondActivation, *metadata.ActivatedAt)
	assert.Equal(t, secondActivation.Add(45*time.Minute), *metadata.RetireNotBefore,
		"reactivation must give every token minted during rollback a fresh full overlap")

	now = secondActivation.Add(45 * time.Minute)
	stdout, stderr, code = runCommand(t, deps, "retire", "--file", path)
	require.Equal(t, 0, code, stderr)
	assert.NotContains(t, stdout, testOldSecret)
	value = mustLoadKeyset(t, path)
	assert.Equal(t, testNewKID, value.ActiveKID)
	assert.Empty(t, value.LegacyKID, "retirement must permanently disable no-kid verification")
	assert.Equal(t, map[string]string{testNewKID: generatedSecret}, value.Keys)
	assert.Equal(t, stateRetired, mustLoadMetadata(t, path).State)

	stdout, stderr, code = runCommand(t, deps, "status", "--file", path)
	require.Equal(t, 0, code, stderr)
	assert.NotContains(t, stdout, testOldSecret)
	assert.NotContains(t, stdout, generatedSecret)
	assert.Contains(t, stdout, `"state": "retired"`)
	assert.Contains(t, stdout, `"active_kid": "`+testNewKID+`"`)
	assert.Contains(t, stdout, `"retired_at":`)
}

func TestStatusIsSanitizedThroughoutOverlap(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := testDependencies(&now, bytes.NewReader(bytes.Repeat([]byte{0x33}, generatedKeyBytes)), testOldSecret)
	require.Equal(t, 0, commandCode(t, deps, "bootstrap", "--file", path, "--kid", testOldKID))
	require.Equal(t, 0, commandCode(t, deps, "prepare", "--file", path, "--new-kid", testNewKID))
	require.Equal(t, 0, commandCode(t, deps, "activate", "--file", path, "--overlap", "1h"))

	stdout, stderr, code := runCommand(t, deps, "status", "--file", path)
	require.Equal(t, 0, code, stderr)

	var status statusOutput

	require.NoError(t, json.Unmarshal([]byte(stdout), &status))
	assert.Equal(t, stateActive, status.State)
	assert.Equal(t, testNewKID, status.ActiveKID)
	assert.Equal(t, testOldKID, status.LegacyKID)
	assert.Equal(t, []string{testNewKID, testOldKID}, status.KeyIDs)
	assert.False(t, status.RetirementDue)
	assert.NotContains(t, stdout, testOldSecret)

	for _, secret := range mustLoadKeyset(t, path).Keys {
		assert.NotContains(t, stdout, secret)
	}

	now = now.Add(time.Hour)
	stdout, stderr, code = runCommand(t, deps, "status", "--file", path)
	require.Equal(t, 0, code, stderr)
	require.NoError(t, json.Unmarshal([]byte(stdout), &status))
	assert.True(t, status.RetirementDue)
}

func TestExportWorkerRequiresExplicitSeparateFileAndNeverPrintsSecret(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "keyset.json")
	outputPath := filepath.Join(directory, "worker-keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := testDependencies(&now, bytes.NewReader(bytes.Repeat([]byte{0x44}, generatedKeyBytes)), testOldSecret)
	require.Equal(t, 0, commandCode(t, deps, "bootstrap", "--file", path, "--kid", testOldKID))

	_, _, code := runCommand(t, deps, "export-worker", "--file", path)
	assert.Equal(t, 2, code)
	_, _, code = runCommand(t, deps, "export-worker", "--file", path, "--out", path)
	assert.Equal(t, 2, code)

	require.NoError(t, os.WriteFile(outputPath, []byte("stale"), 0o600))
	require.NoError(t, os.Chmod(outputPath, 0o644))
	stdout, stderr, code := runCommand(t, deps, "export-worker", "--file", path, "--out", outputPath)
	require.Equal(t, 0, code, stderr)
	assert.NotContains(t, stdout, testOldSecret)
	assertSecretFileMode(t, outputPath)
	assert.Equal(t, mustLoadKeyset(t, path), mustLoadKeyset(t, outputPath))
}

func TestBootstrapRefusesShortSecretAndExistingDestination(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	shortDeps := testDependencies(&now, bytes.NewReader(nil), "too-short")

	_, stderr, code := runCommand(t, shortDeps, "bootstrap", "--file", path, "--kid", testOldKID)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "at least 32 bytes")

	_, err := os.Stat(path)
	assert.ErrorIs(t, err, os.ErrNotExist)

	deps := testDependencies(&now, bytes.NewReader(nil), testOldSecret)
	require.Equal(t, 0, commandCode(t, deps, "bootstrap", "--file", path, "--kid", testOldKID))
	original, err := os.ReadFile(path)
	require.NoError(t, err)

	_, stderr, code = runCommand(t, deps, "bootstrap", "--file", path, "--kid", "different-kid")
	assert.Equal(t, 1, code)
	assert.NotContains(t, stderr, testOldSecret)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, after, "bootstrap must never replace an existing keyset")
}

func TestBootstrapRejectsInvalidUTF8RatherThanChangingSecretBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	invalidSecret := strings.Repeat("a", minimumSecretBytes) + string([]byte{0xff})
	deps := testDependencies(&now, bytes.NewReader(nil), invalidSecret)

	_, stderr, code := runCommand(t, deps, "bootstrap", "--file", path, "--kid", testOldKID)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "valid UTF-8")
	assert.NotContains(t, stderr, invalidSecret)
}

func TestPrepareFailsClosedWhenRandomSourceFails(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := testDependencies(&now, errorReader{}, testOldSecret)
	require.Equal(t, 0, commandCode(t, deps, "bootstrap", "--file", path, "--kid", testOldKID))

	_, stderr, code := runCommand(t, deps, "prepare", "--file", path, "--new-kid", testNewKID)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "crypto random")
	assert.Equal(t, map[string]string{testOldKID: testOldSecret}, mustLoadKeyset(t, path).Keys)
	assert.Equal(t, statePrepared, mustLoadMetadata(t, path).State)

	recoveryDeps := testDependencies(&now, bytes.NewReader(bytes.Repeat([]byte{0x77}, generatedKeyBytes)), testOldSecret)
	_, stderr, code = runCommand(t, recoveryDeps, "prepare", "--file", path, "--new-kid", testNewKID)
	require.Equal(t, 0, code, stderr)
	assert.Len(t, mustLoadKeyset(t, path).Keys, maximumOverlapKeys)
}

func TestInterruptedPrepareIsSafelyRerunnable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := testDependencies(&now, bytes.NewReader(bytes.Repeat([]byte{0x11}, generatedKeyBytes)), testOldSecret)
	require.Equal(t, 0, commandCode(t, deps, "bootstrap", "--file", path, "--kid", testOldKID))

	failingWriter := &failNthWriter{failAt: 2}
	deps.writeFile = failingWriter.write
	_, stderr, code := runCommand(t, deps, "prepare", "--file", path, "--new-kid", testNewKID)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write prepared keyset")
	assert.Equal(t, statePrepared, mustLoadMetadata(t, path).State)
	assert.Len(t, mustLoadKeyset(t, path).Keys, 1, "interruption must leave the old signer intact")

	recoveryDeps := testDependencies(&now, bytes.NewReader(bytes.Repeat([]byte{0x22}, generatedKeyBytes)), testOldSecret)
	_, stderr, code = runCommand(t, recoveryDeps, "prepare", "--file", path, "--new-kid", testNewKID)
	require.Equal(t, 0, code, stderr)

	value, metadata, err := loadRotationState(path)
	require.NoError(t, err)
	assert.Equal(t, testOldKID, value.ActiveKID)
	assert.Equal(t, statePrepared, metadata.State)
	assert.Len(t, value.Keys, maximumOverlapKeys)

	recoveryDeps.random = errorReader{}
	_, stderr, code = runCommand(t, recoveryDeps, "prepare", "--file", path, "--new-kid", testNewKID)
	require.Equal(t, 0, code, stderr, "completed prepare must be idempotent and consume no new randomness")
}

func TestInterruptedActivateResetsGateBeforeSwitchingSigner(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := prepareRotation(t, path, &now)

	failingWriter := &failNthWriter{failAt: 2}
	deps.writeFile = failingWriter.write
	_, stderr, code := runCommand(t, deps, "activate", "--file", path, "--overlap", "1h")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "activate signer")
	assert.Equal(t, testOldKID, mustLoadKeyset(t, path).ActiveKID,
		"gate must persist before a signer switch is attempted")
	firstGate := *mustLoadMetadata(t, path).RetireNotBefore

	now = now.Add(2 * time.Hour)
	recoveryStarted := now
	recoveryDeps := testDependencies(&now, bytes.NewReader(nil), testOldSecret)
	_, stderr, code = runCommand(t, recoveryDeps, "activate", "--file", path, "--overlap", "1h")
	require.Equal(t, 0, code, stderr)

	value, metadata, err := loadRotationState(path)
	require.NoError(t, err)
	assert.Equal(t, testNewKID, value.ActiveKID)
	assert.True(t, firstGate.Before(recoveryStarted), "the interrupted gate should demonstrate why it must be reset")
	assert.Equal(t, recoveryStarted.Add(time.Hour), *metadata.RetireNotBefore,
		"recovery must restart the full token-lifetime overlap before switching issuance")
}

func TestInterruptedRollbackIsSafelyRerunnableAfterOriginalGate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := prepareRotation(t, path, &now)
	require.Equal(t, 0, commandCode(t, deps, "activate", "--file", path, "--overlap", "1h"))

	now = now.Add(10 * time.Minute)
	failingWriter := &failNthWriter{failAt: 2}
	deps.writeFile = failingWriter.write
	_, stderr, code := runCommand(t, deps, "rollback", "--file", path)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "rollback signer")
	assert.Equal(t, stateRolledBack, mustLoadMetadata(t, path).State)
	assert.Equal(t, testNewKID, mustLoadKeyset(t, path).ActiveKID)

	now = now.Add(2 * time.Hour)
	recoveryDeps := testDependencies(&now, bytes.NewReader(nil), testOldSecret)
	_, stderr, code = runCommand(t, recoveryDeps, "rollback", "--file", path)
	require.Equal(t, 0, code, stderr)

	value, metadata, err := loadRotationState(path)
	require.NoError(t, err)
	assert.Equal(t, testOldKID, value.ActiveKID)
	assert.Equal(t, stateRolledBack, metadata.State)
}

func TestInterruptedRetireIsSafelyRerunnable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := prepareRotation(t, path, &now)
	require.Equal(t, 0, commandCode(t, deps, "activate", "--file", path, "--overlap", "1h"))

	now = now.Add(time.Hour)

	failingWriter := &failNthWriter{failAt: 2}
	deps.writeFile = failingWriter.write
	_, stderr, code := runCommand(t, deps, "retire", "--file", path)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "record retirement")
	assert.Equal(t, map[string]string{testNewKID: mustLoadKeyset(t, path).Keys[testNewKID]}, mustLoadKeyset(t, path).Keys)
	assert.Equal(t, stateActive, mustLoadMetadata(t, path).State)

	recoveryDeps := testDependencies(&now, bytes.NewReader(nil), testOldSecret)
	_, stderr, code = runCommand(t, recoveryDeps, "retire", "--file", path)
	require.Equal(t, 0, code, stderr)

	value, metadata, err := loadRotationState(path)
	require.NoError(t, err)
	assert.Equal(t, testNewKID, value.ActiveKID)
	assert.Empty(t, value.LegacyKID)
	assert.Equal(t, stateRetired, metadata.State)

	_, stderr, code = runCommand(t, recoveryDeps, "retire", "--file", path)
	require.Equal(t, 0, code, stderr, "completed retirement must be idempotent")
}

func TestAdvisoryLockRejectsConcurrentCommand(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := testDependencies(&now, bytes.NewReader(nil), testOldSecret)
	require.Equal(t, 0, commandCode(t, deps, "bootstrap", "--file", path, "--kid", testOldKID))

	unlock, err := acquireAdvisoryLock(path)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, unlock())
	})

	_, stderr, code := runCommand(t, deps, "status", "--file", path)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, errConcurrent.Error())
	assertSecretFileMode(t, path+".lock")
}

func TestRollbackClosesAtRetirementGate(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "keyset.json")
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := testDependencies(&now, bytes.NewReader(bytes.Repeat([]byte{0x55}, generatedKeyBytes)), testOldSecret)
	require.Equal(t, 0, commandCode(t, deps, "bootstrap", "--file", path, "--kid", testOldKID))
	require.Equal(t, 0, commandCode(t, deps, "prepare", "--file", path, "--new-kid", testNewKID))
	require.Equal(t, 0, commandCode(t, deps, "activate", "--file", path, "--overlap", "1m"))

	now = now.Add(time.Minute)
	_, stderr, code := runCommand(t, deps, "rollback", "--file", path)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "rollback window closed")
	assert.Equal(t, testNewKID, mustLoadKeyset(t, path).ActiveKID)
}

func TestStrictKeysetValidation(t *testing.T) {
	t.Parallel()

	validSecret := strings.Repeat("a", minimumSecretBytes)
	otherSecret := strings.Repeat("b", minimumSecretBytes)
	tests := map[string]string{ //nolint:gosec // Synthetic invalid keyset fixtures are not production credentials.
		"unknown field":       `{"version":1,"active_kid":"old","keys":{"old":"` + validSecret + `"},"surprise":true}`,
		"trailing JSON":       `{"version":1,"active_kid":"old","keys":{"old":"` + validSecret + `"}} {}`,
		"wrong version":       `{"version":2,"active_kid":"old","keys":{"old":"` + validSecret + `"}}`,
		"invalid active kid":  `{"version":1,"active_kid":"old key","keys":{"old key":"` + validSecret + `"}}`,
		"active absent":       `{"version":1,"active_kid":"old","keys":{"new":"` + validSecret + `"}}`,
		"legacy absent":       `{"version":1,"active_kid":"old","legacy_kid":"missing","keys":{"old":"` + validSecret + `"}}`,
		"short secret":        `{"version":1,"active_kid":"old","keys":{"old":"short"}}`,
		"duplicate secrets":   `{"version":1,"active_kid":"old","keys":{"old":"` + validSecret + `","new":"` + validSecret + `"}}`,
		"more than two keys":  `{"version":1,"active_kid":"a","keys":{"a":"` + validSecret + `","b":"` + otherSecret + `","c":"` + strings.Repeat("c", minimumSecretBytes) + `"}}`,
		"overlap no metadata": `{"version":1,"active_kid":"old","keys":{"old":"` + validSecret + `","new":"` + otherSecret + `"}}`,
	}

	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "keyset.json")
			require.NoError(t, os.WriteFile(path, []byte(payload), secretFilePermissions))

			if name == "overlap no metadata" {
				_, _, err := loadRotationState(path)
				assert.Error(t, err)

				return
			}

			_, err := loadKeyset(path)
			assert.Error(t, err)
		})
	}
}

func TestSecretFilesRejectUnsafeModeAndSymlink(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	payload := `{"version":1,"active_kid":"old","keys":{"old":"` + strings.Repeat("a", minimumSecretBytes) + `"}}`
	unsafePath := filepath.Join(directory, "unsafe.json")
	require.NoError(t, os.WriteFile(unsafePath, []byte(payload), 0o600))
	require.NoError(t, os.Chmod(unsafePath, 0o644))
	_, err := loadKeyset(unsafePath)
	assert.ErrorContains(t, err, "permissions must be 0600")

	safePath := filepath.Join(directory, "safe.json")
	require.NoError(t, os.WriteFile(safePath, []byte(payload), secretFilePermissions))

	symlinkPath := filepath.Join(directory, "link.json")
	require.NoError(t, os.Symlink(safePath, symlinkPath))
	_, err = loadKeyset(symlinkPath)
	assert.ErrorContains(t, err, "not a regular file")

	oversizedPath := filepath.Join(directory, "oversized.json")
	require.NoError(t, os.WriteFile(oversizedPath, bytes.Repeat([]byte{'x'}, maximumSecretFileSize+1), secretFilePermissions))
	_, err = loadKeyset(oversizedPath)
	assert.ErrorContains(t, err, "64 KiB size limit")
}

func TestStrictRotationMetadataValidation(t *testing.T) {
	t.Parallel()

	preparedAt := time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)
	activatedAt := preparedAt.Add(time.Minute)
	retireNotBefore := activatedAt.Add(time.Hour)
	base := rotationMetadata{
		Version:           metadataVersion,
		State:             stateActive,
		PreviousActiveKID: "old",
		NextKID:           "new",
		PreparedAt:        preparedAt,
		ActivatedAt:       &activatedAt,
		RetireNotBefore:   &retireNotBefore,
	}

	tests := map[string]func(*rotationMetadata){
		"same kid": func(metadata *rotationMetadata) {
			metadata.NextKID = metadata.PreviousActiveKID
		},
		"unknown state": func(metadata *rotationMetadata) {
			metadata.State = "unknown"
		},
		"missing activation": func(metadata *rotationMetadata) {
			metadata.ActivatedAt = nil
		},
		"gate before activation": func(metadata *rotationMetadata) {
			value := activatedAt.Add(-time.Second)
			metadata.RetireNotBefore = &value
		},
		"premature retired at": func(metadata *rotationMetadata) {
			metadata.State = stateRetired
			metadata.RetiredAt = &activatedAt
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			metadata := base
			mutate(&metadata)
			assert.Error(t, validateMetadata(&metadata))
		})
	}
}

func TestCommandUsageExitCodes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	deps := testDependencies(&now, bytes.NewReader(nil), testOldSecret)
	tests := [][]string{
		nil,
		{"unknown"},
		{"status"},
		{"bootstrap", "--file", "somewhere"},
		{"prepare", "--file", "somewhere", "--new-kid", "spaces are invalid"},
		{"activate", "--file", "somewhere", "--overlap", "0s"},
		{"retire", "--file", "somewhere", "extra"},
	}

	for _, args := range tests {
		_, stderr, code := runCommand(t, deps, args...)
		assert.Equal(t, 2, code, "args: %v, stderr: %s", args, stderr)
	}
}

func TestAtomicWriteJSONUses0600AndNoClobber(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "secret.json")
	first := map[string]string{"secret": testOldSecret}
	second := map[string]string{"secret": strings.Repeat("z", minimumSecretBytes)}

	require.NoError(t, atomicWriteJSON(path, first, false))
	assertSecretFileMode(t, path)

	err := atomicWriteJSON(path, second, false)
	assert.Error(t, err)

	contents, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Contains(t, string(contents), testOldSecret)
	assert.NotContains(t, string(contents), second["secret"])

	require.NoError(t, os.Chmod(path, 0o644))
	require.NoError(t, atomicWriteJSON(path, second, true))
	assertSecretFileMode(t, path)
	contents, readErr = os.ReadFile(path)
	require.NoError(t, readErr)
	assert.NotContains(t, string(contents), testOldSecret)
	assert.Contains(t, string(contents), second["secret"])
}

func testDependencies(now *time.Time, randomSource io.Reader, secret string) commandDependencies {
	return commandDependencies{
		now: func() time.Time {
			return *now
		},
		random: randomSource,
		getenv: func(name string) string {
			if name == "JWT_SECRET" {
				return secret
			}

			return ""
		},
		writeFile: atomicWriteJSON,
		lock:      acquireAdvisoryLock,
	}
}

func prepareRotation(t *testing.T, path string, now *time.Time) commandDependencies {
	t.Helper()

	deps := testDependencies(now, bytes.NewReader(bytes.Repeat([]byte{0x66}, generatedKeyBytes)), testOldSecret)
	require.Equal(t, 0, commandCode(t, deps, "bootstrap", "--file", path, "--kid", testOldKID))
	require.Equal(t, 0, commandCode(t, deps, "prepare", "--file", path, "--new-kid", testNewKID))

	return deps
}

func runCommand(t *testing.T, deps commandDependencies, args ...string) (
	stdoutText string,
	stderrText string,
	exitCode int,
) {
	t.Helper()

	var stdout bytes.Buffer

	var stderr bytes.Buffer

	code := run(args, &stdout, &stderr, deps)

	return stdout.String(), stderr.String(), code
}

func commandCode(t *testing.T, deps commandDependencies, args ...string) int {
	t.Helper()

	_, stderr, code := runCommand(t, deps, args...)
	require.Empty(t, stderr)

	return code
}

func mustLoadKeyset(t *testing.T, path string) keyset {
	t.Helper()

	value, err := loadKeyset(path)
	require.NoError(t, err)

	return value
}

func mustLoadMetadata(t *testing.T, path string) *rotationMetadata {
	t.Helper()

	metadata, err := loadMetadata(metadataPath(path))
	require.NoError(t, err)
	require.NotNil(t, metadata)

	return metadata
}

func assertSecretFileMode(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(secretFilePermissions), info.Mode().Perm())
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

type failNthWriter struct {
	calls  int
	failAt int
}

func (writer *failNthWriter) write(path string, value any, replace bool) error {
	writer.calls++
	if writer.calls == writer.failAt {
		return io.ErrClosedPipe
	}

	return atomicWriteJSON(path, value, replace)
}
