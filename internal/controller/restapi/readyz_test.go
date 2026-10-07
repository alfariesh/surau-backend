package restapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errReadyzDependencyDown = errors.New("dependency down")

type stubPinger struct{ err error }

func (p stubPinger) Ping(context.Context) error { return p.err }

// stubReadinessInference implements only the readiness probe; the embedded
// interface panics if readyz ever calls anything else.
type stubReadinessInference struct {
	usecase.Inference

	missing []string
	err     error
}

func (s stubReadinessInference) ProviderCredentialReadiness(context.Context) ([]string, error) {
	return s.missing, s.err
}

func TestReadyz(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		db        databasePinger
		inference usecase.Inference
		env       string
		want      int
	}{
		{name: "no database", db: nil, want: http.StatusServiceUnavailable},
		{name: "database down", db: stubPinger{err: errReadyzDependencyDown}, want: http.StatusServiceUnavailable},
		{name: "database up, no inference", db: stubPinger{}, want: http.StatusOK},
		{
			name:      "inference registry failure",
			db:        stubPinger{},
			inference: stubReadinessInference{err: errReadyzDependencyDown},
			want:      http.StatusServiceUnavailable,
		},
		{
			name:      "missing provider credential",
			db:        stubPinger{},
			inference: stubReadinessInference{missing: []string{"RAG_LLM_API_KEY"}},
			want:      http.StatusServiceUnavailable,
		},
		{
			name:      "test environment skips the credential check",
			db:        stubPinger{},
			inference: stubReadinessInference{missing: []string{"RAG_LLM_API_KEY"}},
			env:       " Test ",
			want:      http.StatusOK,
		},
		{
			name:      "every dependency ready",
			db:        stubPinger{},
			inference: stubReadinessInference{},
			want:      http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			app := fiber.New()
			app.Get("/readyz", readyz(tc.db, tc.inference, tc.env, logger.New("error")))

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", http.NoBody)
			resp, err := app.Test(req)
			require.NoError(t, err)

			defer resp.Body.Close()

			assert.Equal(t, tc.want, resp.StatusCode)
		})
	}
}
