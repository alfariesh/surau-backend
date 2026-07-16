// Command jwt-keyset manages the HS256 keyset used for zero-logout JWT
// rotations. Secret material is only ever written to mode-0600 files; stdout
// contains identifiers and timestamps, never key values.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	jwtpkg "github.com/alfariesh/surau-backend/pkg/jwt"
)

const (
	keysetVersion         = jwtpkg.KeysetVersion
	metadataVersion       = 1
	generatedKeyBytes     = 32
	minimumSecretBytes    = jwtpkg.MinHS256KeyBytes
	maximumKIDBytes       = jwtpkg.MaxKeyIDBytes
	maximumOverlapKeys    = 2
	maximumSecretFileSize = 64 * 1024
	secretFilePermissions = 0o600
	usageExitCode         = 2
	asciiKeyIDCharacters  = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

	statePrepared   = "prepared"
	stateActive     = "active"
	stateRolledBack = "rolled_back"
	stateRetired    = "retired"
)

var (
	errInvalidKeyset = jwtpkg.ErrInvalidKeyset
	errInvalidState  = errors.New("invalid rotation state")
	errUsage         = errors.New("invalid command usage")
	errConcurrent    = errors.New("another jwt-keyset process holds the advisory lock")
)

type keyset = jwtpkg.Keyset

type rotationMetadata struct {
	Version           int        `json:"version"`
	State             string     `json:"state"`
	PreviousActiveKID string     `json:"previous_active_kid"`
	NextKID           string     `json:"next_kid"`
	PreparedAt        time.Time  `json:"prepared_at"`
	ActivatedAt       *time.Time `json:"activated_at,omitempty"`
	RetireNotBefore   *time.Time `json:"retire_not_before,omitempty"`
	RolledBackAt      *time.Time `json:"rolled_back_at,omitempty"`
	RetiredAt         *time.Time `json:"retired_at,omitempty"`
}

type commandDependencies struct {
	now       func() time.Time
	random    io.Reader
	getenv    func(string) string
	writeFile func(string, any, bool) error
	lock      func(string) (func() error, error)
}

type commandOptions struct {
	file       string
	kid        string
	newKID     string
	overlap    time.Duration
	outputFile string
}

type statusOutput struct {
	Version           int        `json:"version"`
	State             string     `json:"state"`
	ActiveKID         string     `json:"active_kid"`
	LegacyKID         string     `json:"legacy_kid,omitempty"`
	KeyIDs            []string   `json:"key_ids"`
	PreviousActiveKID string     `json:"previous_active_kid,omitempty"`
	NextKID           string     `json:"next_kid,omitempty"`
	PreparedAt        *time.Time `json:"prepared_at,omitempty"`
	ActivatedAt       *time.Time `json:"activated_at,omitempty"`
	RetireNotBefore   *time.Time `json:"retire_not_before,omitempty"`
	RolledBackAt      *time.Time `json:"rolled_back_at,omitempty"`
	RetiredAt         *time.Time `json:"retired_at,omitempty"`
	RetirementDue     bool       `json:"retirement_due"`
}

func main() {
	deps := commandDependencies{
		now:       time.Now,
		random:    rand.Reader,
		getenv:    os.Getenv,
		writeFile: atomicWriteJSON,
		lock:      acquireAdvisoryLock,
	}

	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, deps))
}

func run(args []string, stdout, stderr io.Writer, deps commandDependencies) int {
	if len(args) == 0 {
		writeUsage(stderr)

		return usageExitCode
	}

	command := args[0]

	opts, err := parseCommandOptions(command, args[1:], stderr)
	if err != nil {
		fmt.Fprintf(stderr, "jwt-keyset: %v\n", err)

		return usageExitCode
	}

	err = executeLocked(command, opts, stdout, deps)
	if err != nil {
		fmt.Fprintf(stderr, "jwt-keyset: %v\n", err)

		return 1
	}

	return 0
}

func executeLocked(
	command string,
	opts commandOptions,
	stdout io.Writer,
	deps commandDependencies,
) (err error) {
	unlock, err := deps.lock(opts.file)
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, unlock())
	}()

	return execute(command, opts, stdout, deps)
}

