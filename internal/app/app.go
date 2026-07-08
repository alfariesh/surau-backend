// Package app configures and runs application.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"sync"
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
	"github.com/alfariesh/surau-backend/pkg/observability"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/exaring/otelpgx"
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
	http *httpserver.Server

	// Supervised background loops (F1-C): one shared cancel plus a drain
	// WaitGroup so shutdown can wait for in-flight passes (bounded by
	// loopDrainTimeout).
	loopStop context.CancelFunc
	loopWG   sync.WaitGroup
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
const tracingShutdownTimeout = 5 * time.Second

const backgroundInitialDelay = 30 * time.Second

func (s *servers) startServers(
	cfg *config.Config,
	emailUC *emailusecase.UseCase,
	userUC *user.UseCase,
	notificationUC *notification.UseCase,
	l logger.Interface,
) {
	loopCtx, cancel := context.WithCancel(context.Background())
	s.loopStop = cancel

	for _, spec := range buildLoopSpecs(cfg, emailUC, userUC, notificationUC, l) {
		s.startLoop(loopCtx, spec, l)
	}

	s.http.Start()
}

// buildLoopSpecs assembles the supervised background loops, applying the same
// config gates as before supervision (a disabled loop emits no metrics).
func buildLoopSpecs(
	cfg *config.Config,
	emailUC *emailusecase.UseCase,
	userUC *user.UseCase,
	notificationUC *notification.UseCase,
	l logger.Interface,
) []loopSpec {
	var specs []loopSpec

	if userUC != nil && cfg.AuthCleanup.Enabled {
		specs = append(specs, loopSpec{
			name:         "auth_cleanup",
			interval:     cfg.AuthCleanup.Interval,
			initialDelay: backgroundInitialDelay,
			run:          authCleanupPass(userUC, l),
		})
	}

	if notificationUC != nil && cfg.OneSignal.Enabled {
		specs = append(specs, loopSpec{
			name:         "notification_reminder",
			interval:     cfg.OneSignal.ReminderInterval,
			initialDelay: backgroundInitialDelay,
			run:          notificationReminderPass(notificationUC, l),
		})
	}

	if userUC != nil && cfg.AuthAlert.Enabled {
		specs = append(specs, loopSpec{
			name:         "auth_alert",
			interval:     cfg.AuthAlert.Interval,
			initialDelay: backgroundInitialDelay,
			run:          authAlertPass(userUC, l),
		})
	}

	return append(specs, buildEmailLoopSpecs(cfg, emailUC)...)
}

// buildEmailLoopSpecs gates the email loops exactly as the pre-supervisor
// code did: the dispatcher runs whenever the usecase exists; the Cloudflare
// event poller only in cloudflare mode with polling enabled (otherwise it
// would tick "success" while doing nothing).
func buildEmailLoopSpecs(cfg *config.Config, emailUC *emailusecase.UseCase) []loopSpec {
	if emailUC == nil {
		return nil
	}

	specs := []loopSpec{{
		name:     "email_dispatch",
		interval: cfg.Email.DispatchInterval,
		run:      emailDispatchPass(cfg, emailUC),
	}}

	if cfg.Email.DeliveryMode == config.EmailDeliveryModeCloudflare && cfg.Email.CloudflareEventPollingEnabled {
		specs = append(specs, loopSpec{
			name:     "email_events_poll",
			interval: cfg.Email.CloudflareEventPollingInterval,
			run: func(ctx context.Context) error {
				_, err := emailUC.PollCloudflareEmailEvents(ctx)

				return err
			},
		})
	}

	return specs
}

