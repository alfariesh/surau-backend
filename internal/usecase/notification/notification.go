// Package notification composes and durably delivers push notifications through OneSignal.
// Every provider request is preceded by a database delivery row; reminder retries additionally
// carry a permanent per-local-day key and the existing 20-hour cooldown.
package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/google/uuid"
)

const (
	reminderCooldown       = 20 * time.Hour
	defaultPushTimeout     = 15 * time.Second
	defaultLeaseDuration   = 30 * time.Second
	eventDeliveryDeadline  = 24 * time.Hour
	maxDeliveryAttempts    = 8
	maxRecoveryPerSweep    = 100
	maxSystemicBatchErrors = 5
	eventContextMultiplier = 2
	deliveryRecordTimeout  = 5 * time.Second
	retryBackoffBase       = 30 * time.Second
	retryBackoffMax        = 15 * time.Minute
	retryJitterFraction    = 0.2
	defaultLang            = "en"
)

var (
	errNotificationRecoveryBudget = errors.New("notification recovery sweep budget exhausted")
	errNotificationDispatchPanic  = errors.New("notification event dispatch panicked")
)

// Pusher delivers a composed notification to a provider.
type Pusher interface {
	Send(
		ctx context.Context,
		message entity.PushNotification,
		idempotencyKey string,
	) (entity.PushDeliveryResult, error)
}

// AccountReader resolves a user's account (for language and notification preferences).
type AccountReader interface {
	GetAccount(ctx context.Context, userID string) (entity.UserAccount, error)
}

// DeliveryRepo owns timezone-safe candidate selection and all durable delivery transitions.
type DeliveryRepo interface {
	ReminderCandidates(
		ctx context.Context,
		asOf time.Time,
		quietStart,
		quietEnd string,
	) (entity.ReminderCandidatesResult, error)
	ClaimReminderDelivery(
		ctx context.Context,
		claim *entity.ReminderDeliveryClaim,
		asOf time.Time,
	) (entity.NotificationDelivery, bool, string, error)
	CreateEventDelivery(
		ctx context.Context,
		create *entity.NotificationDeliveryCreate,
		asOf time.Time,
	) (entity.NotificationDelivery, error)
	ClaimPendingEventDeliveries(
		ctx context.Context,
		asOf time.Time,
		leaseToken string,
		leaseExpiresAt time.Time,
		limit int,
	) ([]entity.NotificationDelivery, error)
	ClaimPendingReminderDeliveries(
		ctx context.Context,
		asOf time.Time,
		quietStart,
		quietEnd,
		leaseToken string,
		leaseExpiresAt time.Time,
		limit int,
	) ([]entity.NotificationDelivery, error)
	ExpireNotificationDeliveries(ctx context.Context, asOf time.Time) (int64, error)
	FailNotificationDelivery(ctx context.Context, deliveryID, leaseToken, reasonCode string, asOf time.Time) error
	NextNotificationRetryAt(ctx context.Context, asOf time.Time) (time.Time, error)
	RecordNotificationDeliveryAttempt(ctx context.Context, attempt *entity.NotificationDeliveryAttempt) error
	RecordReminderSkips(ctx context.Context, skips map[string]int64, asOf time.Time) error
}

// UseCase composes and sends push notifications.
type UseCase struct {
	accounts      AccountReader
	deliveries    DeliveryRepo
	pusher        Pusher
	retryWake     chan struct{}
	asyncErrors   chan error
	quietStart    string
	quietEnd      string
	pushTimeout   time.Duration
	leaseDuration time.Duration
	now           func() time.Time
	log           logger.Interface
	allowedTypes  map[string]bool
	ownerBinding  func(string, int64) string
}

// Options carries operator-configurable quiet hours plus deterministic test seams.
type Options struct {
	QuietStart    string
	QuietEnd      string
	PushTimeout   time.Duration
	LeaseDuration time.Duration
	Now           func() time.Time
	AllowedTypes  []string
	OwnerBinding  func(string, int64) string
}

// DispatchReport is one supervised sweep, separated from provider health errors.
type DispatchReport struct {
	Accepted           int
	Failed             int
	Expired            int64
	RecoveredEvents    int
	RecoveredReminders int
	Skipped            map[string]int64
}

