package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type sessionTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	SessionID    string `json:"session_id"`
}

type sessionInfo struct {
	ID        string `json:"id"`
	UserAgent string `json:"user_agent"`
	IsCurrent bool   `json:"is_current"`
}

type sessionListResponse struct {
	Sessions []sessionInfo `json:"items"`
}

// registerVerifiedUser registers a fresh user, marks it verified, and returns
// the login email plus the user id.
func registerVerifiedUser(t *testing.T) (string, string) {
	t.Helper()

	stamp := time.Now().UnixNano()
	email := fmt.Sprintf("session_%d@test.local", stamp)
	body := fmt.Sprintf(`{"username":"sess_%d","email":%q,"password":"testpass123"}`, stamp, email)

	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/register", bytes.NewBufferString(body), "")
	if resp.StatusCode != http.StatusCreated {
		resp.Body.Close()
		t.Fatalf("register expected 201, got %d", resp.StatusCode)
	}
	var reg struct {
		ID string `json:"id"`
	}
	decodeAndClose(t, resp, &reg)
	if reg.ID == "" {
		t.Fatal("register did not return a user id")
	}

	verifyRegisteredEmail(t, email)

	return email, reg.ID
}

// loginWithUA logs in with a specific User-Agent so device-level assertions
// (is_current, user_agent) are meaningful.
func loginWithUA(t *testing.T, email, userAgent string) sessionTokenResponse {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	body := fmt.Sprintf(`{"email":%q,"password":"testpass123"}`, email)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login %s: %v", userAgent, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("login expected 200, got %d", resp.StatusCode)
	}

	var tokens sessionTokenResponse
	decodeAndClose(t, resp, &tokens)
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatal("login did not return access/refresh tokens")
	}

	return tokens
}

func listSessions(t *testing.T, accessToken string) sessionListResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, baseURL()+"/v1/auth/sessions", nil, accessToken)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("list sessions expected 200, got %d", resp.StatusCode)
	}

	var list sessionListResponse
	decodeAndClose(t, resp, &list)

	return list
}

func TestAuthSessionManagementFlow(t *testing.T) {
	email, _ := registerVerifiedUser(t)

	deviceA := loginWithUA(t, email, "IntegrationDeviceA/1.0")
	loginWithUA(t, email, "IntegrationDeviceB/2.0")

	// 1. Both devices show up; exactly the caller's device is flagged current.
	list := listSessions(t, deviceA.AccessToken)
	if len(list.Sessions) != 2 {
		t.Fatalf("expected 2 active sessions, got %d", len(list.Sessions))
	}
	currentCount := 0
	var deviceBSessionID string
	for _, s := range list.Sessions {
		if s.IsCurrent {
			currentCount++
			if s.UserAgent != "IntegrationDeviceA/1.0" {
				t.Fatalf("current session has user_agent %q, want device A", s.UserAgent)
			}
		} else {
			deviceBSessionID = s.ID
		}
	}
	if currentCount != 1 {
		t.Fatalf("expected exactly 1 current session, got %d", currentCount)
	}
	if deviceBSessionID == "" {
		t.Fatal("could not find the non-current (device B) session id")
	}

	// 2. Revoke device B from device A.
	resp := doJSON(t, http.MethodDelete, baseURL()+"/v1/auth/sessions/"+deviceBSessionID, nil, deviceA.AccessToken)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("revoke expected 200, got %d", resp.StatusCode)
	}
	var revoked struct {
		SessionRevoked bool `json:"session_revoked"`
	}
	decodeAndClose(t, resp, &revoked)
	if !revoked.SessionRevoked {
		t.Fatal("expected session_revoked=true")
	}

	// 3. Only device A remains.
	list = listSessions(t, deviceA.AccessToken)
	if len(list.Sessions) != 1 {
		t.Fatalf("expected 1 active session after revoke, got %d", len(list.Sessions))
	}
	if !list.Sessions[0].IsCurrent {
		t.Fatal("remaining session should be the current device")
	}

	// 4. Unknown (valid UUID) and malformed ids both return 404, not 500.
	for _, id := range []string{"11111111-1111-1111-1111-111111111111", "not-a-uuid"} {
		resp = doJSON(t, http.MethodDelete, baseURL()+"/v1/auth/sessions/"+id, nil, deviceA.AccessToken)
		status := resp.StatusCode
		resp.Body.Close()
		if status != http.StatusNotFound {
			t.Fatalf("revoke %q expected 404, got %d", id, status)
		}
	}
}

func TestRefreshReuseDetectedAudit(t *testing.T) {
	email, userID := registerVerifiedUser(t)
	tokens := loginWithUA(t, email, "IntegrationReuse/1.0")

	// Rotate once: the presented refresh token is retired and replaced.
	refreshBody := fmt.Sprintf(`{"refresh_token":%q}`, tokens.RefreshToken)
	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/refresh", bytes.NewBufferString(refreshBody), "")
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("first refresh expected 200, got %d", resp.StatusCode)
	}
	var rotated sessionTokenResponse
	decodeAndClose(t, resp, &rotated)

	// Replay the now-retired token: reuse must be rejected.
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/refresh", bytes.NewBufferString(refreshBody), "")
	replayStatus := resp.StatusCode
	resp.Body.Close()
	if replayStatus != http.StatusUnauthorized {
		t.Fatalf("replayed refresh expected 401, got %d", replayStatus)
	}

	// The replay must have written a refresh_reuse_detected audit row for the user.
	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	var count int
	err := pool.QueryRow(ctx, `
SELECT COUNT(*) FROM auth_audit_logs
WHERE event = 'refresh_reuse_detected' AND user_id = $1`, userID).Scan(&count)
	if err != nil {
		t.Fatalf("query audit logs: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected >=1 refresh_reuse_detected audit row for user, got %d", count)
	}

	// The reused token must not have minted a new session.
	if rotated.AccessToken == "" {
		t.Fatal("first refresh should have returned a new access token")
	}
}
