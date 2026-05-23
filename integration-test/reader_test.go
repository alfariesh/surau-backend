package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-json"
)

const requestTimeout = 5 * time.Second

func baseURL() string {
	if value := os.Getenv("INTEGRATION_HTTP_URL"); value != "" {
		return strings.TrimRight(value, "/")
	}

	return "http://app:8080"
}

func TestMain(m *testing.M) {
	if os.Getenv("RUN_INTEGRATION_TESTS") != "1" {
		os.Exit(0)
	}

	os.Exit(m.Run())
}

func TestReaderRESTSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL()+"/healthz", http.NoBody)
	if err != nil {
		t.Fatalf("health request: %v", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health expected 200, got %d", resp.StatusCode)
	}

	for _, path := range []string{"/v1/categories", "/v1/authors", "/v1/books"} {
		resp = doJSON(t, http.MethodGet, baseURL()+path, nil, "")
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s expected 200, got %d", path, resp.StatusCode)
		}
	}

	email := fmt.Sprintf("reader_%d@test.local", time.Now().UnixNano())
	registerBody := fmt.Sprintf(`{"username":"reader_%d","email":%q,"password":"testpass123"}`, time.Now().UnixNano(), email)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/register", bytes.NewBufferString(registerBody), "")
	resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register expected 201, got %d", resp.StatusCode)
	}

	loginBody := fmt.Sprintf(`{"email":%q,"password":"testpass123"}`, email)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(loginBody), "")
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login expected 200, got %d", resp.StatusCode)
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tokenResp.Token == "" {
		t.Fatal("expected token")
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/user/profile", nil, tokenResp.Token)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile expected 200, got %d", resp.StatusCode)
	}
}

func doJSON(t *testing.T, method, url string, body *bytes.Buffer, token string) *http.Response {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	var reqBody io.Reader = http.NoBody
	if body != nil {
		reqBody = body
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}

	return resp
}
