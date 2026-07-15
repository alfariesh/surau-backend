package restapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/config"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCORSPreflightAllowsEditorialIfMatchHeader(t *testing.T) {
	t.Parallel()

	const origin = "https://app.surau.org"

	app := fiber.New()
	cfg := &config.Config{}
	cfg.CORS.AllowedOrigins = []string{origin}
	NewRouter(
		app,
		cfg,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		logger.New("error"),
	)

	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodOptions,
		"/v1/editorial/quran/surahs/73/draft",
		nil,
	)
	request.Header.Set(fiber.HeaderOrigin, origin)
	request.Header.Set(fiber.HeaderAccessControlRequestMethod, http.MethodPut)
	request.Header.Set(
		fiber.HeaderAccessControlRequestHeaders,
		"authorization,content-type,if-match",
	)

	response, err := app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Equal(t, http.StatusNoContent, response.StatusCode)
	assert.Equal(t, origin, response.Header.Get(fiber.HeaderAccessControlAllowOrigin))
	assert.Contains(
		t,
		strings.ToLower(response.Header.Get(fiber.HeaderAccessControlAllowHeaders)),
		"if-match",
	)
}
