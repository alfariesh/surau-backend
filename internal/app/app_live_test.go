package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/config"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveAppBootstrap smoke-tests the real bootstrap (F1-E): the full run()
// path — config, Postgres, metrics, usecases, HTTP server, supervised loops —
// against a live migrated Postgres, then a clean in-process shutdown. Gated on
// SURAU_LIVE_PG so it never runs in plain `go test ./...`.
//
//	SURAU_LIVE_PG=postgres://... go test ./internal/app/ -run TestLiveAppBootstrap -v
//
// Exactly ONE bootstrap test may exist per test binary: run() registers
// process-global Prometheus collectors (MustRegister panics on the second
// registration).
//
//nolint:paralleltest // boots the one-per-process app instance
func TestLiveAppBootstrap(t *testing.T) {
	liveURL := os.Getenv("SURAU_LIVE_PG")
	if liveURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	port := freeTCPPort(t)

	priceCatalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, writeErr := io.WriteString(
			w,
			`[{"model_name":"glm-5.1","input_cost_per_token":"0.0000001",`+
				`"output_cost_per_token":"0.00000032"}]`,
		)
		assert.NoError(t, writeErr)
	}))
	defer priceCatalog.Close()

	// The test owns its entire environment: required keys plus explicit safe
	// values for every gate that could reach an external service.
	//nolint:gosec // throwaway smoke-test values, not real credentials
	for key, value := range map[string]string{
		"APP_NAME":                       "surau-backend-smoke",
		"APP_VERSION":                    "smoke-test",
		"APP_ENV":                        "test",
		"HTTP_PORT":                      port,
		"LOG_LEVEL":                      "error",
		"PG_URL":                         liveURL,
		"JWT_SECRET":                     "smoke-test-secret-0123456789abcdef0123456789",
		"METRICS_ENABLED":                "true",
		"SWAGGER_ENABLED":                "false",
		"OTEL_ENABLED":                   "false",
		"ONESIGNAL_ENABLED":              "false",
		"AUTH_ALERT_ENABLED":             "false",
		"EMAIL_DELIVERY_MODE":            "log",
		"EMAIL_DISPATCH_INTERVAL":        "1s",
		"EMAIL_VERIFY_FRONTEND_URL":      "http://localhost:3000/verify",
		"PASSWORD_RESET_FRONTEND_URL":    "http://localhost:3000/reset",
		"EMAIL_CHANGE_FRONTEND_URL":      "http://localhost:3000/email-change",
		"RAG_LLM_API_KEY":                "smoke-primary-provider-credential",
		"DEEPSEEK_API_KEY":               "smoke-secondary-provider-credential",
		"INFERENCE_SUMOPOD_CATALOG_URL":  priceCatalog.URL,
		"INFERENCE_CACHE_ENCRYPTION_KEY": "smoke-u0-cache-key-at-least-thirty-two-bytes",
	} {
		t.Setenv(key, value)
	}

	cfg, err := config.NewConfig()
	require.NoError(t, err, "smoke env must produce a valid config")

	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		run(cfg, stop)
	}()

	base := "http://127.0.0.1:" + port

	// Listener up = bootstrap survived config/DB/usecase/router wiring.
	requireEventuallyOK(t, base+"/healthz", 30*time.Second)

	t.Run("readyz pings the live database", func(t *testing.T) {
		status, _ := httpGet(t, base+"/readyz")
		assert.Equal(t, http.StatusOK, status)
	})

	t.Run("version reports identity", func(t *testing.T) {
		status, body := httpGet(t, base+"/version")
		assert.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, "surau-backend-smoke")
		assert.Contains(t, body, "smoke-test")
	})

	t.Run("background loops are alive (email_dispatch pass lands in metrics)", func(t *testing.T) {
		// Loop series materialize on the first pass; dispatch ticks every 1s
		// here, so a missing series after the deadline means loops never ran.
		deadline := time.Now().Add(15 * time.Second)

		var body string

		for time.Now().Before(deadline) {
			var status int

			status, body = httpGet(t, base+"/metrics")
			require.Equal(t, http.StatusOK, status)

			if strings.Contains(body, `surau_loop_runs_total{loop="email_dispatch",result="success"}`) &&
				strings.Contains(body, `surau_loop_last_success_timestamp_seconds{loop="email_dispatch"}`) {
				return
			}

			time.Sleep(500 * time.Millisecond)
		}

		t.Fatalf("email_dispatch loop series never appeared in /metrics; last scrape:\n%s", body)
	})

	// Optional OneSignal integrations default to disabled. Their handles must
	// then reach the use cases as nil interfaces: a nil *UseCase wrapped in a
	// non-nil interface passes the call sites' nil guards and panics.
	t.Run("disabled OneSignal integrations degrade instead of panicking", func(t *testing.T) {
		const password = "smoke-password-123"

		email := fmt.Sprintf("smoke-%d@example.com", time.Now().UnixNano())
		accessToken := registerVerifiedUser(t, base, liveURL, email, password)

		status, body := httpPost(t, base+"/v1/me/push/identity-token", accessToken, "")
		assert.Equal(t, http.StatusServiceUnavailable, status, body)

		status, body = httpPost(
			t, base+"/v1/auth/delete-account", accessToken,
			`{"current_password":"`+password+`"}`,
		)
		require.Equal(t, http.StatusOK, status, body)
		assert.JSONEq(t, `{"account_deleted":true}`, body)
	})

	// Clean shutdown: run() must return once stop fires, within the bounded
	// loop-drain + HTTP shutdown budget.
	close(stop)

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("run() did not return within 15s of stop — shutdown path is stuck")
	}
}

