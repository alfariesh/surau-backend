// Package app configures and runs application.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alfariesh/surau-backend/config"
	"github.com/alfariesh/surau-backend/internal/controller/restapi"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/repo/webapi"
	"github.com/alfariesh/surau-backend/internal/usecase/bookrag"
	"github.com/alfariesh/surau-backend/internal/usecase/editorial"
	emailusecase "github.com/alfariesh/surau-backend/internal/usecase/email"
	"github.com/alfariesh/surau-backend/internal/usecase/notification"
	"github.com/alfariesh/surau-backend/internal/usecase/personal"
	"github.com/alfariesh/surau-backend/internal/usecase/quran"
	"github.com/alfariesh/surau-backend/internal/usecase/reader"
	"github.com/alfariesh/surau-backend/internal/usecase/user"
	"github.com/alfariesh/surau-backend/pkg/httpserver"
	"github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/alfariesh/surau-backend/pkg/postgres"
)

type useCases struct {
	user         *user.UseCase
	reader       *reader.UseCase
	bookRAG      *bookrag.UseCase
	quran        *quran.UseCase
	personal     *personal.UseCase
	editorial    *editorial.UseCase
	email        *emailusecase.UseCase
	notification *notification.UseCase
}

type servers struct {
	http                     *httpserver.Server
	emailDispatcherStop      context.CancelFunc
	authCleanupStop          context.CancelFunc
	authAlertStop            context.CancelFunc
	notificationReminderStop context.CancelFunc
}