func parseCommandOptions(command string, args []string, stderr io.Writer) (commandOptions, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	opts := commandOptions{}

	flags.StringVar(&opts.file, "file", "", "path to the runtime keyset JSON file")

	switch command {
	case "bootstrap":
		flags.StringVar(&opts.kid, "kid", "", "identifier for the current JWT_SECRET")
	case "prepare":
		flags.StringVar(&opts.newKID, "new-kid", "", "identifier for the generated replacement key")
	case "activate":
		flags.DurationVar(&opts.overlap, "overlap", 0, "minimum old-key verification window")
	case "rollback", "retire", "status":
	case "export-worker":
		flags.StringVar(&opts.outputFile, "out", "", "explicit mode-0600 output file")
	default:
		writeUsage(stderr)

		return commandOptions{}, fmt.Errorf("%w: unknown command %q", errUsage, command)
	}

	if err := flags.Parse(args); err != nil {
		return commandOptions{}, fmt.Errorf("%w: %w", errUsage, err)
	}

	if flags.NArg() != 0 {
		return commandOptions{}, fmt.Errorf("%w: unexpected positional arguments", errUsage)
	}

	if opts.file == "" {
		return commandOptions{}, fmt.Errorf("%w: --file is required", errUsage)
	}

	return validateCommandOptions(command, opts)
}

func validateCommandOptions(command string, opts commandOptions) (commandOptions, error) {
	switch command {
	case "bootstrap":
		return opts, validateKIDOption("--kid", opts.kid)
	case "prepare":
		return opts, validateKIDOption("--new-kid", opts.newKID)
	case "activate":
		if opts.overlap <= 0 {
			return commandOptions{}, fmt.Errorf("%w: --overlap must be positive", errUsage)
		}

		return opts, nil
	case "export-worker":
		return opts, validateExportOptions(opts)
	case "rollback", "retire", "status":
		return opts, nil
	}

	return opts, nil
}

func validateKIDOption(name, value string) error {
	if err := validateKID(value); err != nil {
		return fmt.Errorf("%w: %s: %w", errUsage, name, err)
	}

	return nil
}

func validateExportOptions(opts commandOptions) error {
	if opts.outputFile == "" {
		return fmt.Errorf("%w: --out is required", errUsage)
	}

	if sameCleanPath(opts.file, opts.outputFile) {
		return fmt.Errorf("%w: --out must differ from --file", errUsage)
	}

	return nil
}

func execute(command string, opts commandOptions, stdout io.Writer, deps commandDependencies) error {
	switch command {
	case "bootstrap":
		return bootstrap(opts, stdout, deps)
	case "prepare":
		return prepare(opts, stdout, deps)
	case "activate":
		return activate(opts, stdout, deps)
	case "rollback":
		return rollback(opts, stdout, deps)
	case "retire":
		return retire(opts, stdout, deps)
	case "status":
		return printStatus(opts, stdout, deps.now().UTC())
	case "export-worker":
		return exportWorker(opts, stdout, deps)
	default:
		return fmt.Errorf("%w: unknown command", errUsage)
	}
}

func bootstrap(opts commandOptions, stdout io.Writer, deps commandDependencies) error {
	secret := deps.getenv("JWT_SECRET")
	if len(secret) < minimumSecretBytes {
		return fmt.Errorf("%w: JWT_SECRET must be at least %d bytes", errInvalidKeyset, minimumSecretBytes)
	}

	if !utf8.ValidString(secret) {
		return fmt.Errorf("%w: JWT_SECRET must be valid UTF-8 to preserve its bytes in JSON", errInvalidKeyset)
	}

	value := keyset{
		Version:   keysetVersion,
		ActiveKID: opts.kid,
		LegacyKID: opts.kid,
		Keys:      map[string]string{opts.kid: secret},
	}
	if err := validateKeyset(value); err != nil {
		return err
	}

	if err := deps.writeFile(opts.file, value, false); err != nil {
		return fmt.Errorf("bootstrap keyset: %w", err)
	}

	fmt.Fprintf(stdout, "bootstrapped keyset %s with active kid %s and legacy no-kid verification enabled\n", opts.file, opts.kid)

	return nil
}

