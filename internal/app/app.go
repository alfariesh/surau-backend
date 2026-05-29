// Package app configures and runs application.
package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/evrone/go-clean-template/config"
	"github.com/evrone/go-clean-template/internal/controller/restapi"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/internal/repo/persistent"
	"github.com/evrone/go-clean-template/internal/repo/webapi"
	"github.com/evrone/go-clean-template/internal/usecase/bookrag"
	"github.com/evrone/go-clean-template/internal/usecase/editorial"
	"github.com/evrone/go-clean-template/internal/usecase/personal"
	"github.com/evrone/go-clean-template/internal/usecase/quran"
	"github.com/evrone/go-clean-template/internal/usecase/reader"
	"github.com/evrone/go-clean-template/internal/usecase/user"
	"github.com/evrone/go-clean-template/pkg/httpserver"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/evrone/go-clean-template/pkg/postgres"
)

type useCases struct {
	user      *user.UseCase
	reader    *reader.UseCase
	bookRAG   *bookrag.UseCase
	quran     *quran.UseCase
	personal  *personal.UseCase
	editorial *editorial.UseCase
}

type servers struct {
	http *httpserver.Server
}

func initUseCases(cfg *config.Config, pg *postgres.Postgres, jwtManager *jwt.Manager) useCases {
	userRepo := persistent.NewUserRepo(pg)
	readerRepo := persistent.NewReaderRepo(pg)
	bookRAGRepo := persistent.NewBookRAGRepo(pg)
	quranRepo := persistent.NewQuranRepo(pg)
	personalRepo := persistent.NewPersonalRepo(pg)
	editorialRepo := persistent.NewEditorialRepo(pg)
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
	var rateLimiter repo.AuthRateLimitRepo
	if cfg.AuthRateLimit.Enabled {
		rateLimiter = userRepo
	}

	return useCases{
		user: user.New(userRepo, jwtManager, emailSender, user.Options{
			VerifyFrontendURL:        cfg.Email.VerifyFrontendURL,
			VerificationTTL:          cfg.Email.VerificationTTL,
			ResendCooldown:           cfg.Email.ResendCooldown,
			PasswordResetFrontendURL: cfg.Email.PasswordResetFrontendURL,
			PasswordResetTTL:         cfg.Email.PasswordResetTTL,
			PasswordResetCooldown:    cfg.Email.PasswordResetCooldown,
			RateLimiter:              rateLimiter,
			AuditLogger:              userRepo,
			EmailNotifications: user.EmailNotificationOptions{
				Enabled:                cfg.AuthEmail.NotificationsEnabled,
				NewLoginEnabled:        cfg.AuthEmail.NewLoginEnabled,
				FailedLoginEnabled:     cfg.AuthEmail.FailedLoginEnabled,
				PasswordChangedEnabled: cfg.AuthEmail.PasswordChangedEnabled,
				EmailVerifiedEnabled:   cfg.AuthEmail.EmailVerifiedEnabled,
				RoleChangedEnabled:     cfg.AuthEmail.RoleChangedEnabled,
				FailedLoginCooldown:    cfg.AuthEmail.FailedLoginCooldown,
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
		quran:     quran.New(quranRepo),
		personal:  personal.New(personalRepo),
		editorial: editorial.New(editorialRepo),
	}
}

func initServers(cfg *config.Config, pg *postgres.Postgres, uc useCases, jwtManager *jwt.Manager, l logger.Interface) servers {
	// HTTP Server
	httpServer := httpserver.New(l, httpserver.Port(cfg.HTTP.Port), httpserver.Prefork(cfg.HTTP.UsePreforkMode))
	restapi.NewRouter(httpServer.App, cfg, pg, uc.reader, uc.bookRAG, uc.quran, uc.user, uc.personal, uc.editorial, jwtManager, l)

	return servers{
		http: httpServer,
	}
}

func (s *servers) startServers() {
	s.http.Start()
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
	if err := s.http.Shutdown(); err != nil {
		l.Error(fmt.Errorf("app - Run - httpServer.Shutdown: %w", err))
	}
}

// Run creates objects via constructors.
func Run(cfg *config.Config) {
	l := logger.New(cfg.Log.Level)

	// Repository
	pg, err := postgres.New(cfg.PG.URL, postgres.MaxPoolSize(cfg.PG.PoolMax))
	if err != nil {
		l.Fatal(fmt.Errorf("app - Run - postgres.New: %w", err))
	}
	defer pg.Close()

	// JWT
	jwtManager := jwt.New(cfg.JWT.Secret, cfg.JWT.TokenExpiry, cfg.JWT.Issuer, cfg.JWT.Audience)

	uc := initUseCases(cfg, pg, jwtManager)
	s := initServers(cfg, pg, uc, jwtManager, l)
	s.startServers()
	s.waitForShutdown(l)
}
