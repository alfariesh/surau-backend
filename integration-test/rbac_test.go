package integration_test

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
)

// A-1 capability RBAC, end-to-end against the real stack. The capability gate
// must preserve today's behavior (editor sees editorial review, plain user
// does not) and the two new roles must be grantable additively.

// roleUserToken registers a verified user, promotes it to role via SQL, and
// logs in — returning a bearer token for that role.
func roleUserToken(t *testing.T, role string) string {
	t.Helper()

	email, _ := registerVerifiedUser(t)
	if role != "user" {
		setUserRoleByEmail(t, email, role)
	}

	return loginWithUA(t, email, "rbac-test").AccessToken
}

// TestReviewEditorialCapabilityGate proves the review-editorial gate: editor
// and admin pass, a plain user is forbidden (403), unauthenticated is 401.
func TestReviewEditorialCapabilityGate(t *testing.T) {
	const reviewRoute = "/v1/editorial/books"

	t.Run("editor passes the review gate", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, baseURL()+reviewRoute, nil, roleUserToken(t, "editor"))
		resp.Body.Close()

		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("editor must pass review-editorial, got %d", resp.StatusCode)
		}
	})

	t.Run("admin passes the review gate (superset)", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, baseURL()+reviewRoute, nil, adminJWT(t))
		resp.Body.Close()

		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			t.Fatalf("admin must pass review-editorial, got %d", resp.StatusCode)
		}
	})

	t.Run("plain user is forbidden", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, baseURL()+reviewRoute, nil, roleUserToken(t, "user"))

		var envelope struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}

		decodeAndClose(t, resp, &envelope)

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("plain user must be forbidden from review-editorial, got %d", resp.StatusCode)
		}

		if envelope.Error != "forbidden" {
			t.Fatalf("expected 'forbidden' envelope, got %q", envelope.Error)
		}
	})

	t.Run("curator is forbidden (not in review-editorial)", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, baseURL()+reviewRoute, nil, roleUserToken(t, "curator"))
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("curator must be forbidden from review-editorial in A-1, got %d", resp.StatusCode)
		}
	})

	t.Run("unauthenticated is 401", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, baseURL()+reviewRoute, nil, "")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unauthenticated must be 401, got %d", resp.StatusCode)
		}
	})
}

// TestRoleManagementAcceptsNewRoles proves the role API additively accepts
// curator + scholar_reviewer and rejects an unknown role. Granting a role that
// requires MFA (scholar_reviewer) must flip that account's MFA status to
// required.
func TestRoleManagementAcceptsNewRoles(t *testing.T) {
	adminToken := adminJWT(t)

	setRole := func(t *testing.T, email, role string) *http.Response {
		t.Helper()

		body := fmt.Sprintf(`{"email":%q,"role":%q}`, email, role)

		return doJSON(t, http.MethodPatch, baseURL()+"/v1/admin/users/role", bytes.NewBufferString(body), adminToken)
	}

	t.Run("promote to curator succeeds", func(t *testing.T) {
		email, _ := registerVerifiedUser(t)

		resp := setRole(t, email, "curator")

		var user struct {
			Role string `json:"role"`
		}

		decodeAndClose(t, resp, &user)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("promote to curator expected 200, got %d", resp.StatusCode)
		}

		if user.Role != "curator" {
			t.Fatalf("expected role curator, got %q", user.Role)
		}
	})

	t.Run("promote to scholar_reviewer makes MFA required", func(t *testing.T) {
		email, _ := registerVerifiedUser(t)

		resp := setRole(t, email, "scholar_reviewer")
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("promote to scholar_reviewer expected 200, got %d", resp.StatusCode)
		}

		// The scholar_reviewer now sees MFA as mandatory.
		token := loginWithUA(t, email, "rbac-mfa-test").AccessToken

		statusResp := doJSON(t, http.MethodGet, baseURL()+"/v1/auth/mfa/status", nil, token)

		var status struct {
			Required bool `json:"required"`
		}

		decodeAndClose(t, statusResp, &status)

		if statusResp.StatusCode != http.StatusOK {
			t.Fatalf("mfa status expected 200, got %d", statusResp.StatusCode)
		}

		if !status.Required {
			t.Fatal("scholar_reviewer must have MFA required=true (policy.RoleRequiresMFA)")
		}
	})

	t.Run("unknown role is rejected", func(t *testing.T) {
		email, _ := registerVerifiedUser(t)

		resp := setRole(t, email, "overlord")
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("unknown role expected 400, got %d", resp.StatusCode)
		}
	})
}