func prepare(opts commandOptions, stdout io.Writer, deps commandDependencies) error {
	value, metadata, err := loadRotationFiles(opts.file)
	if err != nil {
		return err
	}

	if metadata != nil && metadata.State == statePrepared {
		return resumePrepare(opts, stdout, deps, value, metadata)
	}

	if err := validateStableForPrepare(value, metadata); err != nil {
		return err
	}

	if _, exists := value.Keys[opts.newKID]; exists {
		return fmt.Errorf("%w: new kid already exists", errInvalidState)
	}

	preparedAt := deps.now().UTC()
	metadata = &rotationMetadata{
		Version:           metadataVersion,
		State:             statePrepared,
		PreviousActiveKID: value.ActiveKID,
		NextKID:           opts.newKID,
		PreparedAt:        preparedAt,
	}

	if err := deps.writeFile(metadataPath(opts.file), metadata, true); err != nil {
		return fmt.Errorf("write rotation metadata: %w", err)
	}

	return finishPrepare(opts, stdout, deps, value, metadata)
}

func resumePrepare(
	opts commandOptions,
	stdout io.Writer,
	deps commandDependencies,
	value keyset,
	metadata *rotationMetadata,
) error {
	if metadata.NextKID != opts.newKID {
		return fmt.Errorf("%w: prepared rotation expects --new-kid %s", errInvalidState, metadata.NextKID)
	}

	if err := validatePreparedFiles(value, metadata, true); err != nil {
		return err
	}

	if _, prepared := value.Keys[metadata.NextKID]; prepared {
		fmt.Fprintf(stdout, "kid %s is already prepared; signer remains %s\n", opts.newKID, value.ActiveKID)

		return nil
	}

	return finishPrepare(opts, stdout, deps, value, metadata)
}

func finishPrepare(
	opts commandOptions,
	stdout io.Writer,
	deps commandDependencies,
	value keyset,
	metadata *rotationMetadata,
) error {
	secret, err := generateSecret(deps.random)
	if err != nil {
		return fmt.Errorf("generate replacement key: %w", err)
	}

	value.Keys[metadata.NextKID] = secret
	if err := validateRotationState(value, metadata); err != nil {
		return err
	}

	if err := deps.writeFile(opts.file, value, true); err != nil {
		return fmt.Errorf("write prepared keyset: %w", err)
	}

	fmt.Fprintf(stdout, "prepared kid %s; signer remains %s\n", opts.newKID, value.ActiveKID)

	return nil
}

//nolint:cyclop,gocyclo // Branches are the explicit safe states of an interrupted two-file transition.
func activate(opts commandOptions, stdout io.Writer, deps commandDependencies) error {
	value, metadata, err := loadRotationFiles(opts.file)
	if err != nil {
		return err
	}

	if metadata == nil {
		return fmt.Errorf("%w: activate requires a prepared rotation", errInvalidState)
	}

	if metadata.State == stateActive && activeSignerComplete(value, metadata) {
		fmt.Fprintf(stdout, "kid %s is already active; old kid cannot retire before %s\n",
			metadata.NextKID, metadata.RetireNotBefore.Format(time.RFC3339))

		return nil
	}

	if metadata.State != statePrepared && metadata.State != stateRolledBack && metadata.State != stateActive {
		return fmt.Errorf("%w: activate requires a prepared, rolled-back, or interrupted active rotation", errInvalidState)
	}

	if err := validateDualRotationKeys(value, metadata); err != nil {
		return err
	}

	activatedAt := deps.now().UTC()
	retireNotBefore := activatedAt.Add(opts.overlap)
	metadata.State = stateActive
	metadata.ActivatedAt = &activatedAt
	metadata.RetireNotBefore = &retireNotBefore
	metadata.RolledBackAt = nil

	metadata.RetiredAt = nil
	if err := validateMetadata(metadata); err != nil {
		return err
	}

	if err := deps.writeFile(metadataPath(opts.file), metadata, true); err != nil {
		return fmt.Errorf("record activation gate: %w", err)
	}

	value.ActiveKID = metadata.NextKID
	if err := validateRotationState(value, metadata); err != nil {
		return err
	}

	if err := deps.writeFile(opts.file, value, true); err != nil {
		return fmt.Errorf("activate signer: %w", err)
	}

	fmt.Fprintf(stdout, "activated kid %s; old kid %s cannot retire before %s\n",
		metadata.NextKID, metadata.PreviousActiveKID, retireNotBefore.Format(time.RFC3339))

	return nil
}