func authCleanupPass(userUC *user.UseCase, l logger.Interface) func(context.Context) error {
	return func(ctx context.Context) error {
		result, err := userUC.CleanupAuthData(ctx)
		if err != nil {
			return fmt.Errorf("auth cleanup: %w", err)
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

		return nil
	}
}

func authAlertPass(userUC *user.UseCase, l logger.Interface) func(context.Context) error {
	return func(ctx context.Context) error {
		count, err := userUC.AlertRefreshReuse(ctx)
		if err != nil {
			return fmt.Errorf("refresh-reuse alert: %w", err)
		}

		if count > 0 {
			l.Info("app - refresh-reuse alert: notified admins of %d new event(s)", count)
		}

		return nil
	}
}

func notificationReminderPass(notificationUC *notification.UseCase, l logger.Interface) func(context.Context) error {
	return func(ctx context.Context) error {
		sent, err := notificationUC.DispatchReminders(ctx)
		if err != nil {
			return fmt.Errorf("reminder dispatch: %w", err)
		}

		if sent > 0 {
			l.Info("app - reminder dispatch: sent %d streak reminder(s)", sent)
		}

		return nil
	}
}

func emailDispatchPass(cfg *config.Config, emailUC *emailusecase.UseCase) func(context.Context) error {
	return func(ctx context.Context) error {
		var campaignErr, transactionalErr error

		if err := emailUC.DispatchDueCampaigns(ctx, cfg.Email.DispatchBatch); err != nil {
			campaignErr = fmt.Errorf("email dispatcher: %w", err)
		}

		if err := emailUC.DispatchDueTransactionalEmails(ctx, cfg.Email.DispatchBatch); err != nil {
			transactionalErr = fmt.Errorf("transactional email dispatcher: %w", err)
		}

		return errors.Join(campaignErr, transactionalErr)
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
	// Cancel all supervised loops first so their drain overlaps the HTTP
	// shutdown; then wait, bounded, so a stuck pass cannot block exit past
	// Docker's kill grace.
	if s.loopStop != nil {
		s.loopStop()
	}

	drained := make(chan struct{})

	go func() {
		s.loopWG.Wait()
		close(drained)
	}()

	if err := s.http.Shutdown(); err != nil {
		l.Error(fmt.Errorf("app - Run - httpServer.Shutdown: %w", err))
	}

	select {
	case <-drained:
	case <-time.After(loopDrainTimeout):
		l.Error("app - shutdown: background loops still draining after %s, exiting anyway", loopDrainTimeout)
	}
}

// Run creates objects via constructors.
func Run(cfg *config.Config) {
	l := logger.New(cfg.Log.Level)

	// Tracing (F1-B): HTTP -> pgx -> outbound webapi spans, exported over
	// OTLP to the self-hosted backend. Disabled config = zero-cost no-op.
	shutdownTracing, err := observability.InitTracing(context.Background(), &observability.TracingConfig{
		Enabled:     cfg.Otel.Enabled,
		Endpoint:    cfg.Otel.Endpoint,
		SampleRatio: cfg.Otel.SampleRatio,
		ServiceName: cfg.App.Name,
		Environment: cfg.App.Env,
		Version:     cfg.App.Version,
	})
	if err != nil {
		l.Fatal(fmt.Errorf("app - Run - observability.InitTracing: %w", err))
	}

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), tracingShutdownTimeout)
		defer cancel()

		if err := shutdownTracing(shutdownCtx); err != nil {
			l.Error(fmt.Errorf("app - Run - tracing shutdown: %w", err))
		}
	}()

	// Repository
	pgOpts := []postgres.Option{
		postgres.MaxPoolSize(cfg.PG.PoolMax),
		postgres.MaxConnLifetime(cfg.PG.MaxConnLifetime),
		postgres.MaxConnIdleTime(cfg.PG.MaxConnIdleTime),
	}
	if cfg.Otel.Enabled {
		pgOpts = append(pgOpts, postgres.QueryTracer(otelpgx.NewTracer(otelpgx.WithTrimSQLInSpanName())))
	}

	pg, err := postgres.New(cfg.PG.URL, pgOpts...)
	if err != nil {
		l.Fatal(fmt.Errorf("app - Run - postgres.New: %w", err))
	}
	defer pg.Close()

	if cfg.Metrics.Enabled {
		registerEmailQueueMetrics(pg.Pool)
		registerBackfillMetrics(pg.Pool)
	}

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