// New creates a notification usecase. All dependencies are required.
func New(
	accounts AccountReader,
	deliveries DeliveryRepo,
	pusher Pusher,
	opts Options,
	l logger.Interface,
) *UseCase {
	if opts.QuietStart == "" {
		opts.QuietStart = "21:00"
	}

	if opts.QuietEnd == "" {
		opts.QuietEnd = "07:00"
	}

	if opts.PushTimeout <= 0 {
		opts.PushTimeout = defaultPushTimeout
	}

	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = defaultLeaseDuration
	}

	if opts.Now == nil {
		opts.Now = time.Now
	}

	allowedTypes := make(map[string]bool, len(opts.AllowedTypes))
	for _, notificationType := range opts.AllowedTypes {
		allowedTypes[notificationType] = true
	}
	return &UseCase{
		accounts:      accounts,
		deliveries:    deliveries,
		pusher:        pusher,
		retryWake:     make(chan struct{}, 1),
		asyncErrors:   make(chan error, 1),
		quietStart:    opts.QuietStart,
		quietEnd:      opts.QuietEnd,
		pushTimeout:   opts.PushTimeout,
		leaseDuration: opts.LeaseDuration,
		now:           opts.Now,
		log:           l,
		allowedTypes:  allowedTypes,
		ownerBinding:  opts.OwnerBinding,
	}
}

// RetryWakeups lets the existing F1-C reminder supervisor interrupt its ordinary polling sleep
// when an asynchronous event push creates durable retry work. The channel is edge-triggered and
// coalesced so a burst of failures cannot grow an in-memory queue.
func (uc *UseCase) RetryWakeups() <-chan struct{} {
	return uc.retryWake
}

// SetOwnerBinding wires the account-generation signer after the identity usecase is initialized.
func (uc *UseCase) SetOwnerBinding(binding func(string, int64) string) {
	uc.ownerBinding = binding
}

func (uc *UseCase) wakeRetryLoop() {
	select {
	case uc.retryWake <- struct{}{}:
	default:
	}
}

func (uc *UseCase) reportAsyncError(err error) {
	if err == nil {
		return
	}

	select {
	case uc.asyncErrors <- err:
	default:
	}

	uc.wakeRetryLoop()
}

func (uc *UseCase) takeAsyncError() error {
	select {
	case err := <-uc.asyncErrors:
		return err
	default:
		return nil
	}
}

// NotifyKhatamCompleted congratulates a user who finished a full khatam. Fire-and-forget.
func (uc *UseCase) NotifyKhatamCompleted(ctx context.Context, userID string) {
	uc.dispatchEvent(ctx, userID, entity.NotificationTypeKhatamCompleted, func(account entity.UserAccount) (entity.PushNotification, bool) {
		if !account.Preferences.NotifyKhatamMilestones {
			return entity.PushNotification{}, false
		}
		lang := account.Preferences.PreferredUILang

		return entity.PushNotification{
			ExternalIDs: []string{userID},
			Headings:    localized(lang, khatamCompletedHeadings),
			Contents:    localized(lang, khatamCompletedContents),
			Data:        uc.personalKhatamData(&account),
		}, true
	})
}

// NotifyKhatamMilestone nudges a user who reached an intermediate juz milestone. Fire-and-forget.
func (uc *UseCase) NotifyKhatamMilestone(ctx context.Context, userID string, juzCount int) {
	if !isKhatamMilestone(juzCount) {
		return
	}

	uc.dispatchEvent(ctx, userID, entity.NotificationTypeKhatamMilestone, func(account entity.UserAccount) (entity.PushNotification, bool) {
		if !account.Preferences.NotifyKhatamMilestones {
			return entity.PushNotification{}, false
		}
		lang := account.Preferences.PreferredUILang

		return entity.PushNotification{
			ExternalIDs: []string{userID},
			Headings:    localized(lang, khatamMilestoneHeadings),
			Contents:    localizedf(lang, khatamMilestoneContents, juzCount),
			Data:        uc.personalKhatamData(&account),
		}, true
	})
}

// NotifyNewLogin warns a user that a new device signed in. Security alert — not gated by the
// product notification categories. Fire-and-forget.
func (uc *UseCase) NotifyNewLogin(ctx context.Context, userID, device, _ string) {
	uc.dispatchEvent(ctx, userID, entity.NotificationTypeNewLogin, func(account entity.UserAccount) (entity.PushNotification, bool) {
		lang := account.Preferences.PreferredUILang

		return entity.PushNotification{
			ExternalIDs: []string{userID},
			Headings:    localized(lang, newLoginHeadings),
			Contents:    localizedf(lang, newLoginContents, device),
		}, true
	})
}