//nolint:cyclop,gocognit,gocyclo // Branches make interrupted rollback recovery and its time gate explicit.
func rollback(opts commandOptions, stdout io.Writer, deps commandDependencies) error {
	value, metadata, err := loadRotationFiles(opts.file)
	if err != nil {
		return err
	}

	if metadata == nil || (metadata.State != stateActive && metadata.State != stateRolledBack) {
		return fmt.Errorf("%w: rollback requires an active or interrupted rollback overlap", errInvalidState)
	}

	if err := validateDualRotationKeys(value, metadata); err != nil {
		return err
	}

	if metadata.State == stateRolledBack && value.ActiveKID == metadata.PreviousActiveKID {
		fmt.Fprintf(stdout, "signer is already rolled back to kid %s; both verifier keys remain loaded\n", value.ActiveKID)

		return nil
	}

	now := deps.now().UTC()
	if metadata.State == stateActive && !now.Before(*metadata.RetireNotBefore) {
		return fmt.Errorf("%w: rollback window closed at %s", errInvalidState, metadata.RetireNotBefore.Format(time.RFC3339))
	}

	if metadata.State == stateActive {
		metadata.State = stateRolledBack

		metadata.RolledBackAt = &now
		if err := validateMetadata(metadata); err != nil {
			return err
		}

		if err := deps.writeFile(metadataPath(opts.file), metadata, true); err != nil {
			return fmt.Errorf("record rollback: %w", err)
		}
	}

	value.ActiveKID = metadata.PreviousActiveKID
	if err := validateRotationState(value, metadata); err != nil {
		return err
	}

	if err := deps.writeFile(opts.file, value, true); err != nil {
		return fmt.Errorf("rollback signer: %w", err)
	}

	fmt.Fprintf(stdout, "rolled signer back to kid %s; both verifier keys remain loaded\n", value.ActiveKID)

	return nil
}

//nolint:cyclop,gocognit,gocyclo // Branches make both sides of the crash-safe retirement transition explicit.
func retire(opts commandOptions, stdout io.Writer, deps commandDependencies) error {
	value, metadata, err := loadRotationFiles(opts.file)
	if err != nil {
		return err
	}

	if metadata == nil || (metadata.State != stateActive && metadata.State != stateRetired) {
		return fmt.Errorf("%w: retire requires an active or interrupted retirement", errInvalidState)
	}

	if metadata.State == stateRetired && retiredSignerComplete(value, metadata) {
		fmt.Fprintf(stdout, "kid %s is already retired; legacy no-kid tokens are rejected\n", metadata.PreviousActiveKID)

		return nil
	}

	now := deps.now().UTC()
	if now.Before(*metadata.RetireNotBefore) {
		return fmt.Errorf("%w: old key cannot retire before %s", errInvalidState, metadata.RetireNotBefore.Format(time.RFC3339))
	}

	if value.ActiveKID != metadata.NextKID {
		return fmt.Errorf("%w: signer has not completed activation; rerun activate before retire", errInvalidState)
	}

	if _, oldKeyPresent := value.Keys[metadata.PreviousActiveKID]; oldKeyPresent {
		delete(value.Keys, metadata.PreviousActiveKID)

		value.LegacyKID = ""
		if err := validateKeyset(value); err != nil {
			return err
		}

		if err := deps.writeFile(opts.file, value, true); err != nil {
			return fmt.Errorf("retire old verifier key: %w", err)
		}
	}

	if metadata.State != stateRetired {
		metadata.State = stateRetired
		metadata.RetiredAt = &now
	}

	if err := validateRotationState(value, metadata); err != nil {
		return err
	}

	if err := deps.writeFile(metadataPath(opts.file), metadata, true); err != nil {
		return fmt.Errorf("record retirement: %w", err)
	}

	fmt.Fprintf(stdout, "retired kid %s; legacy no-kid tokens are now rejected\n", metadata.PreviousActiveKID)

	return nil
}

