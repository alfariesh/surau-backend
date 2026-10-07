package observability

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestTracesEndpointURLAddsTheSignalPathOnlyToBaseURLs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ endpoint, want string }{
		{"http://tempo:4318", "http://tempo:4318/v1/traces"},
		{"https://collector.example:4318", "https://collector.example:4318/v1/traces"},
		{"http://tempo:4318/", "http://tempo:4318/"},
		{"http://collector:4318/custom/v1/traces", "http://collector:4318/custom/v1/traces"},
		{"://not-a-url", "://not-a-url"},
	} {
		assert.Equal(t, tc.want, tracesEndpointURL(tc.endpoint), tc.endpoint)
	}
}

// TestTraceExporterPostsBaseURLSpansToV1Traces drives the real OTLP/HTTP
// exporter: a collector configured by base URL, as OTEL_EXPORTER_OTLP_ENDPOINT
// is in every deployment, must receive spans on /v1/traces.
func TestTraceExporterPostsBaseURLSpansToV1Traces(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		paths []string
	)

	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()

		paths = append(paths, r.URL.Path)

		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)

	exporter, err := newTraceExporter(t.Context(), collector.URL)
	require.NoError(t, err)

	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	_, span := provider.Tracer("observability-test").Start(t.Context(), "export-path")
	span.End()
	require.NoError(t, provider.Shutdown(t.Context()))

	mu.Lock()
	defer mu.Unlock()

	assert.Equal(t, []string{"/v1/traces"}, paths)
}