func freeTCPPort(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig

	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr, ok := lis.Addr().(*net.TCPAddr)
	require.True(t, ok)
	require.NoError(t, lis.Close())

	return fmt.Sprintf("%d", addr.Port)
}

func requireEventuallyOK(t *testing.T, url string, budget time.Duration) {
	t.Helper()

	var lastErr error

	for deadline := time.Now().Add(budget); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		status, err := tryGet(url)
		if err == nil && status == http.StatusOK {
			return
		}

		lastErr = fmt.Errorf("GET %s: status=%d err=%w", url, status, err)
	}

	t.Fatalf("app never became ready within %s: %v", budget, lastErr)
}

func tryGet(url string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return 0, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	return resp.StatusCode, nil
}

func httpGet(t *testing.T, url string) (status int, body string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(raw)
}

// registerVerifiedUser signs up through the public API, marks the address
// verified directly (no inbox in a smoke test), and logs in.
func registerVerifiedUser(t *testing.T, base, liveURL, email, password string) string {
	t.Helper()

	status, body := httpPost(t, base+"/v1/auth/register", "", fmt.Sprintf(
		`{"username":%q,"email":%q,"password":%q}`, strings.Split(email, "@")[0], email, password,
	))
	require.Equal(t, http.StatusCreated, status, body)

	conn, err := pgx.Connect(t.Context(), liveURL)
	require.NoError(t, err)

	defer func() { _ = conn.Close(context.Background()) }()

	t.Cleanup(func() {
		cleanup, cleanupErr := pgx.Connect(context.Background(), liveURL)
		if !assert.NoError(t, cleanupErr) {
			return
		}

		defer func() { _ = cleanup.Close(context.Background()) }()

		_, cleanupErr = cleanup.Exec(context.Background(), `DELETE FROM users WHERE email = $1`, email)
		assert.NoError(t, cleanupErr)
	})

	_, err = conn.Exec(
		t.Context(),
		`UPDATE users SET email_verified = true, email_verified_at = now() WHERE email = $1`,
		email,
	)
	require.NoError(t, err)

	status, body = httpPost(t, base+"/v1/auth/login", "", fmt.Sprintf(
		`{"email":%q,"password":%q}`, email, password,
	))
	require.Equal(t, http.StatusOK, status, body)

	var token struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &token))
	require.NotEmpty(t, token.AccessToken)

	return token.AccessToken
}

func httpPost(t *testing.T, url, bearer, jsonBody string) (status int, body string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(jsonBody))
	require.NoError(t, err)

	if jsonBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, string(raw)
}