func initUseCases(cfg *config.Config, pg *postgres.Postgres, jwtManager *jwt.Manager, l logger.Interface) useCases {
	userRepo := persistent.NewUserRepo(pg)
	readerRepo := persistent.NewReaderRepo(pg)
	bookRAGRepo := persistent.NewBookRAGRepo(pg)
	quranRepo := persistent.NewQuranRepo(pg)
	personalRepo := persistent.NewPersonalRepo(pg)
	editorialRepo := persistent.NewEditorialRepo(pg)
	emailRepo := persistent.NewEmailRepo(pg)
	llmClient := webapi.NewOpenAICompatibleClient(webapi.OpenAICompatibleOptions{
		BaseURL:     cfg.RAG.LLMBaseURL,
		APIKey:      cfg.RAG.LLMAPIKey,
		Model:       cfg.RAG.LLMModel,
		Timeout:     cfg.RAG.LLMTimeout,
		MaxTokens:   cfg.RAG.LLMMaxTokens,
		Temperature: cfg.RAG.LLMTemperature,
	})
	var emailSender repo.EmailSender
	if cfg.Email.DeliveryMode == config.EmailDeliveryModeLog {
		emailSender = webapi.NewLogEmailSender()
	} else {
		emailSender = webapi.NewCloudflareEmailClient(webapi.CloudflareEmailOptions{
			AccountID:   cfg.Email.CloudflareAccountID,
			APIToken:    cfg.Email.CloudflareAPIToken,
			FromAddress: cfg.Email.FromAddress,
			FromName:    cfg.Email.FromName,
			ReplyTo:     cfg.Email.ReplyTo,
			Timeout:     cfg.Email.HTTPTimeout,
		})
	}
	var emailEventPoller repo.EmailEventPoller
	if cfg.Email.DeliveryMode == config.EmailDeliveryModeCloudflare && cfg.Email.CloudflareEventPollingEnabled {
		emailEventPoller = webapi.NewCloudflareEmailEventsClient(webapi.CloudflareEmailEventsOptions{
			APIToken: cfg.Email.CloudflareAnalyticsAPIToken,
			Timeout:  cfg.Email.HTTPTimeout,
		})
	}
	emailUC := emailusecase.New(emailRepo, emailSender, emailusecase.Options{
		SupportEmail:              cfg.Email.ReplyTo,
		UnsubscribeURL:            unsubscribeFrontendURL(cfg),
		UnsubscribeHeaderURL:      cfg.Email.UnsubscribePublicURL,
		UnsubscribeTokenKeyID:     unsubscribeTokenKeyID(cfg),
		UnsubscribeTokenSeed:      unsubscribeTokenSeed(cfg),
		UnsubscribeTokenSecrets:   unsubscribeTokenSecrets(cfg),
		CloudflareEventPoller:     emailEventPoller,
		CloudflarePollingZoneID:   cfg.Email.CloudflareZoneID,
		CloudflarePollingLookback: cfg.Email.CloudflareEventPollingLookback,
		CloudflarePollingLimit:    cfg.Email.CloudflareEventPollingLimit,
	})
	var rateLimiter repo.AuthRateLimitRepo
	if cfg.AuthRateLimit.Enabled {
		rateLimiter = userRepo
	}

	var lockoutRepo repo.AuthLockoutRepo
	if cfg.AuthLockout.Enabled {
		lockoutRepo = userRepo
	}

	// OneSignal push notifications. When disabled, the notifier and the typed-interface handles stay
	// nil so the khatam/login hooks and the reminder cron are no-ops. Keeping the handles as the
	// consumer interface types (not the concrete *UseCase) avoids the nil-pointer-in-interface trap.
	var notificationUC *notification.UseCase
	var (
		khatamNotifier personal.Notifier
		loginNotifier  user.PushNotifier
	)
	if cfg.OneSignal.Enabled {
		notificationUC = notification.New(
			userRepo,
			userRepo,
			personalRepo,
			webapi.NewOneSignalClient(webapi.OneSignalOptions{
				AppID:      cfg.OneSignal.AppID,
				RESTAPIKey: cfg.OneSignal.RESTAPIKey,
				Timeout:    cfg.OneSignal.HTTPTimeout,
			}),
			l,
		)
		khatamNotifier = notificationUC
		loginNotifier = notificationUC
	}

	return useCases{
		user: user.New(userRepo, jwtManager, emailSender, user.Options{
			VerifyFrontendURL:        cfg.Email.VerifyFrontendURL,
			VerificationTTL:          cfg.Email.VerificationTTL,
			VerificationOTPTTL:       cfg.Email.VerificationOTPTTL,
			ResendCooldown:           cfg.Email.ResendCooldown,
			PasswordResetFrontendURL: cfg.Email.PasswordResetFrontendURL,
			PasswordResetTTL:         cfg.Email.PasswordResetTTL,
			PasswordResetCooldown:    cfg.Email.PasswordResetCooldown,
			EmailChangeFrontendURL:   cfg.Email.EmailChangeFrontendURL,
			EmailChangeTTL:           cfg.Email.EmailChangeTTL,
			EmailChangeOTPTTL:        cfg.Email.EmailChangeOTPTTL,
			EmailChangeCooldown:      cfg.Email.EmailChangeCooldown,
			SupportEmail:             cfg.Email.ReplyTo,
			EmailService:             emailUC,
			PushNotifier:             loginNotifier,
			RateLimiter:              rateLimiter,
			AuditLogger:              userRepo,
			Sessions:                 userRepo,
			Lockout:                  lockoutRepo,
			Maintenance:              userRepo,
			RefreshTokenTTL:          cfg.JWT.RefreshTokenExpiry,
			LockoutOptions: user.LockoutOptions{
				Enabled:      cfg.AuthLockout.Enabled,
				Threshold:    cfg.AuthLockout.Threshold,
				BaseDuration: cfg.AuthLockout.BaseDuration,
				Factor:       cfg.AuthLockout.Factor,
				MaxDuration:  cfg.AuthLockout.MaxDuration,
			},
			Cleanup: user.CleanupOptions{
				TokenRetention:   cfg.AuthCleanup.TokenRetention,
				SessionRetention: cfg.AuthCleanup.SessionRetention,
				AuditRetention:   cfg.AuthCleanup.AuditRetention,
			},
			EmailNotifications: user.EmailNotificationOptions{
				Enabled:                cfg.AuthEmail.NotificationsEnabled,
				NewLoginEnabled:        cfg.AuthEmail.NewLoginEnabled,
				FailedLoginEnabled:     cfg.AuthEmail.FailedLoginEnabled,
				PasswordChangedEnabled: cfg.AuthEmail.PasswordChangedEnabled,
				EmailVerifiedEnabled:   cfg.AuthEmail.EmailVerifiedEnabled,
				RoleChangedEnabled:     cfg.AuthEmail.RoleChangedEnabled,
				EmailChangedEnabled:    cfg.AuthEmail.EmailChangedEnabled,
				AccountDeletedEnabled:  cfg.AuthEmail.AccountDeletedEnabled,
				FailedLoginCooldown:    cfg.AuthEmail.FailedLoginCooldown,
			},
			Alert: user.AlertOptions{
				Enabled:    cfg.AuthAlert.Enabled,
				Recipients: cfg.AuthAlert.Recipients,
			},
			RateLimit: user.RateLimitOptions{
				LoginEmail: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.LoginEmailMax,
					Window: cfg.AuthRateLimit.LoginEmailWindow,
				},
				LoginIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.LoginIPMax,
					Window: cfg.AuthRateLimit.LoginIPWindow,
				},
				RegisterEmail: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.RegisterEmailMax,
					Window: cfg.AuthRateLimit.RegisterEmailWindow,
				},
				RegisterIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.RegisterIPMax,
					Window: cfg.AuthRateLimit.RegisterIPWindow,
				},
				ForgotPasswordEmail: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ForgotPasswordEmailMax,
					Window: cfg.AuthRateLimit.ForgotPasswordEmailWindow,
				},
				ForgotPasswordIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ForgotPasswordIPMax,
					Window: cfg.AuthRateLimit.ForgotPasswordIPWindow,
				},
				VerifyEmailOTPEmail: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.VerifyEmailOTPEmailMax,
					Window: cfg.AuthRateLimit.VerifyEmailOTPEmailWindow,
				},
				VerifyEmailOTPIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.VerifyEmailOTPIPMax,
					Window: cfg.AuthRateLimit.VerifyEmailOTPIPWindow,
				},
				ResendVerificationEmail: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ResendVerificationEmailMax,
					Window: cfg.AuthRateLimit.ResendVerificationEmailWindow,
				},
				ResendVerificationIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ResendVerificationIPMax,
					Window: cfg.AuthRateLimit.ResendVerificationIPWindow,
				},
				ResetPasswordToken: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ResetPasswordTokenMax,
					Window: cfg.AuthRateLimit.ResetPasswordTokenWindow,
				},
				ResetPasswordIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ResetPasswordIPMax,
					Window: cfg.AuthRateLimit.ResetPasswordIPWindow,
				},
				ChangePasswordUser: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ChangePasswordUserMax,
					Window: cfg.AuthRateLimit.ChangePasswordUserWindow,
				},
				ChangePasswordIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ChangePasswordIPMax,
					Window: cfg.AuthRateLimit.ChangePasswordIPWindow,
				},
				ChangeEmailUser: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ChangeEmailUserMax,
					Window: cfg.AuthRateLimit.ChangeEmailUserWindow,
				},
				ChangeEmailIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ChangeEmailIPMax,
					Window: cfg.AuthRateLimit.ChangeEmailIPWindow,
				},
				ChangeEmailToken: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.ChangeEmailTokenMax,
					Window: cfg.AuthRateLimit.ChangeEmailTokenWindow,
				},
				DeleteAccountUser: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.DeleteAccountUserMax,
					Window: cfg.AuthRateLimit.DeleteAccountUserWindow,
				},
				DeleteAccountIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.DeleteAccountIPMax,
					Window: cfg.AuthRateLimit.DeleteAccountIPWindow,
				},
				RefreshToken: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.RefreshTokenMax,
					Window: cfg.AuthRateLimit.RefreshTokenWindow,
				},
				RefreshIP: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.RefreshIPMax,
					Window: cfg.AuthRateLimit.RefreshIPWindow,
				},
				VerifyEmailToken: user.RateLimitRule{
					Max:    cfg.AuthRateLimit.VerifyEmailTokenMax,
					Window: cfg.AuthRateLimit.VerifyEmailTokenWindow,
				},
			},
		}),
		reader: reader.New(readerRepo),
		bookRAG: bookrag.New(bookRAGRepo, llmClient, bookrag.Options{
			MaxContextPages:      cfg.RAG.MaxContextPages,
			TreeFullMaxNodes:     cfg.RAG.TreeFullMaxNodes,
			TreeBlockMaxNodes:    cfg.RAG.TreeBlockMaxNodes,
			TreeBeamSize:         cfg.RAG.TreeBeamSize,
			TreeMaxTurns:         cfg.RAG.TreeMaxTurns,
			TreeMaxBlocksPerTurn: cfg.RAG.TreeMaxBlocksPerTurn,
		}),
		quran:        quran.New(quranRepo),
		personal:     personal.New(personalRepo, khatamNotifier),
		editorial:    editorial.New(editorialRepo),
		email:        emailUC,
		notification: notificationUC,
	}
}