// DispatchReminders recovers orphaned event deliveries, expires stale retries, and sends eligible
// streak reminders. Systemic provider failures are returned so the F1-C supervisor backs off and
// does not advance the loop's last-success timestamp.
func (uc *UseCase) DispatchReminders(ctx context.Context) (report DispatchReport, returnErr error) {
	now := uc.now().UTC()
	report.Skipped = make(map[string]int64)

	defer func() {
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryRecordTimeout)
		defer cancel()

		if err := uc.deliveries.RecordReminderSkips(recordCtx, report.Skipped, now); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("notification - DispatchReminders - skips: %w", err))
		}
	}()

	if err := uc.takeAsyncError(); err != nil {
		return report, fmt.Errorf("notification - DispatchReminders - asynchronous event: %w", err)
	}

	expired, err := uc.deliveries.ExpireNotificationDeliveries(ctx, now)
	if err != nil {
		return report, fmt.Errorf("notification - DispatchReminders - expire: %w", err)
	}

	report.Expired = expired

	state := dispatchState{report: &report}
	if err := uc.recoverDeliveries(ctx, &state); err != nil {
		return report, err
	}

	if err := uc.dispatchNewReminders(ctx, &report, &state); err != nil {
		return report, err
	}

	if err := state.err(); err != nil {
		return report, err
	}

	if err := uc.waitForDurableRetry(ctx); err != nil {
		return report, err
	}

	return report, nil
}

func (uc *UseCase) recoverDeliveries(ctx context.Context, state *dispatchState) error {
	if err := uc.recoverEventDeliveries(ctx, state); err != nil {
		return err
	}

	if state.stop {
		return state.err()
	}

	if err := uc.recoverReminderDeliveries(ctx, state); err != nil {
		return err
	}

	if state.stop {
		return state.err()
	}

	return nil
}

func (uc *UseCase) dispatchNewReminders(
	ctx context.Context,
	report *DispatchReport,
	state *dispatchState,
) error {
	candidateResult, err := uc.deliveries.ReminderCandidates(
		ctx,
		uc.now().UTC(),
		uc.quietStart,
		uc.quietEnd,
	)
	if err != nil {
		return errors.Join(state.err(), fmt.Errorf("notification - DispatchReminders - ReminderCandidates: %w", err))
	}

	report.Skipped["missing_timezone"] += candidateResult.MissingTimezoneSkipped
	report.Skipped["invalid_timezone"] += candidateResult.InvalidTimezoneSkipped

	if err := uc.dispatchReminderCandidates(ctx, candidateResult.Candidates, state); err != nil {
		return errors.Join(state.err(), err)
	}

	return state.err()
}

func (uc *UseCase) waitForDurableRetry(ctx context.Context) error {
	retryNow := uc.now().UTC()

	nextRetryAt, err := uc.deliveries.NextNotificationRetryAt(ctx, retryNow)
	if err != nil {
		return fmt.Errorf("notification - DispatchReminders - next retry: %w", err)
	}

	if nextRetryAt.After(retryNow) {
		return &scheduledRetryError{retryAfter: nextRetryAt.Sub(retryNow)}
	}

	return nil
}

type dispatchState struct {
	report           *DispatchReport
	systemicErr      error
	systemicFailures int
	maxRetryAfter    time.Duration
	stop             bool
}

func (s *dispatchState) observe(status string, deliveryErr error) error {
	switch status {
	case entity.NotificationStatusAccepted:
		s.report.Accepted++
	case entity.NotificationStatusFailed:
		s.report.Failed++
	}

	if deliveryErr == nil {
		return nil
	}

	var providerErr *providerDeliveryError
	if !errors.As(deliveryErr, &providerErr) {
		return deliveryErr
	}

	s.systemicErr = errors.Join(s.systemicErr, deliveryErr)
	s.systemicFailures++

	if providerErr.retryAfter > s.maxRetryAfter {
		s.maxRetryAfter = providerErr.retryAfter
	}

	s.stop = providerErr.reasonCode == "rate_limited" ||
		providerErr.reasonCode == "unauthorized" ||
		providerErr.reasonCode == "invalid_configuration" ||
		s.systemicFailures >= maxSystemicBatchErrors

	return nil
}

