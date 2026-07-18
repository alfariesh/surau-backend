package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	ID          string    `json:"id"`
	UserAgent   string    `json:"user_agent"`
	DeviceLabel string    `json:"device_label"`
	LastUsedAt  time.Time `json:"last_used_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	IsCurrent   bool      `json:"is_current"`
}

type sessionListResponse struct {
	Sessions []sessionInfo `json:"items"`
}

// registerVerifiedUser registers a fresh user, marks it verified, and returns
// the login email plus the user id.
func registerVerifiedUser(t *testing.T) (email, userID string) {
	t.Helper()

	stamp := time.Now().UnixNano()
	email = fmt.Sprintf("session_%d@test.local", stamp)
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

	deviceAUserAgent := "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36"
	deviceA := loginWithUA(t, email, deviceAUserAgent)
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

			if s.UserAgent != deviceAUserAgent {
				t.Fatalf("current session has user_agent %q, want device A", s.UserAgent)
			}

			if s.DeviceLabel != "Chrome di Mac" {
				t.Fatalf("current session has device_label %q, want Chrome di Mac", s.DeviceLabel)
			}

			if delta := s.ExpiresAt.Sub(s.LastUsedAt); delta != 336*time.Hour {
				t.Fatalf("current session window = %s, want exactly 336h", delta)
			}
		} else {
			deviceBSessionID = s.ID
			if s.DeviceLabel != "Perangkat tidak dikenal" {
				t.Fatalf("unknown metadata label = %q, want safe fallback", s.DeviceLabel)
			}
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

func TestRefreshSlidingWindowForExistingSessions(t *testing.T) {
	email, userID := registerVerifiedUser(t)
	tokens := loginWithUA(t, email, "SurauAndroid/4.2.0 (Android 15)")

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	// Simulate a legacy 720h row that was still active thirteen days ago.
	_, err := pool.Exec(ctx, `
UPDATE auth_sessions
SET last_used_at = now() - interval '13 days',
    expires_at = now() + interval '30 days'
WHERE family_id = $1 AND revoked_at IS NULL`, tokens.SessionID)
	if err != nil {
		t.Fatalf("age active legacy session: %v", err)
	}

	legacyList := listSessions(t, tokens.AccessToken)
	if len(legacyList.Sessions) != 1 {
		t.Fatalf("active legacy session list expected 1 row, got %d", len(legacyList.Sessions))
	}

	if delta := legacyList.Sessions[0].ExpiresAt.Sub(legacyList.Sessions[0].LastUsedAt); delta != 336*time.Hour {
		t.Fatalf("legacy effective list window = %s, want 336h", delta)
	}

	refreshBody := fmt.Sprintf(`{"refresh_token":%q}`, tokens.RefreshToken)

	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/refresh", bytes.NewBufferString(refreshBody), "")
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("active legacy refresh expected 200, got %d", resp.StatusCode)
	}

	var rotated sessionTokenResponse
	decodeAndClose(t, resp, &rotated)

	var successorWindowSeconds int64

	err = pool.QueryRow(ctx, `
SELECT EXTRACT(EPOCH FROM (expires_at - last_used_at))::bigint
FROM auth_sessions
WHERE family_id = $1 AND revoked_at IS NULL`, tokens.SessionID).Scan(&successorWindowSeconds)
	if err != nil {
		t.Fatalf("read successor window: %v", err)
	}

	if successorWindowSeconds != int64((336*time.Hour)/time.Second) {
		t.Fatalf("successor window = %ds, want exactly 336h", successorWindowSeconds)
	}

	// The same stored 30-day expiry must not rescue a row that has been idle
	// beyond the new 14-day boundary.
	_, err = pool.Exec(ctx, `
UPDATE auth_sessions
SET last_used_at = now() - interval '14 days 1 minute',
    expires_at = now() + interval '30 days'
