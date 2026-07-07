package httpserver_test

import (
	"testing"

	"github.com/alfariesh/surau-backend/pkg/httpserver"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/stretchr/testify/assert"
)

func TestNewProxyConfig(t *testing.T) {
	t.Parallel()

	l := logger.New("error")

	t.Run("trusts proxy header when allowlist is set", func(t *testing.T) {
		t.Parallel()

		s := httpserver.New(
			l,
			httpserver.ProxyHeader("X-Real-IP"),
			httpserver.TrustedProxies([]string{"172.16.0.0/12", " ", "10.0.0.1"}),
		)
		cfg := s.App.Config()

		assert.Equal(t, "X-Real-IP", cfg.ProxyHeader)
		assert.True(t, cfg.EnableTrustedProxyCheck)
		assert.True(t, cfg.EnableIPValidation)
		// Blank entries are dropped so they cannot widen the allowlist.
		assert.Equal(t, []string{"172.16.0.0/12", "10.0.0.1"}, cfg.TrustedProxies)
	})

	t.Run("ignores proxy header without an allowlist", func(t *testing.T) {
		t.Parallel()

		s := httpserver.New(
			l,
			httpserver.ProxyHeader("X-Real-IP"),
			httpserver.TrustedProxies(nil),
		)
		cfg := s.App.Config()

		assert.Empty(t, cfg.ProxyHeader)
		assert.False(t, cfg.EnableTrustedProxyCheck)
	})
}