func (s *dispatchState) err() error {
	if s.systemicErr == nil {
		return nil
	}

	return &dispatchError{cause: s.systemicErr, retryAfter: s.maxRetryAfter}
}

func (uc *UseCase) recoverEventDeliveries(ctx context.Context, state *dispatchState) error {
	for range maxRecoveryPerSweep {
		now := uc.now().UTC()

		recovered, err := uc.deliveries.ClaimPendingEventDeliveries(
			ctx,
			now,
			uuid.NewString(),
			now.Add(uc.leaseDuration),
			1,
		)
		if err != nil {
			return fmt.Errorf("notification - DispatchReminders - recover events: %w", err)
		}

		if len(recovered) == 0 {
			return nil
		}

		status, deliveryErr := uc.deliver(ctx, &recovered[0])
		state.report.RecoveredEvents++

		if err := state.observe(status, deliveryErr); err != nil {
			return err
		}

		if state.stop {
			return nil
		}
	}

	return errNotificationRecoveryBudget
}

func (uc *UseCase) recoverReminderDeliveries(ctx context.Context, state *dispatchState) error {
	for range maxRecoveryPerSweep {
		now := uc.now().UTC()

		recovered, err := uc.deliveries.ClaimPendingReminderDeliveries(
			ctx,
			now,
			uc.quietStart,
			uc.quietEnd,
			uuid.NewString(),
			now.Add(uc.leaseDuration),
			1,
		)
		if err != nil {
			return fmt.Errorf("notification - DispatchReminders - recover reminders: %w", err)
		}

		if len(recovered) == 0 {
			return nil
		}

		status, deliveryErr := uc.deliver(ctx, &recovered[0])
		state.report.RecoveredReminders++

		if err := state.observe(status, deliveryErr); err != nil {
			return err
		}

		if state.stop {
			return nil
		}
	}

	return errNotificationRecoveryBudget
}

func (uc *UseCase) dispatchReminderCandidates(
	ctx context.Context,
	candidates []entity.ReminderCandidate,
	state *dispatchState,
) error {
	for i := range candidates {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := uc.dispatchReminderCandidate(ctx, &candidates[i], state); err != nil {
			return err
		}

		if state.stop {
			return nil
		}
	}

	return nil
}

func (uc *UseCase) dispatchReminderCandidate(
	ctx context.Context,
	candidate *entity.ReminderCandidate,
	state *dispatchState,
) error {
	now := uc.now().UTC()
	if !candidate.DeliveryDeadlineAt.IsZero() && !now.Before(candidate.DeliveryDeadlineAt) {
		state.report.Skipped["delivery_window_expired"]++

		return nil
	}

	claim := uc.newReminderClaim(candidate, now)

	delivery, claimed, skipReason, err := uc.deliveries.ClaimReminderDelivery(ctx, claim, now)
	if err != nil {
		return fmt.Errorf("notification - DispatchReminders - claim: %w", err)
	}

	if !claimed {
		if skipReason != "" && skipReason != "leased" && skipReason != "retry_not_due" {
			state.report.Skipped[skipReason]++
		}

		return nil
	}

	status, deliveryErr := uc.deliver(ctx, &delivery)

	return state.observe(status, deliveryErr)
}

func (uc *UseCase) newReminderClaim(
	candidate *entity.ReminderCandidate,
	now time.Time,
) *entity.ReminderDeliveryClaim {
	message := entity.PushNotification{
		ExternalIDs: []string{candidate.UserID},
		Headings:    localized(candidate.Lang, streakReminderHeadings),
		Contents:    localized(candidate.Lang, streakReminderContents),
	}

	return &entity.ReminderDeliveryClaim{
		Delivery: entity.NotificationDeliveryCreate{
			ID:                 uuid.NewString(),
			UserID:             candidate.UserID,
			NotificationType:   entity.NotificationTypeStreakReminder,
			LocalDate:          candidate.LocalDate,
			Payload:            message,
			IdempotencyKey:     uuid.NewString(),
			LeaseToken:         uuid.NewString(),
			LeaseExpiresAt:     now.Add(uc.leaseDuration),
			DeliveryDeadlineAt: candidate.DeliveryDeadlineAt,
		},
		CooldownKeyHash:       keyHash(entity.NotificationTypeStreakReminder, candidate.UserID),
		LegacyCooldownKeyHash: keyHash(entity.NotificationTypeStreakReminder, candidate.UserID, candidate.LocalDate),
		CooldownExpiresAt:     now.Add(reminderCooldown),
	}
}

