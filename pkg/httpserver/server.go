// Package httpserver implements HTTP server.
package httpserver

import (
	"context"
	"errors"
	"time"

	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/goccy/go-json"
	"github.com/gofiber/fiber/v2"
	"golang.org/x/sync/errgroup"
)

const (
	_defaultAddr            = ":80"
	_defaultReadTimeout     = 5 * time.Second
	_defaultWriteTimeout    = 5 * time.Second
	_defaultShutdownTimeout = 3 * time.Second
	_defaultBodyLimit       = 4 * 1024 * 1024
)

// Server -.
type Server struct {
	ctx context.Context
	eg  *errgroup.Group

	App    *fiber.App
	notify chan error

	address         string
	prefork         bool
	readTimeout     time.Duration
	writeTimeout    time.Duration
	shutdownTimeout time.Duration
	bodyLimit       int
	proxyHeader     string
	trustedProxies  []string
	errorHandler    fiber.ErrorHandler

	logger logger.Interface
}

// New -.
func New(l logger.Interface, opts ...Option) *Server {
	group, ctx := errgroup.WithContext(context.Background())
	group.SetLimit(1) // Run only one goroutine

	s := &Server{
		ctx:             ctx,
		eg:              group,
		App:             nil,
		notify:          make(chan error, 1),
		address:         _defaultAddr,
		readTimeout:     _defaultReadTimeout,
		writeTimeout:    _defaultWriteTimeout,
		shutdownTimeout: _defaultShutdownTimeout,
		bodyLimit:       _defaultBodyLimit,
		logger:          l,
	}

	// Custom options
	for _, opt := range opts {
		opt(s)
	}

	// Only honor the proxy header when a trusted-proxy allowlist is configured;
	// otherwise ctx.IP() falls back to the socket peer so clients cannot spoof it.
	proxyHeader := s.proxyHeader
	if len(s.trustedProxies) == 0 {
		proxyHeader = ""
	}

	config := fiber.Config{
		Prefork:                 s.prefork,
		ReadTimeout:             s.readTimeout,
		WriteTimeout:            s.writeTimeout,
		BodyLimit:               s.bodyLimit,
		JSONDecoder:             json.Unmarshal,
		JSONEncoder:             json.Marshal,
		ProxyHeader:             proxyHeader,
		EnableTrustedProxyCheck: len(s.trustedProxies) > 0,
		TrustedProxies:          s.trustedProxies,
		EnableIPValidation:      true,
	}

	// F1-D: framework-level failures share the API error envelope when the
	// caller installs a handler (fiber's default returns text/plain).
	if s.errorHandler != nil {
		config.ErrorHandler = s.errorHandler
	}

	app := fiber.New(config)

	s.App = app

	return s
}

// Start -.
func (s *Server) Start() {
	s.eg.Go(func() error {
		err := s.App.Listen(s.address)
		if err != nil {
			s.notify <- err

			close(s.notify)

			return err
		}

		return nil
	})

	s.logger.Info("restapi server - Server - Started")
}

// Notify -.
func (s *Server) Notify() <-chan error {
	return s.notify
}

// Shutdown -.
func (s *Server) Shutdown() error {
	var shutdownErrors []error

	err := s.App.ShutdownWithTimeout(s.shutdownTimeout)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Error(err, "restapi server - Server - Shutdown - s.App.ShutdownWithTimeout")

		shutdownErrors = append(shutdownErrors, err)
	}

	// Wait for all goroutines to finish and get any error
	err = s.eg.Wait()
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Error(err, "restapi server - Server - Shutdown - s.eg.Wait")

		shutdownErrors = append(shutdownErrors, err)
	}

	s.logger.Info("restapi server - Server - Shutdown")

	return errors.Join(shutdownErrors...)
}