func printStatus(opts commandOptions, stdout io.Writer, now time.Time) error {
	value, metadata, err := loadRotationState(opts.file)
	if err != nil {
		return err
	}

	status := buildStatus(value, metadata, now)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(status); err != nil {
		return fmt.Errorf("encode sanitized status: %w", err)
	}

	return nil
}

func exportWorker(opts commandOptions, stdout io.Writer, deps commandDependencies) error {
	value, metadata, err := loadRotationState(opts.file)
	if err != nil {
		return err
	}

	if err := validateRotationState(value, metadata); err != nil {
		return err
	}

	if err := deps.writeFile(opts.outputFile, value, true); err != nil {
		return fmt.Errorf("export worker keyset: %w", err)
	}

	fmt.Fprintf(stdout, "exported worker keyset to %s with active kid %s\n", opts.outputFile, value.ActiveKID)

	return nil
}

func buildStatus(value keyset, metadata *rotationMetadata, now time.Time) statusOutput {
	keyIDs := make([]string, 0, len(value.Keys))
	for kid := range value.Keys {
		keyIDs = append(keyIDs, kid)
	}

	sort.Strings(keyIDs)

	status := statusOutput{
		Version:   value.Version,
		State:     "stable",
		ActiveKID: value.ActiveKID,
		LegacyKID: value.LegacyKID,
		KeyIDs:    keyIDs,
	}
	if metadata == nil {
		return status
	}

	status.State = metadata.State
	status.PreviousActiveKID = metadata.PreviousActiveKID
	status.NextKID = metadata.NextKID
	status.PreparedAt = &metadata.PreparedAt
	status.ActivatedAt = metadata.ActivatedAt
	status.RetireNotBefore = metadata.RetireNotBefore
	status.RolledBackAt = metadata.RolledBackAt
	status.RetiredAt = metadata.RetiredAt
	status.RetirementDue = metadata.State == stateActive && metadata.RetireNotBefore != nil &&
		!now.Before(*metadata.RetireNotBefore)

	return status
}

func loadRotationState(path string) (keyset, *rotationMetadata, error) {
	value, metadata, err := loadRotationFiles(path)
	if err != nil {
		return keyset{}, nil, err
	}

	if err := validateRotationState(value, metadata); err != nil {
		return keyset{}, nil, err
	}

	return value, metadata, nil
}

func loadRotationFiles(path string) (keyset, *rotationMetadata, error) {
	value, err := loadKeyset(path)
	if err != nil {
		return keyset{}, nil, err
	}

	metadata, err := loadMetadata(metadataPath(path))
	if err != nil {
		return keyset{}, nil, err
	}

	return value, metadata, nil
}

func loadKeyset(path string) (keyset, error) {
	var value keyset
	if err := readStrictJSON(path, &value); err != nil {
		return keyset{}, fmt.Errorf("load keyset: %w", err)
	}

	if err := validateKeyset(value); err != nil {
		return keyset{}, err
	}

	return value, nil
}

func loadMetadata(path string) (*rotationMetadata, error) {
	var metadata rotationMetadata

	err := readStrictJSON(path, &metadata)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("load rotation metadata: %w", err)
	}

	if err := validateMetadata(&metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

func readStrictJSON(path string, destination any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", errInvalidKeyset, path)
	}

	if info.Size() > maximumSecretFileSize {
		return fmt.Errorf("%w: %s exceeds the 64 KiB size limit", errInvalidKeyset, path)
	}

	if info.Mode().Perm() != secretFilePermissions {
		return fmt.Errorf("%w: %s permissions must be 0600", errInvalidKeyset, path)
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: %s contains trailing JSON", errInvalidKeyset, path)
		}

		return fmt.Errorf("decode trailing data in %s: %w", path, err)
	}

	return nil
}

func validateKeyset(value keyset) error {
	if len(value.Keys) > maximumOverlapKeys {
		return fmt.Errorf("%w: keys must contain one stable key or two overlap keys", errInvalidKeyset)
	}

	_, err := jwtpkg.NewWithKeyset(value, time.Minute, jwtpkg.DefaultIssuer, jwtpkg.DefaultAudience)
	if err != nil {
		return err
	}

	return nil
}

