// Package onesignalerasure durably deletes authenticated users from OneSignal.
package onesignalerasure

import (
	"context"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/cryptobox"
	"github.com/google/uuid"
)

const (
	defaultBatchSize          = 50
	defaultLeaseDuration      = 2 * time.Minute
	defaultVerificationDelay  = 30 * time.Second
	defaultRetention          = 90 * 24 * time.Hour
	defaultRetryBackoffBase   = 30 * time.Second
	defaultRetryBackoffMax    = 15 * time.Minute
	maxRetryExponent          = 5
	maxReasonCodeBytes        = 64
	erasureEncryptionInfo     = "surau-onesignal-erasure-encryption-v1"
	erasureAuditHMACInfo      = "surau-onesignal-erasure-audit-hmac-v1"
	erasureAuditHMACKeyLength = 32
)

var (
	errInvalidOptions           = errors.New("invalid OneSignal erasure options")
	errInvalidExternalID        = errors.New("external id must be a canonical non-nil UUID")
	errCiphertextBinding        = errors.New("encrypted external id does not match audit binding")
	errSystemicProviderResponse = errors.New("systemic OneSignal provider rejection")
	// Provider bodies must never put a credential-shaped value in durable evidence.
	jwtLikePattern = regexp.MustCompile(`[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
)

type providerFailure struct {
	cause error
}

func (e providerFailure) Error() string {
	return "OneSignal erasure provider call failed"
}

func (e providerFailure) Unwrap() error {
	return e.cause
}

type Options struct {
	AppID             string
	Secret            string
	BatchSize         int
	LeaseDuration     time.Duration
	VerificationDelay time.Duration
	Retention         time.Duration
	RetryBackoffBase  time.Duration
	RetryBackoffMax   time.Duration
	Now               func() time.Time
}

type UseCase struct {
	repo              repo.OneSignalErasureRepo
	provider          repo.OneSignalUserEraser
	box               *cryptobox.Box
	auditKey          []byte
	appID             string
	batchSize         int
	leaseDuration     time.Duration
	verificationDelay time.Duration
	retention         time.Duration
	retryBackoffBase  time.Duration
	retryBackoffMax   time.Duration
	now               func() time.Time
	wake              chan struct{}
}

type DispatchReport struct {
	Claimed  int
	Verified int
	Retained int64
}

func New(
	erasureRepo repo.OneSignalErasureRepo,
	provider repo.OneSignalUserEraser,
	opts *Options,
) (*UseCase, error) {
	if erasureRepo == nil || provider == nil || opts == nil || strings.TrimSpace(opts.AppID) == "" {
		return nil, fmt.Errorf("%w: repository, provider, and app id are required", errInvalidOptions)
	}

	normalized := *opts

	box, err := cryptobox.New(normalized.Secret, erasureEncryptionInfo)
	if err != nil {
		return nil, fmt.Errorf("%w: encryption secret: %w", errInvalidOptions, err)
	}

	auditKey, err := hkdf.Key(
		sha256.New,
		[]byte(normalized.Secret),
		nil,
		erasureAuditHMACInfo,
		erasureAuditHMACKeyLength,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: derive audit key: %w", errInvalidOptions, err)
	}

	normalizeOptions(&normalized)

	return &UseCase{
		repo:              erasureRepo,
		provider:          provider,
		box:               box,
		auditKey:          auditKey,
		appID:             strings.TrimSpace(normalized.AppID),
		batchSize:         normalized.BatchSize,
		leaseDuration:     normalized.LeaseDuration,
		verificationDelay: normalized.VerificationDelay,
		retention:         normalized.Retention,
		retryBackoffBase:  normalized.RetryBackoffBase,
		retryBackoffMax:   normalized.RetryBackoffMax,
		now:               normalized.Now,
		wake:              make(chan struct{}, 1),
	}, nil
}

func normalizeOptions(opts *Options) {
	if opts.BatchSize <= 0 || opts.BatchSize > 100 {
		opts.BatchSize = defaultBatchSize
	}

	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = defaultLeaseDuration
	}

	if opts.VerificationDelay <= 0 {
		opts.VerificationDelay = defaultVerificationDelay
	}

	if opts.Retention <= 0 {
		opts.Retention = defaultRetention
	}

	if opts.RetryBackoffBase <= 0 {
		opts.RetryBackoffBase = defaultRetryBackoffBase
	}

	if opts.RetryBackoffMax < opts.RetryBackoffBase {
		opts.RetryBackoffMax = defaultRetryBackoffMax
	}

	if opts.Now == nil {
		opts.Now = time.Now
	}
}

// Prepare creates encrypted operational data plus a one-way audit binding.
func (uc *UseCase) Prepare(userID string) (entity.OneSignalErasureCreate, error) {
	externalID := strings.TrimSpace(userID)

	parsedID, err := uuid.Parse(externalID)
	if err != nil || parsedID == uuid.Nil || parsedID.String() != externalID {
		return entity.OneSignalErasureCreate{}, fmt.Errorf(
			"prepare OneSignal erasure: %w",
			errInvalidExternalID,
		)
	}

	ciphertext, err := uc.box.Seal([]byte(externalID))
	if err != nil {
		return entity.OneSignalErasureCreate{}, fmt.Errorf("prepare OneSignal erasure: encrypt external id: %w", err)
	}

	return entity.OneSignalErasureCreate{
		ID:                   uuid.NewString(),
		AppID:                uc.appID,
		ExternalIDCiphertext: ciphertext,
		ExternalIDHash:       uc.auditHash(externalID),
		NextAttemptAt:        uc.now().UTC(),
	}, nil
}

// Wake requests an early supervised pass after an account deletion commits.
func (uc *UseCase) Wake() {
	select {
	case uc.wake <- struct{}{}:
	default:
	}
}

func (uc *UseCase) Wakeups() <-chan struct{} {
	return uc.wake
}

// Dispatch claims, processes, and retains one bounded erasure batch.
func (uc *UseCase) Dispatch(ctx context.Context) (DispatchReport, error) {
	now := uc.now().UTC()
	leaseToken := uuid.NewString()

	erasures, err := uc.repo.ClaimDueOneSignalErasures(
		ctx,
		now,
		leaseToken,
		now.Add(uc.leaseDuration),
		uc.batchSize,
	)
	if err != nil {
		return DispatchReport{}, fmt.Errorf("OneSignal erasure - claim: %w", err)
	}

	var dispatchErr error

	report := DispatchReport{Claimed: len(erasures)}

	for i := range erasures {
		verified, processErr := uc.process(ctx, &erasures[i], now)
		if verified {
			report.Verified++
		}

		if processErr != nil {
			dispatchErr = errors.Join(dispatchErr, processErr)
		}
	}

	retained, cleanupErr := uc.repo.CleanupVerifiedOneSignalErasures(ctx, now.Add(-uc.retention))
	report.Retained = retained

	if cleanupErr != nil {
		dispatchErr = errors.Join(dispatchErr, fmt.Errorf("OneSignal erasure - cleanup: %w", cleanupErr))
	}

	return report, dispatchErr
}

func (uc *UseCase) process(
	ctx context.Context,
	erasure *entity.OneSignalErasure,
	now time.Time,
) (bool, error) {
	externalIDBytes, err := uc.box.Open(erasure.ExternalIDCiphertext)
	if err != nil {
		result := entity.OneSignalErasureProviderResult{
			ReasonCode: "ciphertext_invalid", ReasonDetail: "could not decrypt erasure identifier", Systemic: true,
		}

		return false, uc.recordFailure(ctx, erasure, "delete", result, now, err)
	}

	externalID := string(externalIDBytes)
	if !hmac.Equal([]byte(uc.auditHash(externalID)), []byte(erasure.ExternalIDHash)) {
		result := entity.OneSignalErasureProviderResult{
			ReasonCode:   "ciphertext_binding_mismatch",
			ReasonDetail: "encrypted identifier did not match its audit binding",
			Systemic:     true,
		}

		return false, uc.recordFailure(ctx, erasure, "delete", result, now, errCiphertextBinding)
	}

	var result entity.OneSignalErasureProviderResult

	operation := "delete"

	if erasure.Status == entity.OneSignalErasureStatusVerifying {
		operation = "verify"
		result, err = uc.provider.ViewUser(ctx, externalID)
	} else {
		result, err = uc.provider.DeleteUser(ctx, externalID)
	}

	result.ReasonDetail = sanitizeEvidence(result.ReasonDetail, externalID)

	if err != nil || (!result.Accepted && !result.NotFound) {
		return false, uc.recordFailure(ctx, erasure, operation, result, now, err)
	}

	if result.NotFound {
		return true, uc.recordVerified(ctx, erasure, operation, result, now)
	}

	return false, uc.recordAccepted(ctx, erasure, operation, result, now)
}

func (uc *UseCase) recordAccepted(
	ctx context.Context,
	erasure *entity.OneSignalErasure,
	operation string,
	result entity.OneSignalErasureProviderResult,
	now time.Time,
) error {
	acceptedAt := now
	if erasure.AcceptedAt != nil {
		acceptedAt = *erasure.AcceptedAt
	}

	outcome := "accepted"
	if operation == "verify" {
		outcome = "exists"
	}

	return uc.repo.RecordOneSignalErasureAttempt(ctx, &entity.OneSignalErasureAttempt{
		ID: uuid.NewString(), ErasureID: erasure.ID, LeaseToken: erasure.LeaseToken,
		Operation: operation, Status: entity.OneSignalErasureStatusVerifying,
		HTTPStatus: result.HTTPStatus, ReasonCode: result.ReasonCode, ReasonDetail: result.ReasonDetail,
		NextAttemptAt: now.Add(uc.verificationDelay), AcceptedAt: &acceptedAt,
		AttemptedAt: now, ProviderCallOutcome: outcome,
	})
}

func (uc *UseCase) recordVerified(
	ctx context.Context,
	erasure *entity.OneSignalErasure,
	operation string,
	result entity.OneSignalErasureProviderResult,
	now time.Time,
) error {
	verifiedAt := now

	return uc.repo.RecordOneSignalErasureAttempt(ctx, &entity.OneSignalErasureAttempt{
		ID: uuid.NewString(), ErasureID: erasure.ID, LeaseToken: erasure.LeaseToken,
		Operation: operation, Status: entity.OneSignalErasureStatusVerified,
		HTTPStatus: result.HTTPStatus, ReasonCode: result.ReasonCode, ReasonDetail: result.ReasonDetail,
		NextAttemptAt: now, VerifiedAt: &verifiedAt, ClearExternalID: true,
		AttemptedAt: now, ProviderCallOutcome: "not_found",
	})
}

func (uc *UseCase) recordFailure(
	ctx context.Context,
	erasure *entity.OneSignalErasure,
	operation string,
	result entity.OneSignalErasureProviderResult,
	now time.Time,
	cause error,
) error {
	status := erasure.Status
	if status != entity.OneSignalErasureStatusVerifying {
		status = entity.OneSignalErasureStatusPending
	}

	delay := uc.retryDelay(erasure.AttemptCount + 1)
	retryAfter := min(result.RetryAfter, uc.retryBackoffMax)

	if retryAfter > delay {
		delay = retryAfter
	}

	nextAttempt := now.Add(delay)

	recordErr := uc.repo.RecordOneSignalErasureAttempt(ctx, &entity.OneSignalErasureAttempt{
		ID: uuid.NewString(), ErasureID: erasure.ID, LeaseToken: erasure.LeaseToken,
		Operation: operation, Status: status, HTTPStatus: result.HTTPStatus,
		ReasonCode: boundedReasonCode(result.ReasonCode), ReasonDetail: result.ReasonDetail,
		NextAttemptAt: nextAttempt, AcceptedAt: erasure.AcceptedAt,
		AttemptedAt: now, ProviderCallOutcome: "failed",
	})
	if recordErr != nil {
		return fmt.Errorf("OneSignal erasure - record failure: %w", recordErr)
	}

	if result.Systemic || cause != nil {
		if cause == nil {
			cause = errSystemicProviderResponse
		}

		return fmt.Errorf(
			"OneSignal erasure provider %s failed: %s: %w",
			operation,
			boundedReasonCode(result.ReasonCode),
			providerFailure{cause: cause},
		)
	}

	return nil
}

func (uc *UseCase) retryDelay(attempt int) time.Duration {
	exponent := min(max(attempt-1, 0), maxRetryExponent)

	delay := uc.retryBackoffBase * time.Duration(1<<exponent)
	if delay > uc.retryBackoffMax {
		return uc.retryBackoffMax
	}

	return delay
}

func (uc *UseCase) auditHash(externalID string) string {
	mac := hmac.New(sha256.New, uc.auditKey)
	_, _ = mac.Write([]byte(externalID))

	return hex.EncodeToString(mac.Sum(nil))
}

func boundedReasonCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "provider_rejected"
	}

	if len(value) > maxReasonCodeBytes {
		return value[:maxReasonCodeBytes]
	}

	return value
}

func sanitizeEvidence(detail, externalID string) string {
	detail = strings.TrimSpace(detail)
	if externalID != "" {
		detail = strings.ReplaceAll(detail, externalID, "[redacted-id]")
	}

	detail = jwtLikePattern.ReplaceAllString(detail, "[redacted-jwt]")

	const maxEvidenceRunes = 2000

	runes := []rune(detail)
	if len(runes) > maxEvidenceRunes {
		return string(runes[:maxEvidenceRunes])
	}

	return detail
}