WHERE family_id = $1 AND revoked_at IS NULL`, tokens.SessionID)
	if err != nil {
		t.Fatalf("age idle legacy session: %v", err)
	}

	list := listSessions(t, rotated.AccessToken)
	if len(list.Sessions) != 0 {
		t.Fatalf("idle session must be hidden from list, got %d rows", len(list.Sessions))
	}

	idleBody := fmt.Sprintf(`{"refresh_token":%q}`, rotated.RefreshToken)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/refresh", bytes.NewBufferString(idleBody), "")
	idleStatus := resp.StatusCode
	resp.Body.Close()

	if idleStatus != http.StatusUnauthorized {
		t.Fatalf("idle legacy refresh expected 401, got %d", idleStatus)
	}

	var reuseAudits int

	err = pool.QueryRow(ctx, `
SELECT COUNT(*) FROM auth_audit_logs
WHERE event = 'refresh_reuse_detected' AND user_id = $1`, userID).Scan(&reuseAudits)
	if err != nil {
		t.Fatalf("query idle audit logs: %v", err)
	}

	if reuseAudits != 0 {
		t.Fatalf("ordinary idle expiry wrote %d reuse audits, want 0", reuseAudits)
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

	// Reuse revokes the whole family, including the successor that was valid
	// immediately before the replay.
	rotatedBody := fmt.Sprintf(`{"refresh_token":%q}`, rotated.RefreshToken)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/refresh", bytes.NewBufferString(rotatedBody), "")
	rotatedStatus := resp.StatusCode
	resp.Body.Close()

	if rotatedStatus != http.StatusUnauthorized {
		t.Fatalf("successor after family reuse expected 401, got %d", rotatedStatus)
	}
}

func TestConcurrentRefreshHasOneWinnerAndRevokesFamily(t *testing.T) {
	email, _ := registerVerifiedUser(t)
	tokens := loginWithUA(t, email, "ConcurrentRefresh/1.0")
	body := fmt.Sprintf(`{"refresh_token":%q}`, tokens.RefreshToken)

	type refreshAttempt struct {
		status int
		tokens sessionTokenResponse
		err    error
	}

	start := make(chan struct{})
	results := make(chan refreshAttempt, 2)

	for range 2 {
		go func() {
			<-start

			ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
			defer cancel()

			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodPost,
				baseURL()+"/v1/auth/refresh",
				bytes.NewBufferString(body),
			)
			if err != nil {
				results <- refreshAttempt{err: err}

				return
			}

			req.Header.Set("Content-Type", "application/json")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				results <- refreshAttempt{err: err}

				return
			}
			defer resp.Body.Close()

			attempt := refreshAttempt{status: resp.StatusCode}
			if resp.StatusCode == http.StatusOK {
				attempt.err = json.NewDecoder(resp.Body).Decode(&attempt.tokens)
			}

			results <- attempt
		}()
	}

	close(start)

	winners := make([]sessionTokenResponse, 0, 1)
	unauthorized := 0

	for range 2 {
		attempt := <-results
		if attempt.err != nil {
			t.Fatalf("concurrent refresh request: %v", attempt.err)
		}

		switch attempt.status {
		case http.StatusOK:
			winners = append(winners, attempt.tokens)
		case http.StatusUnauthorized:
			unauthorized++
		default:
			t.Fatalf("concurrent refresh returned unexpected status %d", attempt.status)
		}
	}

	if len(winners) != 1 || unauthorized != 1 {
		t.Fatalf("concurrent refresh winners=%d unauthorized=%d, want 1/1", len(winners), unauthorized)
	}

	// The losing spend is intentional reuse detection, so it revokes the
	// successor issued to the winner as well.
	winnerBody := fmt.Sprintf(`{"refresh_token":%q}`, winners[0].RefreshToken)
	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/refresh", bytes.NewBufferString(winnerBody), "")
	status := resp.StatusCode
	resp.Body.Close()

	if status != http.StatusUnauthorized {
		t.Fatalf("winner successor after concurrent reuse expected 401, got %d", status)
	}
}