func validateKID(kid string) error {
	if kid == "" || len(kid) > maximumKIDBytes {
		return fmt.Errorf("%w: kid must contain 1-%d ASCII characters", errInvalidKeyset, maximumKIDBytes)
	}

	for _, char := range kid {
		if !strings.ContainsRune(asciiKeyIDCharacters, char) {
			return fmt.Errorf("%w: kid may contain only A-Z, a-z, 0-9, underscore, and hyphen", errInvalidKeyset)
		}
	}

	return nil
}

func validateMetadata(metadata *rotationMetadata) error {
	if err := validateMetadataHeader(metadata); err != nil {
		return err
	}

	switch metadata.State {
	case statePrepared:
		return validatePreparedMetadata(metadata)
	case stateActive:
		return validateActiveMetadata(metadata)
	case stateRolledBack:
		return validateRolledBackMetadata(metadata)
	case stateRetired:
		return validateRetiredMetadata(metadata)
	default:
		return fmt.Errorf("%w: unknown metadata state", errInvalidState)
	}
}

func validateMetadataHeader(metadata *rotationMetadata) error {
	if metadata.Version != metadataVersion {
		return fmt.Errorf("%w: metadata version must be %d", errInvalidState, metadataVersion)
	}

	if err := validateKID(metadata.PreviousActiveKID); err != nil {
		return fmt.Errorf("%w: previous_active_kid: %w", errInvalidState, err)
	}

	if err := validateKID(metadata.NextKID); err != nil {
		return fmt.Errorf("%w: next_kid: %w", errInvalidState, err)
	}

	if metadata.PreviousActiveKID == metadata.NextKID {
		return fmt.Errorf("%w: previous and next kid must differ", errInvalidState)
	}

	if metadata.PreparedAt.IsZero() {
		return fmt.Errorf("%w: prepared_at is required", errInvalidState)
	}

	return nil
}

func validatePreparedMetadata(metadata *rotationMetadata) error {
	if metadata.ActivatedAt != nil || metadata.RetireNotBefore != nil ||
		metadata.RolledBackAt != nil || metadata.RetiredAt != nil {
		return fmt.Errorf("%w: prepared metadata contains later-phase timestamps", errInvalidState)
	}

	return nil
}

func validateActiveMetadata(metadata *rotationMetadata) error {
	if err := validateActivationTimes(metadata); err != nil {
		return err
	}

	if metadata.RolledBackAt != nil || metadata.RetiredAt != nil {
		return fmt.Errorf("%w: active metadata contains rollback or retirement timestamp", errInvalidState)
	}

	return nil
}

func validateRolledBackMetadata(metadata *rotationMetadata) error {
	if err := validateActivationTimes(metadata); err != nil {
		return err
	}

	if metadata.RolledBackAt == nil || metadata.RolledBackAt.Before(*metadata.ActivatedAt) {
		return fmt.Errorf("%w: valid rolled_back_at is required", errInvalidState)
	}

	if metadata.RetiredAt != nil {
		return fmt.Errorf("%w: rolled-back metadata cannot be retired", errInvalidState)
	}

	return nil
}

func validateRetiredMetadata(metadata *rotationMetadata) error {
	if err := validateActivationTimes(metadata); err != nil {
		return err
	}

	if metadata.RolledBackAt != nil {
		return fmt.Errorf("%w: retired metadata cannot retain rolled_back_at", errInvalidState)
	}

	if metadata.RetiredAt == nil || metadata.RetiredAt.Before(*metadata.RetireNotBefore) {
		return fmt.Errorf("%w: retired_at must honor retire_not_before", errInvalidState)
	}

	return nil
}

func validateActivationTimes(metadata *rotationMetadata) error {
	if metadata.ActivatedAt == nil || metadata.RetireNotBefore == nil {
		return fmt.Errorf("%w: activation timestamps are required", errInvalidState)
	}

	if metadata.ActivatedAt.Before(metadata.PreparedAt) || !metadata.RetireNotBefore.After(*metadata.ActivatedAt) {
		return fmt.Errorf("%w: activation timestamp order is invalid", errInvalidState)
	}

	return nil
}

