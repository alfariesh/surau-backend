package restapi

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/alfariesh/surau-backend/config"
	"github.com/alfariesh/surau-backend/docs"
	v1 "github.com/alfariesh/surau-backend/internal/controller/restapi/v1"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"
)

// undocumentedV1Routes is the ratchet for /v1 routes that predate this
// contract test and still lack Swagger annotations. It may only shrink:
// document a route, then delete its entry. New routes never belong here.
//
//nolint:gochecknoglobals // test-only ratchet list
var undocumentedV1Routes = map[string]bool{}

// TestSwaggerDocumentsExactlyTheMountedV1Routes keeps docs/swagger.json (the
// contract web and mobile clients read) and the live /v1 router in lockstep:
// a documented route the router does not mount is a phantom endpoint, and a
// mounted route without documentation is an invisible one. Operational
// endpoints (/healthz, /readyz, /version, /metrics, /swagger) and the
// service-to-service /internal tree are outside the public contract.
func TestSwaggerDocumentsExactlyTheMountedV1Routes(t *testing.T) {
	t.Parallel()

	mounted := mountedV1Routes(t)
	documented := documentedV1Routes(t)

	var phantom, invisible, stale []string

	for route := range documented {
		if !mounted[route] {
			phantom = append(phantom, route)
		}
	}

	for route := range mounted {
		if !documented[route] && !undocumentedV1Routes[route] {
			invisible = append(invisible, route)
		}
	}

	for route := range undocumentedV1Routes {
		if documented[route] || !mounted[route] {
			stale = append(stale, route)
		}
	}

	sort.Strings(phantom)
	sort.Strings(invisible)
	sort.Strings(stale)

	require.Empty(t, phantom, "swagger documents routes the router does not mount")
	require.Empty(t, invisible, "router mounts routes swagger does not document; annotate the handler and run `make swag-v1`")
	require.Empty(t, stale, "remove these entries from undocumentedV1Routes: they are documented or gone")
}

var fiberPathParam = regexp.MustCompile(`:(\w+)`)

func mountedV1Routes(t *testing.T) map[string]bool {
	t.Helper()

	app := fiber.New()
	NewRouter(app, &config.Config{}, nil, &v1.Dependencies{Logger: logger.New("error")})

	routes := map[string]bool{}

	mountedRoutes := app.GetRoutes(true)
	for i := range mountedRoutes {
		route := &mountedRoutes[i]
		// Fiber mounts HEAD automatically next to every GET.
		if route.Method == http.MethodHead || !strings.HasPrefix(route.Path, "/v1/") {
			continue
		}

		path := strings.TrimSuffix(fiberPathParam.ReplaceAllString(route.Path, "{$1}"), "/")
		routes[route.Method+" "+path] = true
	}

	require.NotEmpty(t, routes, "router mounted no /v1 routes")

	return routes
}

func documentedV1Routes(t *testing.T) map[string]bool {
	t.Helper()

	var document struct {
		BasePath string                    `json:"basePath"`
		Paths    map[string]map[string]any `json:"paths"`
	}
	require.NoError(t, json.Unmarshal([]byte(docs.SwaggerInfo.ReadDoc()), &document))
	require.Equal(t, "/v1", document.BasePath)

	routes := map[string]bool{}

	for path, operations := range document.Paths {
		for method := range operations {
			routes[strings.ToUpper(method)+" "+document.BasePath+path] = true
		}
	}

	return routes
}
