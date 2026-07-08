package httpserver

import (
	"net"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Option -.
type Option func(*Server)

// ErrorHandler installs a custom Fiber error handler so framework-level
// failures (body limit, routing errors) share the API's error envelope
// instead of Fiber's plain-text defaults (F1-D).
func ErrorHandler(handler fiber.ErrorHandler) Option {
	return func(s *Server) {
		s.errorHandler = handler
	}
}

// Port -.
func Port(port string) Option {
	return func(s *Server) {
		s.address = net.JoinHostPort("", port)
	}
}

// Prefork -.
func Prefork(prefork bool) Option {
	return func(s *Server) {
		s.prefork = prefork
	}
}

// ReadTimeout -.
func ReadTimeout(timeout time.Duration) Option {
	return func(s *Server) {
		s.readTimeout = timeout
	}
}

// WriteTimeout -.
func WriteTimeout(timeout time.Duration) Option {
	return func(s *Server) {
		s.writeTimeout = timeout
	}
}

// ShutdownTimeout -.
func ShutdownTimeout(timeout time.Duration) Option {
	return func(s *Server) {
		s.shutdownTimeout = timeout
	}
}

// BodyLimit caps the accepted request body size in bytes. Values <= 0 keep the
// default (4 MiB).
func BodyLimit(bytes int) Option {
	return func(s *Server) {
		if bytes > 0 {
			s.bodyLimit = bytes
		}
	}
}

// ProxyHeader sets the header used to resolve the client IP when a request
// arrives through a trusted reverse proxy (e.g. "X-Real-IP"). It only takes
// effect together with a non-empty TrustedProxies allowlist.
func ProxyHeader(header string) Option {
	return func(s *Server) {
		s.proxyHeader = strings.TrimSpace(header)
	}
}

// TrustedProxies lists the proxy IPs/CIDRs allowed to set the forwarding
// header. When empty, forwarding headers are ignored and the socket peer IP is
// used, so untrusted clients cannot spoof their address.
func TrustedProxies(proxies []string) Option {
	return func(s *Server) {
		cleaned := make([]string, 0, len(proxies))
		for _, proxy := range proxies {
			if proxy = strings.TrimSpace(proxy); proxy != "" {
				cleaned = append(cleaned, proxy)
			}
		}

		s.trustedProxies = cleaned
	}
}