func validateRotationState(value keyset, metadata *rotationMetadata) error {
	if err := validateKeyset(value); err != nil {
		return err
	}

	if metadata == nil {
		if len(value.Keys) != 1 {
			return fmt.Errorf("%w: overlap keyset requires rotation metadata", errInvalidState)
		}

		return nil
	}

	if err := validateMetadata(metadata); err != nil {
		return err
	}

	switch metadata.State {
	case statePrepared, stateRolledBack:
		return validatePreviousSignerState(value, metadata)
	case stateActive:
		return validateActiveSignerState(value, metadata)
	case stateRetired:
		return validateRetiredSignerState(value, metadata)
	}

	return nil
}

func validatePreviousSignerState(value keyset, metadata *rotationMetadata) error {
	_, hasPrevious := value.Keys[metadata.PreviousActiveKID]

	_, hasNext := value.Keys[metadata.NextKID]
	if value.ActiveKID != metadata.PreviousActiveKID || !hasPrevious || !hasNext || len(value.Keys) != 2 {
		return fmt.Errorf("%w: prepared/rolled-back keyset does not match metadata", errInvalidState)
	}

	return nil
}

func validateActiveSignerState(value keyset, metadata *rotationMetadata) error {
	_, hasPrevious := value.Keys[metadata.PreviousActiveKID]

	_, hasNext := value.Keys[metadata.NextKID]
	if value.ActiveKID != metadata.NextKID || !hasPrevious || !hasNext || len(value.Keys) != 2 {
		return fmt.Errorf("%w: active keyset does not match metadata", errInvalidState)
	}

	return nil
}

func validateRetiredSignerState(value keyset, metadata *rotationMetadata) error {
	_, hasPrevious := value.Keys[metadata.PreviousActiveKID]
	_, hasNext := value.Keys[metadata.NextKID]

	invalid := value.ActiveKID != metadata.NextKID || hasPrevious || !hasNext || len(value.Keys) != 1
	if invalid || value.LegacyKID != "" {
		return fmt.Errorf("%w: retired keyset does not match metadata", errInvalidState)
	}

	return nil
}

func validateStableForPrepare(value keyset, metadata *rotationMetadata) error {
	if len(value.Keys) != 1 {
		return fmt.Errorf("%w: prepare requires one stable key", errInvalidState)
	}

	if metadata == nil {
		return nil
	}

	if metadata.State != stateRetired {
		return fmt.Errorf("%w: an unfinished rotation already exists", errInvalidState)
	}

	return validateRetiredSignerState(value, metadata)
}

func validatePreparedFiles(value keyset, metadata *rotationMetadata, allowMissingNext bool) error {
	if err := validateKeyset(value); err != nil {
		return err
	}

	if metadata.State != statePrepared || value.ActiveKID != metadata.PreviousActiveKID {
		return fmt.Errorf("%w: prepared keyset does not match metadata", errInvalidState)
	}

	if _, previousExists := value.Keys[metadata.PreviousActiveKID]; !previousExists {
		return fmt.Errorf("%w: prepared keyset is missing its previous key", errInvalidState)
	}

	_, nextExists := value.Keys[metadata.NextKID]
	if nextExists && len(value.Keys) == maximumOverlapKeys {
		return nil
	}

	if allowMissingNext && !nextExists && len(value.Keys) == 1 {
		return nil
	}

	return fmt.Errorf("%w: prepared key material is inconsistent", errInvalidState)
}

func validateDualRotationKeys(value keyset, metadata *rotationMetadata) error {
	if err := validateKeyset(value); err != nil {
		return err
	}

	_, previousExists := value.Keys[metadata.PreviousActiveKID]

	_, nextExists := value.Keys[metadata.NextKID]

	activeMatches := value.ActiveKID == metadata.PreviousActiveKID || value.ActiveKID == metadata.NextKID
	if !previousExists || !nextExists || !activeMatches || len(value.Keys) != maximumOverlapKeys {
		return fmt.Errorf("%w: rotation requires both old and new keys", errInvalidState)
	}

	return nil
}