func initServers(cfg *config.Config, pg *postgres.Postgres, uc useCases, jwtManager *jwt.Manager, l logger.Interface) servers {
	// HTTP Server
	httpServer := httpserver.New(
		l,
		httpserver.Port(cfg.HTTP.Port),
		httpserver.Prefork(cfg.HTTP.UsePreforkMode),
		httpserver.ProxyHeader(cfg.HTTP.ProxyHeader),
		httpserver.TrustedProxies(cfg.HTTP.TrustedProxies),
		httpserver.BodyLimit(cfg.HTTP.BodyLimitBytes),
	)
	restapi.NewRouter(
		httpServer.App,
		cfg,
		pg,
		uc.reader,
		uc.bookRAG,
		uc.quran,
		uc.user,
		uc.personal,
		uc.editorial,
		uc.email,
		jwtManager,
		l,
	)

	return servers{
		http: httpServer,
	}
}

// backgroundInitialDelay is the short head start before the first cleanup and
// alert passes, so restarts do not postpone them by a full interval.
const backgroundInitialDelay = 30 * time.Second

func (s *servers) startServers(
	cfg *config.Config,
	emailUC *emailusecase.UseCase,
	userUC *user.UseCase,
	notificationUC *notification.UseCase,
	l logger.Interface,
) {
	if userUC != nil && cfg.AuthCleanup.Enabled {
		cleanupCtx, cancel := context.WithCancel(context.Background())
		s.authCleanupStop = cancel

		go runAuthCleanupLoop(cleanupCtx, cfg.AuthCleanup.Interval, userUC, l)
	}

	if notificationUC != nil && cfg.OneSignal.Enabled {
		reminderCtx, cancel := context.WithCancel(context.Background())
		s.notificationReminderStop = cancel

		go runNotificationReminderLoop(reminderCtx, cfg.OneSignal.ReminderInterval, notificationUC, l)
	}

	if userUC != nil && cfg.AuthAlert.Enabled {
		alertCtx, cancel := context.WithCancel(context.Background())
		s.authAlertStop = cancel

		go runAuthAlertLoop(alertCtx, cfg.AuthAlert.Interval, userUC, l)
	}

	if emailUC != nil {
		dispatchCtx, cancel := context.WithCancel(context.Background())
		s.emailDispatcherStop = cancel

		go runEmailDispatchLoop(dispatchCtx, cfg, emailUC, l)
	}

	s.http.Start()
}

