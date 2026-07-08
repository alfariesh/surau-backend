package restapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/config"
	_ "github.com/alfariesh/surau-backend/docs" // Swagger docs.
	"github.com/alfariesh/surau-backend/internal/controller/restapi/apierror"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	v1 "github.com/alfariesh/surau-backend/internal/controller/restapi/v1"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/gofiber/contrib/otelfiber/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/compress"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/helmet"
	"github.com/gofiber/swagger"
)

// corsPreflightMaxAgeSeconds is how long browsers may cache a preflight
// response (Access-Control-Max-Age).
const corsPreflightMaxAgeSeconds = 3600

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
	email usecase.EmailAdmin,
	jwtManager *jwt.Manager,
	l logger.Interface,
) {
	// Options. Order matters: RequestID first (correlation id), then the
	// access logger (wraps everything below), recovery, then tracing — the
	// otelfiber span is opened inside the logger so trace_id is available to
	// the request-scoped logger that TraceContext builds.
	app.Use(middleware.RequestID())
	app.Use(middleware.Logger(l))
	app.Use(middleware.Recovery(l))

	if cfg.Otel.Enabled {
		app.Use(otelfiber.Middleware())
	}

	app.Use(middleware.TraceContext(l))

	// Security headers, CORS, and response compression. Helmet first so its
	// headers reach every response, CORS before routing so browser preflights
	// are answered, compress last so all bodies are encoded.
	if cfg.Security.HeadersEnabled {
		app.Use(helmet.New(helmet.Config{HSTSMaxAge: cfg.Security.HSTSSeconds}))
	}

	if len(cfg.CORS.AllowedOrigins) > 0 {
		app.Use(cors.New(cors.Config{
			AllowOrigins: strings.Join(cfg.CORS.AllowedOrigins, ","),
			AllowMethods: "GET,POST,PUT,PATCH,DELETE,OPTIONS",
			AllowHeaders: "Authorization,Content-Type,X-Request-ID",
			// AllowCredentials stays false: auth uses bearer tokens, not
			// cookies, and fiber panics on wildcard origins with credentials.
			ExposeHeaders: "ETag,Retry-After,X-Request-ID",
			MaxAge:        corsPreflightMaxAgeSeconds,
		}))
	}

	if cfg.HTTP.CompressionEnabled {
		app.Use(compress.New())
	}

	// Prometheus metrics
	if cfg.Metrics.Enabled {
		// NewWithDefaultRegistry, NOT New: the plain constructor creates a private
		// registry, which would silently drop every custom collector registered on
		// the default one (email queue gauges, loop freshness — bit us on dev).
		prometheus := fiberprometheus.NewWithDefaultRegistry(cfg.App.Name)
		prometheus.RegisterAt(app, "/metrics")
		app.Use(prometheus.Middleware)
	}

	// Swagger
	if cfg.Swagger.Enabled {
		app.Get("/swagger/*", swagger.HandlerDefault)
	}

	// Build/version info — public, so a deploy can be verified (which version/env is
	// live) and clients can report the backend they are talking to.
	app.Get("/version", func(ctx *fiber.Ctx) error {
		return ctx.Status(http.StatusOK).JSON(fiber.Map{
			"name":    cfg.App.Name,
			"version": cfg.App.Version,
			"env":     cfg.App.Env,
		})
	})

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
		v1.NewRoutes(apiV1Group, r, bookRAG, q, u, p, e, email, cfg.Email.CloudflareWebhookSecret, jwtManager, l)
	}

	// Internal service-to-service bridge for the collab websocket server.
	// Guarded by a static service token and meant for the private network
	// only — the reverse proxy must not forward /internal (nginx returns 404).
	if cfg.Collab.Enabled {
		internalGroup := app.Group("/internal", middleware.ServiceToken(cfg.Collab.ServiceToken))
		v1.NewInternalRoutes(internalGroup, e, l)
	}

	// Catch-all (F1-D): unmatched routes answer with the standard error
	// envelope instead of fiber's plain-text 404. Registered last so every
	// real route wins. (Side effect: /internal/* with collab disabled also
	// gets the JSON envelope — the route stays hidden either way.)
	app.Use(func(ctx *fiber.Ctx) error {
		requestID, _ := ctx.Locals("requestID").(string) //nolint:errcheck // absent locals just mean empty request_id

		const msg = "not found"

		return ctx.Status(http.StatusNotFound).JSON(response.Error{
			Error:     msg,
			Code:      apierror.Code(msg),
			Message:   msg,
			RequestID: requestID,
		})
	})
}
