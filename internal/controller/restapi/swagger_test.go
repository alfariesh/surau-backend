package restapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSwaggerDocumentRendersValidGenerationIdentityContract(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	app.Get("/swagger/*", swagger.HandlerDefault)

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/swagger/doc.json", http.NoBody)
	response, err := app.Test(request)
	require.NoError(t, err)

	defer response.Body.Close()

	assert.Equal(t, http.StatusOK, response.StatusCode)

	rendered, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	var document struct {
		Paths       map[string]json.RawMessage `json:"paths"`
		Definitions map[string]json.RawMessage `json:"definitions"`
	}
	require.NoError(t, json.Unmarshal(rendered, &document))
	assert.Contains(t, document.Paths, "/editorial/citable-units/{id}")
	assert.Contains(t, document.Paths, "/editorial/cross-references/{id}")
	assert.Contains(t, document.Definitions, "entity.GenerationIdentity")
}