func runAuthCleanupLoop(ctx context.Context, interval time.Duration, userUC *user.UseCase, l logger.Interface) {
	initialDelay := time.NewTimer(backgroundInitialDelay)
	defer initialDelay.Stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-initialDelay.C:
		case <-ticker.C:
		}

		result, err := userUC.CleanupAuthData(ctx)
		if err != nil {
			l.Error(fmt.Errorf("app - auth cleanup: %w", err))

			continue
		}

		l.Info(
			"app - auth cleanup: rate_limits=%d tokens=%d sessions=%d lockouts=%d cooldowns=%d audit=%d",
			result.RateLimits,
			result.VerificationTokens+result.PasswordResetTokens+result.EmailChangeTokens,
			result.Sessions,
			result.Lockouts,
			result.NotificationCooldowns,
			result.AuditLogs,
		)
	}
}

func runAuthAlertLoop(ctx context.Context, interval time.Duration, userUC *user.UseCase, l logger.Interface) {
	initialDelay := time.NewTimer(backgroundInitialDelay)
	defer initialDelay.Stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-initialDelay.C:
		case <-ticker.C:
		}

		count, err := userUC.AlertRefreshReuse(ctx)
		if err != nil {
			l.Error(fmt.Errorf("app - refresh-reuse alert: %w", err))

			continue
		}

		if count > 0 {
			l.Info("app - refresh-reuse alert: notified admins of %d new event(s)", count)
		}
	}
}

func runNotificationReminderLoop(
	ctx context.Context,
	interval time.Duration,
	notificationUC *notification.UseCase,
	l logger.Interface,
) {
	initialDelay := time.NewTimer(backgroundInitialDelay)
	defer initialDelay.Stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-initialDelay.C:
		case <-ticker.C:
		}

		sent, err := notificationUC.DispatchReminders(ctx)
		if err != nil {
			l.Error(fmt.Errorf("app - reminder dispatch: %w", err))

			continue
		}

		if sent > 0 {
			l.Info("app - reminder dispatch: sent %d streak reminder(s)", sent)
		}
	}
}