func activeSignerComplete(value keyset, metadata *rotationMetadata) bool {
	return metadata.RetireNotBefore != nil && validateActiveSignerState(value, metadata) == nil
}

func retiredSignerComplete(value keyset, metadata *rotationMetadata) bool {
	return validateRetiredSignerState(value, metadata) == nil
}

func generateSecret(source io.Reader) (string, error) {
	buffer := make([]byte, generatedKeyBytes)
	if _, err := io.ReadFull(source, buffer); err != nil {
		return "", fmt.Errorf("read crypto random source: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func acquireAdvisoryLock(keysetPath string) (func() error, error) {
	lockPath := keysetPath + ".lock"

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, secretFilePermissions) // #nosec G304 -- operator path is the lock target.
	if err != nil {
		return nil, fmt.Errorf("open advisory lock: %w", err)
	}

	if err := file.Chmod(secretFilePermissions); err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("secure advisory lock: %w", err)
	}

	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		_ = file.Close()

		return nil, errConcurrent
	}

	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("acquire advisory lock: %w", err)
	}

	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		closeErr := file.Close()

		return errors.Join(unlockErr, closeErr)
	}, nil
}

func atomicWriteJSON(path string, value any, replace bool) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}

	data = append(data, '\n')

	directory := filepath.Dir(path)

	temporary, err := os.CreateTemp(directory, ".jwt-keyset-*")
	if err != nil {
		return fmt.Errorf("create temporary secret file: %w", err)
	}

	temporaryPath := temporary.Name()
	removeTemporary := true

	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := writeTemporarySecret(temporary, data); err != nil {
		return err
	}

	if err := installSecretFile(temporaryPath, path, replace); err != nil {
		return err
	}

	removeTemporary = false

	return syncDirectory(directory)
}

func writeTemporarySecret(temporary *os.File, data []byte) error {
	if err := temporary.Chmod(secretFilePermissions); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("set temporary secret permissions: %w", err)
	}

	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("write temporary secret file: %w", err)
	}

	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()

		return fmt.Errorf("sync temporary secret file: %w", err)
	}

	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary secret file: %w", err)
	}

	return nil
}

func installSecretFile(temporaryPath, path string, replace bool) error {
	if replace {
		if err := os.Rename(temporaryPath, path); err != nil {
			return fmt.Errorf("install secret file: %w", err)
		}

		return nil
	}

	if err := os.Link(temporaryPath, path); err != nil {
		return fmt.Errorf("install secret file: %w", err)
	}

	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove linked temporary file: %w", err)
	}

	return nil
}

func syncDirectory(directory string) error {
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open secret directory: %w", err)
	}
	defer directoryHandle.Close()

	if err := directoryHandle.Sync(); err != nil {
		return fmt.Errorf("sync secret directory: %w", err)
	}

	return nil
}

func metadataPath(keysetPath string) string {
	return keysetPath + ".rotation.json"
}

func sameCleanPath(first, second string) bool {
	firstAbsolute, firstErr := filepath.Abs(first)
	secondAbsolute, secondErr := filepath.Abs(second)

	return firstErr == nil && secondErr == nil && firstAbsolute == secondAbsolute
}

func writeUsage(output io.Writer) {
	fmt.Fprintln(output, "usage: jwt-keyset <bootstrap|prepare|activate|rollback|retire|status|export-worker> --file PATH [options]")
	fmt.Fprintln(output, "  bootstrap     import JWT_SECRET byte-for-byte as the current and legacy no-kid key")
	fmt.Fprintln(output, "  prepare       generate and add a new verifier key without changing the signer")
	fmt.Fprintln(output, "  activate      switch signing immediately and record the mandatory overlap gate")
	fmt.Fprintln(output, "  rollback      switch signing back during overlap while retaining both verifier keys")
	fmt.Fprintln(output, "  retire        remove the old and no-kid verifier only after the recorded gate")
	fmt.Fprintln(output, "  status        print sanitized identifiers, state, and timestamps")
	fmt.Fprintln(output, "  export-worker write the runtime keyset, including secrets, only to an explicit 0600 file")
}
