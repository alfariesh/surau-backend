package restapi

import (
	"context"
	"net/http"
	"time"

	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/evrone/go-clean-template/config"
	_ "github.com/evrone/go-clean-template/docs" // Swagger docs.
	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
	v1 "github.com/evrone/go-clean-template/internal/controller/restapi/v1"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
)

type databasePinger interface {
	Ping(context.Context) error
}

// NewRouter -.
// Swagger spec:
//
//	@title       Go Clean Template API
//	@description Surau classical book reader API
//	@version     1.0
//	@host        localhost:8080
//	@BasePath    /v1
//	@securityDefinitions.apikey BearerAuth
//	@in header
//	@name Authorization
func NewRouter(
	app *fiber.App,
	cfg *config.Config,
	db databasePinger,
	r usecase.Reader,
	bookRAG usecase.BookRAG,
	q usecase.Quran,
	u usecase.User,
	p usecase.Personal,
	e usecase.Editorial,
	jwtManager *jwt.Manager,
	l logger.Interface,
) {
	// Options
	app.Use(middleware.Logger(l))
	app.Use(middleware.Recovery(l))

	// Prometheus metrics
	if cfg.Metrics.Enabled {
		prometheus := fiberprometheus.New("my-service-name")
		prometheus.RegisterAt(app, "/metrics")
		app.Use(prometheus.Middleware)
	}

	// Swagger
	if cfg.Swagger.Enabled {
		app.Get("/swagger/*", swagger.HandlerDefault)
	}

	// K8s probes
	app.Get("/healthz", func(ctx *fiber.Ctx) error { return ctx.SendStatus(http.StatusOK) })
	app.Get("/readyz", func(ctx *fiber.Ctx) error {
		if db == nil {
			return ctx.SendStatus(http.StatusServiceUnavailable)
		}

		pingCtx, cancel := context.WithTimeout(ctx.UserContext(), 2*time.Second)
		defer cancel()

		if err := db.Ping(pingCtx); err != nil {
			return ctx.SendStatus(http.StatusServiceUnavailable)
		}

		return ctx.SendStatus(http.StatusOK)
	})

	// Routers
	apiV1Group := app.Group("/v1")
	{
		v1.NewRoutes(apiV1Group, r, bookRAG, q, u, p, e, jwtManager, l)
	}
}