func runEmailDispatchLoop(ctx context.Context, cfg *config.Config, emailUC *emailusecase.UseCase, l logger.Interface) {
	ticker := time.NewTicker(cfg.Email.DispatchInterval)
	defer ticker.Stop()

	var pollTicker *time.Ticker

	var pollC <-chan time.Time

	if cfg.Email.DeliveryMode == config.EmailDeliveryModeCloudflare && cfg.Email.CloudflareEventPollingEnabled {
		pollTicker = time.NewTicker(cfg.Email.CloudflareEventPollingInterval)
		pollC = pollTicker.C

		defer pollTicker.Stop()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := emailUC.DispatchDueCampaigns(ctx, cfg.Email.DispatchBatch); err != nil {
				l.Error(fmt.Errorf("app - email dispatcher: %w", err))
			}

			if err := emailUC.DispatchDueTransactionalEmails(ctx, cfg.Email.DispatchBatch); err != nil {
				l.Error(fmt.Errorf("app - transactional email dispatcher: %w", err))
			}
		case <-pollC:
			if _, err := emailUC.PollCloudflareEmailEvents(ctx); err != nil {
				l.Error(fmt.Errorf("app - cloudflare email event poller: %w", err))
			}
		}
	}
}

func (s *servers) waitForShutdown(l logger.Interface) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	var err error

	select {
	case sig := <-interrupt:
		l.Info("app - Run - signal: %s", sig.String())
	case err = <-s.http.Notify():
		l.Error(fmt.Errorf("app - Run - httpServer.Notify: %w", err))
	}

	s.shutdownServers(l)
}

func (s *servers) shutdownServers(l logger.Interface) {
	if s.emailDispatcherStop != nil {
		s.emailDispatcherStop()
	}

	if s.authCleanupStop != nil {
		s.authCleanupStop()
	}

	if s.authAlertStop != nil {
		s.authAlertStop()
	}

	if s.notificationReminderStop != nil {
		s.notificationReminderStop()
	}
	if err := s.http.Shutdown(); err != nil {
		l.Error(fmt.Errorf("app - Run - httpServer.Shutdown: %w", err))
	}
}

// Run creates objects via constructors.
func Run(cfg *config.Config) {
	l := logger.New(cfg.Log.Level)

	// Repository
	pg, err := postgres.New(
		cfg.PG.URL,
		postgres.MaxPoolSize(cfg.PG.PoolMax),
		postgres.MaxConnLifetime(cfg.PG.MaxConnLifetime),
		postgres.MaxConnIdleTime(cfg.PG.MaxConnIdleTime),
	)
	if err != nil {
		l.Fatal(fmt.Errorf("app - Run - postgres.New: %w", err))
	}
	defer pg.Close()

	// JWT. The manager's duration is the ACCESS token TTL; refresh tokens are
	// opaque session tokens with their own configured expiry.
	jwtManager := jwt.New(cfg.JWT.Secret, cfg.JWT.AccessTokenExpiry, cfg.JWT.Issuer, cfg.JWT.Audience)

	uc := initUseCases(cfg, pg, jwtManager, l)
	s := initServers(cfg, pg, uc, jwtManager, l)
	s.startServers(cfg, uc.email, uc.user, uc.notification, l)
	s.waitForShutdown(l)
}

func unsubscribeFrontendURL(cfg *config.Config) string {
	if cfg.Email.UnsubscribeFrontendURL != "" {
		return cfg.Email.UnsubscribeFrontendURL
	}
	parsed, err := url.Parse(cfg.Email.VerifyFrontendURL)
	if err != nil {
		return ""
	}
	parsed.Path = "/unsubscribe"
	parsed.RawQuery = ""

	return parsed.String()
}

func unsubscribeTokenSeed(cfg *config.Config) string {
	secrets := unsubscribeTokenSecrets(cfg)
	keyID := unsubscribeTokenKeyID(cfg)
	if secrets[keyID] != "" {
		return secrets[keyID]
	}
	if cfg.Email.UnsubscribeTokenSecret != "" {
		return cfg.Email.UnsubscribeTokenSecret
	}

	return cfg.JWT.Secret
}

func unsubscribeTokenKeyID(cfg *config.Config) string {
	if cfg.Email.UnsubscribeTokenKeyID != "" {
		return cfg.Email.UnsubscribeTokenKeyID
	}

	return "default"
}

func unsubscribeTokenSecrets(cfg *config.Config) map[string]string {
	secrets := map[string]string{}
	if cfg.Email.UnsubscribeTokenSecrets != "" {
		_ = json.Unmarshal([]byte(cfg.Email.UnsubscribeTokenSecrets), &secrets)
	}
	keyID := unsubscribeTokenKeyID(cfg)
	if secrets[keyID] == "" {
		if cfg.Email.UnsubscribeTokenSecret != "" {
			secrets[keyID] = cfg.Email.UnsubscribeTokenSecret
		} else {
			secrets[keyID] = cfg.JWT.Secret
		}
	}

	return secrets
}