// dispatchEvent resolves the account, persists the composed payload, then attempts an immediate
// send on a detached context. The supervised reminder pass later recovers a pending/retrying row.
func (uc *UseCase) dispatchEvent(
	_ context.Context,
	userID string,
	notificationType string,
	compose func(entity.UserAccount) (entity.PushNotification, bool),
) {
	if userID == "" || !uc.allowed(notificationType) {
		return
	}
	go func() {
		persisted := false

		// Detached goroutine: a panic here would kill the whole process
		// (F1-C), so recover and log instead.
		defer func() {
			if r := recover(); r != nil {
				panicErr := fmt.Errorf("%w: %v", errNotificationDispatchPanic, r)
				uc.log.Error("%v\n%s", panicErr, debug.Stack())

				if persisted {
					uc.wakeRetryLoop()
				} else {
					uc.reportAsyncError(panicErr)
				}
			}
		}()

		ctx, cancel := context.WithTimeout(
			context.Background(),
			eventContextMultiplier*uc.pushTimeout,
		)
		defer cancel()

		account, err := uc.accounts.GetAccount(ctx, userID)
		if err != nil {
			uc.log.Error(fmt.Errorf("notification - dispatchEvent - GetAccount: %w", err))

			if !errors.Is(err, entity.ErrUserNotFound) {
				uc.reportAsyncError(err)
			}

			return
		}
		message, ok := compose(account)
		if !ok {
			return
		}

		now := uc.now().UTC()

		delivery, err := uc.deliveries.CreateEventDelivery(ctx, &entity.NotificationDeliveryCreate{
			ID:                 uuid.NewString(),
			UserID:             userID,
			NotificationType:   notificationType,
			Payload:            message,
			IdempotencyKey:     uuid.NewString(),
			LeaseToken:         uuid.NewString(),
			LeaseExpiresAt:     now.Add(uc.leaseDuration),
			DeliveryDeadlineAt: now.Add(eventDeliveryDeadline),
		}, now)
		if err != nil {
			uc.log.Error(fmt.Errorf("notification - dispatchEvent - persist: %w", err))
			uc.reportAsyncError(err)

			return
		}

		persisted = true

		if _, err := uc.deliver(ctx, &delivery); err != nil {
			uc.log.Error(fmt.Errorf("notification - dispatchEvent - deliver: %w", err))
			uc.wakeRetryLoop()
		}
	}()
}

func (uc *UseCase) deliver(ctx context.Context, delivery *entity.NotificationDelivery) (string, error) {
	now := uc.now().UTC()

	expired, err := uc.expireReminderBeforeProvider(ctx, delivery, now)
	if err != nil {
		return "", err
	}

	if expired {
		return entity.NotificationStatusFailed, nil
	}

	var result entity.PushDeliveryResult

	var sendErr error

	if !uc.allowed(delivery.NotificationType) {
		result = entity.PushDeliveryResult{
			Outcome: entity.PushDeliveryFailed, ReasonCode: "ineligible_category",
			ReasonDetail: "notification category is not eligible for this app", Retryable: false,
		}
	} else {
		providerCtx, cancel := context.WithTimeout(ctx, uc.pushTimeout)
		result, sendErr = uc.pusher.Send(providerCtx, delivery.Payload, delivery.IdempotencyKey)

		cancel()
	}

	normalizeProviderResult(&result, sendErr)
	terminal := uc.deliveryIsTerminal(delivery, &result)
	occurredAt := uc.now().UTC()
	nextAttemptAt, retryDelay := notificationAttemptSchedule(delivery, &result, terminal, occurredAt)

	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryRecordTimeout)
	defer recordCancel()

	recordErr := uc.deliveries.RecordNotificationDeliveryAttempt(recordCtx, &entity.NotificationDeliveryAttempt{
		ID:                     uuid.NewString(),
		DeliveryID:             delivery.ID,
		LeaseToken:             delivery.LeaseToken,
		Outcome:                result.Outcome,
		Retryable:              result.Retryable,
		Systemic:               result.Systemic,
		Terminal:               terminal,
		HTTPStatus:             result.HTTPStatus,
		RetryAfter:             result.RetryAfter,
		ProviderNotificationID: result.ProviderNotificationID,
		ReasonCode:             result.ReasonCode,
		ReasonDetail:           result.ReasonDetail,
		OccurredAt:             occurredAt,
		NextAttemptAt:          nextAttemptAt,
	})
	if recordErr != nil {
		return "", fmt.Errorf("notification - deliver - record: %w", recordErr)
	}

	status := providerResultStatus(&result, terminal)
	if result.Systemic || result.Retryable || sendErr != nil {
		return status, &providerDeliveryError{
			reasonCode: result.ReasonCode,
			retryAfter: retryDelay,
			cause:      sendErr,
		}
	}

	return status, nil
}

