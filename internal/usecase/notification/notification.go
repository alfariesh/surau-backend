// Package notification composes and delivers push notifications (via OneSignal) for product events
// — khatam milestones, security alerts — and for the scheduled streak/daily reading reminders.
//
// Targeting uses the OneSignal external_id alias (the backend user UUID); the provider owns the
// device-subscription mapping, so there is no device-token storage here. Event methods are
// fire-and-forget: they detach onto a background context and never block (or fail) the caller.
package notification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
)

const (
	eventStreakReminder = "streak_reminder"
	// reminderCooldown bounds one reminder per user per local day even if the hourly dispatch
	// overlaps a restart; it must outlast the evening send window but expire before the next day.
	reminderCooldown = 20 * time.Hour
	// pushTimeout caps a single detached event delivery.
	pushTimeout = 15 * time.Second
	// defaultLang is OneSignal's required fallback language.
	defaultLang = "en"
)

// Pusher delivers a composed notification to a provider.
type Pusher interface {
	Send(ctx context.Context, message entity.PushNotification) error
}

// AccountReader resolves a user's account (for language and notification preferences).
type AccountReader interface {
	GetAccount(ctx context.Context, userID string) (entity.UserAccount, error)
}

// CooldownAcquirer atomically claims a per-key send slot, reused from the auth-notification cooldown
// table so a redeploy mid-window can't double-send.
type CooldownAcquirer interface {
	AcquireAuthNotificationCooldown(ctx context.Context, cooldown entity.AuthNotificationCooldown) (bool, error)
}

// ReminderRepo lists users eligible for a streak/daily reminder, resolved in each user's timezone.
type ReminderRepo interface {
	ReminderCandidates(ctx context.Context) ([]entity.ReminderCandidate, error)
}

// UseCase composes and sends push notifications.
type UseCase struct {
	accounts  AccountReader
	cooldowns CooldownAcquirer
	reminders ReminderRepo
	pusher    Pusher
	log       logger.Interface
}

// New creates a notification usecase. All dependencies are required.
func New(
	accounts AccountReader,
	cooldowns CooldownAcquirer,
	reminders ReminderRepo,
	pusher Pusher,
	l logger.Interface,
) *UseCase {
	return &UseCase{
		accounts:  accounts,
		cooldowns: cooldowns,
		reminders: reminders,
		pusher:    pusher,
		log:       l,
	}
}

// NotifyKhatamCompleted congratulates a user who finished a full khatam. Fire-and-forget.
func (uc *UseCase) NotifyKhatamCompleted(ctx context.Context, userID string) {
	uc.dispatchEvent(ctx, userID, func(account entity.UserAccount) (entity.PushNotification, bool) {
		if !account.Preferences.NotifyKhatamMilestones {
			return entity.PushNotification{}, false
		}
		lang := account.Preferences.PreferredUILang

		return entity.PushNotification{
			ExternalIDs: []string{userID},
			Headings:    localized(lang, khatamCompletedHeadings),
			Contents:    localized(lang, khatamCompletedContents),
		}, true
	})
}

// NotifyKhatamMilestone nudges a user who reached an intermediate juz milestone. Fire-and-forget.
func (uc *UseCase) NotifyKhatamMilestone(ctx context.Context, userID string, juzCount int) {
	if !isKhatamMilestone(juzCount) {
		return
	}
	uc.dispatchEvent(ctx, userID, func(account entity.UserAccount) (entity.PushNotification, bool) {
		if !account.Preferences.NotifyKhatamMilestones {
			return entity.PushNotification{}, false
		}
		lang := account.Preferences.PreferredUILang

		return entity.PushNotification{
			ExternalIDs: []string{userID},
			Headings:    localized(lang, khatamMilestoneHeadings),
			Contents:    localizedf(lang, khatamMilestoneContents, juzCount),
		}, true
	})
}

// NotifyNewLogin warns a user that a new device signed in. Security alert — not gated by the
// product notification categories. Fire-and-forget.
func (uc *UseCase) NotifyNewLogin(ctx context.Context, userID, device, _ string) {
	uc.dispatchEvent(ctx, userID, func(account entity.UserAccount) (entity.PushNotification, bool) {
		lang := account.Preferences.PreferredUILang

		return entity.PushNotification{
			ExternalIDs: []string{userID},
			Headings:    localized(lang, newLoginHeadings),
			Contents:    localizedf(lang, newLoginContents, device),
		}, true
	})
}

// DispatchReminders sends streak/daily reading reminders to all currently-eligible users. Intended
// to be called on a schedule. Returns the number of reminders sent.
func (uc *UseCase) DispatchReminders(ctx context.Context) (int, error) {
	candidates, err := uc.reminders.ReminderCandidates(ctx)
	if err != nil {
		return 0, fmt.Errorf("notification - DispatchReminders - ReminderCandidates: %w", err)
	}

	sent := 0
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			break
		}

		acquired, cErr := uc.cooldowns.AcquireAuthNotificationCooldown(ctx, entity.AuthNotificationCooldown{
			Event:     eventStreakReminder,
			KeyHash:   keyHash(eventStreakReminder, candidate.UserID, candidate.LocalDate),
			ExpiresAt: time.Now().UTC().Add(reminderCooldown),
		})
		if cErr != nil {
			uc.log.Error(fmt.Errorf("notification - DispatchReminders - cooldown: %w", cErr))

			continue
		}
		if !acquired {
			continue
		}

		lang := candidate.Lang
		message := entity.PushNotification{
			ExternalIDs: []string{candidate.UserID},
			Headings:    localized(lang, streakReminderHeadings),
			Contents:    localized(lang, streakReminderContents),
		}
		if sErr := uc.pusher.Send(ctx, message); sErr != nil {
			uc.log.Error(fmt.Errorf("notification - DispatchReminders - send: %w", sErr))

			continue
		}
		sent++
	}

	return sent, nil
}

// dispatchEvent resolves the account, builds the message via compose, and sends it on a detached
// context so the calling request is never blocked or failed by push delivery.
func (uc *UseCase) dispatchEvent(
	_ context.Context,
	userID string,
	compose func(entity.UserAccount) (entity.PushNotification, bool),
) {
	if userID == "" {
		return
	}
	go func() {
		// Detached goroutine: a panic here would kill the whole process
		// (F1-C), so recover and log instead.
		defer func() {
			if r := recover(); r != nil {
				uc.log.Error(fmt.Errorf("notification - dispatchEvent - panic recovered: %v\n%s", r, debug.Stack()))
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
		defer cancel()

		account, err := uc.accounts.GetAccount(ctx, userID)
		if err != nil {
			uc.log.Error(fmt.Errorf("notification - dispatchEvent - GetAccount: %w", err))

			return
		}
		message, ok := compose(account)
		if !ok {
			return
		}
		if err := uc.pusher.Send(ctx, message); err != nil {
			uc.log.Error(fmt.Errorf("notification - dispatchEvent - send: %w", err))
		}
	}()
}

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