func (uc *UseCase) allowed(notificationType string) bool {
	return len(uc.allowedTypes) == 0 || uc.allowedTypes[notificationType]
}

func (uc *UseCase) personalKhatamData(account *entity.UserAccount) map[string]string {
	if uc.ownerBinding == nil {
		return nil
	}

	return map[string]string{
		"schema_version": entity.PushDataSchemaV1,
		"scope":          "personal",
		"category":       "notify_khatam_milestones",
		"intent":         "open_khatam_progress",
		"owner_binding":  uc.ownerBinding(account.ID, account.TokenVersion),
	}
}

func (uc *UseCase) expireReminderBeforeProvider(
	ctx context.Context,
	delivery *entity.NotificationDelivery,
	now time.Time,
) (bool, error) {
	if delivery.NotificationType != entity.NotificationTypeStreakReminder ||
		delivery.DeliveryDeadlineAt.IsZero() || now.Before(delivery.DeliveryDeadlineAt) {
		return false, nil
	}

	recordCtx, recordCancel := context.WithTimeout(context.WithoutCancel(ctx), deliveryRecordTimeout)
	defer recordCancel()

	if err := uc.deliveries.FailNotificationDelivery(
		recordCtx,
		delivery.ID,
		delivery.LeaseToken,
		"delivery_window_expired",
		now,
	); err != nil {
		return false, fmt.Errorf("notification - deliver - expire window: %w", err)
	}

	return true, nil
}

func notificationAttemptSchedule(
	delivery *entity.NotificationDelivery,
	result *entity.PushDeliveryResult,
	terminal bool,
	occurredAt time.Time,
) (time.Time, time.Duration) {
	if terminal {
		return occurredAt, 0
	}

	retryDelay := notificationRetryDelay(delivery.AttemptCount+1, result.RetryAfter)

	nextAttemptAt := occurredAt.Add(retryDelay)
	if !delivery.DeliveryDeadlineAt.IsZero() && nextAttemptAt.After(delivery.DeliveryDeadlineAt) {
		nextAttemptAt = delivery.DeliveryDeadlineAt
		retryDelay = max(0, nextAttemptAt.Sub(occurredAt))
	}

	return nextAttemptAt, retryDelay
}

func notificationRetryDelay(attemptNumber int, providerHint time.Duration) time.Duration {
	delay := retryBackoffBase
	for attempt := 1; attempt < attemptNumber && delay < retryBackoffMax; attempt++ {
		delay *= 2
	}

	if delay > retryBackoffMax {
		delay = retryBackoffMax
	}

	jitter := 1 - retryJitterFraction + 2*retryJitterFraction*rand.Float64() //nolint:gosec // retry jitter

	delay = max(providerHint, min(time.Duration(float64(delay)*jitter), retryBackoffMax))

	return delay
}

func normalizeProviderResult(result *entity.PushDeliveryResult, sendErr error) {
	if result.Outcome == "" {
		result.Outcome = entity.PushDeliveryFailed
	}

	if result.ReasonCode == "" && result.Outcome == entity.PushDeliveryAccepted {
		result.ReasonCode = "accepted"
	}

	if result.ReasonCode == "" {
		result.ReasonCode = "provider_error"
	}

	if sendErr != nil && result.ReasonDetail == "" {
		result.ReasonDetail = "provider request failed"
	}
}

func (uc *UseCase) deliveryIsTerminal(
	delivery *entity.NotificationDelivery,
	result *entity.PushDeliveryResult,
) bool {
	return result.Outcome == entity.PushDeliveryAccepted ||
		!result.Retryable || delivery.AttemptCount+1 >= maxDeliveryAttempts
}

func providerResultStatus(result *entity.PushDeliveryResult, terminal bool) string {
	if result.Outcome == entity.PushDeliveryAccepted {
		return entity.NotificationStatusAccepted
	}

	if terminal {
		return entity.NotificationStatusFailed
	}

	return entity.NotificationStatusRetrying
}

type providerDeliveryError struct {
	reasonCode string
	retryAfter time.Duration
	cause      error
}

func (e *providerDeliveryError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("OneSignal %s: %v", e.reasonCode, e.cause)
	}

	return "OneSignal " + e.reasonCode
}

func (e *providerDeliveryError) Unwrap() error { return e.cause }

// RetryAfter lets the generic F1-C supervisor honor provider backoff hints without depending on
// this package's concrete error type.
func (e *providerDeliveryError) RetryAfter() time.Duration { return e.retryAfter }

type dispatchError struct {
	cause      error
	retryAfter time.Duration
}

func (e *dispatchError) Error() string             { return e.cause.Error() }
func (e *dispatchError) Unwrap() error             { return e.cause }
func (e *dispatchError) RetryAfter() time.Duration { return e.retryAfter }

type scheduledRetryError struct {
	retryAfter time.Duration
}

func (e *scheduledRetryError) Error() string {
	return fmt.Sprintf("notification retry scheduled in %s", e.retryAfter.Round(time.Second))
}

func (e *scheduledRetryError) RetryAfter() time.Duration { return e.retryAfter }

func isKhatamMilestone(juzCount int) bool {
	return juzCount == 10 || juzCount == 20
}

// localized returns OneSignal headings/contents with the English default plus the user's language
// when it differs and is available.
func localized(lang string, table map[string]string) map[string]string {
	out := map[string]string{defaultLang: table[defaultLang]}
	if lang != "" && lang != defaultLang {
		if value, ok := table[lang]; ok {
			out[lang] = value
		}
	}

	return out
}

// localizedf is localized for format-string tables, applying the argument to each language.
func localizedf(lang string, table map[string]string, arg any) map[string]string {
	out := map[string]string{defaultLang: fmt.Sprintf(table[defaultLang], arg)}
	if lang != "" && lang != defaultLang {
		if value, ok := table[lang]; ok {
			out[lang] = fmt.Sprintf(value, arg)
		}
	}

	return out
}

func keyHash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}

	return hex.EncodeToString(h.Sum(nil))
}

var (
	khatamCompletedHeadings = map[string]string{
		"en": "Alhamdulillah! 🎉",
		"id": "Alhamdulillah! 🎉",
		"ar": "الحمد لله! 🎉",
	}
	khatamCompletedContents = map[string]string{
		"en": "You completed a full khatam of the Qur'an. May Allah accept it. Start a new cycle?",
		"id": "Kamu telah menyelesaikan satu khatam Al-Qur'an. Semoga Allah menerimanya. Mulai siklus baru?",
		"ar": "أتممت ختمة كاملة للقرآن الكريم. تقبّل الله منك. هل تبدأ دورة جديدة؟",
	}
	khatamMilestoneHeadings = map[string]string{
		"en": "Khatam progress 💪",
		"id": "Progres khatam 💪",
		"ar": "تقدّم الختمة 💪",
	}
	khatamMilestoneContents = map[string]string{
		"en": "You've reached %d juz toward your khatam. Keep going!",
		"id": "Kamu sudah mencapai %d juz menuju khatam. Lanjutkan!",
		"ar": "وصلت إلى %d جزءًا في ختمتك. واصل!",
	}
	newLoginHeadings = map[string]string{
		"en": "New sign-in",
		"id": "Login baru",
		"ar": "تسجيل دخول جديد",
	}
	newLoginContents = map[string]string{
		"en": "A new sign-in to your Surau account from %s. Not you? Review your sessions.",
		"id": "Ada login baru ke akun Surau-mu dari %s. Bukan kamu? Tinjau sesi.",
		"ar": "تم تسجيل دخول جديد إلى حسابك في سوراو من %s. ليس أنت؟ راجع جلساتك.",
	}
	streakReminderHeadings = map[string]string{
		"en": "🔥 Keep your streak alive",
		"id": "🔥 Jaga streak-mu",
		"ar": "🔥 حافظ على تتابعك",
	}
	streakReminderContents = map[string]string{
		"en": "You haven't read yet today. Open Surau and keep your streak going.",
		"id": "Kamu belum membaca hari ini. Buka Surau dan jaga streak-mu tetap menyala.",
		"ar": "لم تقرأ اليوم بعد. افتح سوراو وحافظ على استمرار تتابعك.",
	}
)
