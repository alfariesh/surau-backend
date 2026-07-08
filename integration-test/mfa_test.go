package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// A-3 end-to-end flows: enrollment, the diverted login, step-up on real
// destructive routes (AC-2), the grace lockout (AC-1), one-time recovery
// codes (AC-3), the lost-device reset, and the untouched non-MFA login.

type mfaChallengeResponse struct {
	MFARequired bool   `json:"mfa_required"`
	MFAToken    string `json:"mfa_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

type mfaEnrollmentResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURL string `json:"otpauth_url"`
}

type mfaRecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

type mfaErrorResponse struct {
	Code      string `json:"code"`
	RequestID string `json:"request_id"`
}

// totpFor mints a valid code for the enrolled secret at the current step.
func totpFor(t *testing.T, secret string) string {
	t.Helper()

	code, err := totp.GenerateCode(secret, time.Now().UTC())
	if err != nil {
		t.Fatalf("generate totp: %v", err)
	}

	return code
}

// resetTOTPStepGuard clears the monotonic replay guard so tests can reuse the
// current 30s step (production never does this; tests move faster than time).
func resetTOTPStepGuard(t *testing.T, userID string) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	if _, err := pool.Exec(ctx, `UPDATE user_mfa SET last_used_totp_step = 0 WHERE user_id = $1`, userID); err != nil {
		t.Fatalf("reset totp step guard: %v", err)
	}
}

// enrollMFA runs enroll + confirm for the logged-in token and returns the
// provisioning secret plus the one-time recovery codes.
func enrollMFA(t *testing.T, accessToken string) (secret string, recoveryCodes []string) {
	t.Helper()

	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/enroll", nil, accessToken)

	var enrollment mfaEnrollmentResponse

	decodeAndClose(t, resp, &enrollment)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mfa enroll expected 200, got %d", resp.StatusCode)
	}

	if enrollment.Secret == "" || enrollment.OTPAuthURL == "" {
		t.Fatalf("mfa enroll returned empty provisioning material: %+v", enrollment)
	}

	confirmBody := fmt.Sprintf(`{"code":%q}`, totpFor(t, enrollment.Secret))
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/enroll/confirm", bytes.NewBufferString(confirmBody), accessToken)

	var codes mfaRecoveryCodesResponse

	decodeAndClose(t, resp, &codes)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mfa enroll confirm expected 200, got %d", resp.StatusCode)
	}

	if len(codes.RecoveryCodes) != 10 {
		t.Fatalf("expected 10 recovery codes, got %d", len(codes.RecoveryCodes))
	}

	return enrollment.Secret, codes.RecoveryCodes
}

// mfaAdmin provisions a verified admin (SQL role grant + grace anchor, the
// same anchor the API/CLI paths stamp) and returns its identifiers.
func mfaAdmin(t *testing.T) (email, userID string) {
	t.Helper()

	email, userID = registerVerifiedUser(t)
	setUserRoleByEmail(t, email, "admin")

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	if _, err := pool.Exec(ctx,
		`UPDATE users SET mfa_enforced_from = now() WHERE id = $1`, userID); err != nil {
		t.Fatalf("stamp mfa_enforced_from: %v", err)
	}

	return email, userID
}

// changeRoleRequest fires the step-up-gated destructive action (role change).
func changeRoleRequest(t *testing.T, accessToken, targetEmail, role string) (*http.Response, mfaErrorResponse) {
	t.Helper()

	body := fmt.Sprintf(`{"email":%q,"role":%q}`, targetEmail, role)
	resp := doJSON(t, http.MethodPatch, baseURL()+"/v1/admin/users/role", bytes.NewBufferString(body), accessToken)

	var envelope mfaErrorResponse

	decodeAndClose(t, resp, &envelope)

	return resp, envelope
}

// TestMFAStepUpGateOnRoleChange is AC-2 end-to-end: fresh MFA opens the
// destructive route, an aged stamp closes it with mfa_step_up_required, and a
// step-up (recovery code) reopens it.
func TestMFAStepUpGateOnRoleChange(t *testing.T) {
	adminEmail, adminID := mfaAdmin(t)
	targetEmail, _ := registerVerifiedUser(t)

	login := loginWithUA(t, adminEmail, "mfa-stepup-test")
	_, recoveryCodes := enrollMFA(t, login.AccessToken)

	// Enroll-confirm just stamped the session fresh: destructive route opens.
	resp, _ := changeRoleRequest(t, login.AccessToken, targetEmail, "editor")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fresh step-up role change expected 200, got %d", resp.StatusCode)
	}

	// Age the stamp via SQL (the test cannot wait 10 minutes): the same
	// session must now be refused (AC-2).
	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	if _, err := pool.Exec(ctx,
		`UPDATE auth_sessions SET mfa_verified_at = now() - interval '1 hour' WHERE user_id = $1`, adminID); err != nil {
		t.Fatalf("age mfa stamp: %v", err)
	}

	resp, envelope := changeRoleRequest(t, login.AccessToken, targetEmail, "user")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("stale step-up expected 403, got %d", resp.StatusCode)
	}

	if envelope.Code != "mfa_step_up_required" {
		t.Fatalf("expected code mfa_step_up_required, got %q", envelope.Code)
	}

	if envelope.RequestID == "" {
		t.Fatal("step-up refusal must carry request_id")
	}

	// Step up with a recovery code (no TOTP replay concerns) and retry.
	stepUpBody := fmt.Sprintf(`{"code":%q}`, recoveryCodes[0])

	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/step-up", bytes.NewBufferString(stepUpBody), login.AccessToken)

	var stepUp struct {
		SteppedUp bool `json:"stepped_up"`
	}

	decodeAndClose(t, resp, &stepUp)

	if resp.StatusCode != http.StatusOK || !stepUp.SteppedUp {
		t.Fatalf("step-up expected 200 stepped_up, got %d %+v", resp.StatusCode, stepUp)
	}

	resp, _ = changeRoleRequest(t, login.AccessToken, targetEmail, "user")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-step-up role change expected 200, got %d", resp.StatusCode)
	}
}

// TestMFAGraceLockout is AC-1 end-to-end: an un-enrolled admin works inside
// the grace window, is locked out of the destructive route once the anchor
// ages past it, and regains access by enrolling.
func TestMFAGraceLockout(t *testing.T) {
	adminEmail, adminID := mfaAdmin(t)
	targetEmail, _ := registerVerifiedUser(t)

	login := loginWithUA(t, adminEmail, "mfa-grace-test")

	// Inside grace (anchor = now): allowed without any MFA.
	resp, _ := changeRoleRequest(t, login.AccessToken, targetEmail, "editor")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("in-grace role change expected 200, got %d", resp.StatusCode)
	}

	// Expire the grace window (default 168h) via SQL.
	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	if _, err := pool.Exec(ctx,
		`UPDATE users SET mfa_enforced_from = now() - interval '30 days' WHERE id = $1`, adminID); err != nil {
		t.Fatalf("age mfa_enforced_from: %v", err)
	}

	resp, envelope := changeRoleRequest(t, login.AccessToken, targetEmail, "user")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("past-grace expected 403, got %d", resp.StatusCode)
	}

	if envelope.Code != "mfa_enrollment_required" {
		t.Fatalf("expected code mfa_enrollment_required, got %q", envelope.Code)
	}

	// Enrolling lifts the lockout (confirm stamps the session fresh).
	enrollMFA(t, login.AccessToken)

	resp, _ = changeRoleRequest(t, login.AccessToken, targetEmail, "user")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-enroll role change expected 200, got %d", resp.StatusCode)
	}
}

// TestMFALoginFlowAndRecoveryCodeOnce covers the diverted login (challenge →
// verify) with TOTP, then proves recovery codes are one-time (AC-3).
func TestMFALoginFlowAndRecoveryCodeOnce(t *testing.T) {
	adminEmail, adminID := mfaAdmin(t)

	login := loginWithUA(t, adminEmail, "mfa-login-test")
	secret, recoveryCodes := enrollMFA(t, login.AccessToken)

	// Login now diverts to the challenge.
	challengeBody := fmt.Sprintf(`{"email":%q,"password":"testpass123"}`, adminEmail)
	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(challengeBody), "")

	var challenge mfaChallengeResponse

	decodeAndClose(t, resp, &challenge)

	if resp.StatusCode != http.StatusOK || !challenge.MFARequired || challenge.MFAToken == "" {
		t.Fatalf("MFA login expected challenge, got %d %+v", resp.StatusCode, challenge)
	}

	// Complete with TOTP (step guard cleared: enroll-confirm used this step).
	resetTOTPStepGuard(t, adminID)

	verifyBody := fmt.Sprintf(`{"mfa_token":%q,"code":%q}`, challenge.MFAToken, totpFor(t, secret))
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/verify", bytes.NewBufferString(verifyBody), "")

	var tokens sessionTokenResponse

	decodeAndClose(t, resp, &tokens)

	if resp.StatusCode != http.StatusOK || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Fatalf("mfa verify expected token pair, got %d", resp.StatusCode)
	}

	// The consumed challenge cannot buy a second session.
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/verify", bytes.NewBufferString(verifyBody), "")

	var replayEnvelope mfaErrorResponse

	decodeAndClose(t, resp, &replayEnvelope)

	if resp.StatusCode != http.StatusUnauthorized || replayEnvelope.Code != "invalid_mfa_challenge" {
		t.Fatalf("challenge replay expected 401 invalid_mfa_challenge, got %d %q", resp.StatusCode, replayEnvelope.Code)
	}

	// Recovery code path: works once (AC-3)...
	useRecovery := func(code string) (*http.Response, mfaErrorResponse) {
		resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(challengeBody), "")

		var freshChallenge mfaChallengeResponse

		decodeAndClose(t, resp, &freshChallenge)

		if resp.StatusCode != http.StatusOK || freshChallenge.MFAToken == "" {
			t.Fatalf("login for recovery expected challenge, got %d", resp.StatusCode)
		}

		body := fmt.Sprintf(`{"mfa_token":%q,"code":%q}`, freshChallenge.MFAToken, code)
		verifyResp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/verify", bytes.NewBufferString(body), "")

		var envelope mfaErrorResponse

		decodeAndClose(t, verifyResp, &envelope)

		return verifyResp, envelope
	}

	resp, _ = useRecovery(recoveryCodes[1])
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recovery code login expected 200, got %d", resp.StatusCode)
	}

	// ...and exactly once.
	resp, envelope := useRecovery(recoveryCodes[1])
	if resp.StatusCode != http.StatusUnauthorized || envelope.Code != "invalid_mfa_code" {
		t.Fatalf("spent recovery code expected 401 invalid_mfa_code, got %d %q (AC-3)", resp.StatusCode, envelope.Code)
	}
}

// TestNonMFALoginUnchanged pins the additive contract: accounts without MFA
// get the exact pre-A-3 login response, no mfa fields.
func TestNonMFALoginUnchanged(t *testing.T) {
	email, _ := registerVerifiedUser(t)

	body := fmt.Sprintf(`{"email":%q,"password":"testpass123"}`, email)
	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(body), "")

	var raw map[string]any

	decodeAndClose(t, resp, &raw)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("plain login expected 200, got %d", resp.StatusCode)
	}

	for _, key := range []string{"token", "access_token", "refresh_token", "token_type", "expires_in", "session_id"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("plain login response missing %q", key)
		}
	}

	if _, ok := raw["mfa_required"]; ok {
		t.Fatal("plain login must not carry mfa_required")
	}
}

// TestMFAResetFlow is the lost-device path: email OTP + recovery code remove
// MFA and revoke every session.
func TestMFAResetFlow(t *testing.T) {
	adminEmail, adminID := mfaAdmin(t)

	login := loginWithUA(t, adminEmail, "mfa-reset-test")
	_, recoveryCodes := enrollMFA(t, login.AccessToken)

	// Reach the challenge state (proof of password).
	challengeBody := fmt.Sprintf(`{"email":%q,"password":"testpass123"}`, adminEmail)
	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(challengeBody), "")

	var challenge mfaChallengeResponse

	decodeAndClose(t, resp, &challenge)

	if !challenge.MFARequired {
		t.Fatalf("expected MFA challenge before reset, got %+v", challenge)
	}

	requestBody := fmt.Sprintf(`{"mfa_token":%q}`, challenge.MFAToken)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/reset/request", bytes.NewBufferString(requestBody), "")

	var reset struct {
		ResetToken string `json:"reset_token"`
		ExpiresIn  int64  `json:"expires_in"`
	}

	decodeAndClose(t, resp, &reset)

	if resp.StatusCode != http.StatusAccepted || reset.ResetToken == "" {
		t.Fatalf("reset request expected 202 with token, got %d", resp.StatusCode)
	}

	// The OTP went out via the log email sender (unreadable); overwrite its
	// bcrypt hash with one computed in-test, the established pattern for
	// asserting emailed codes.
	knownOTP := "424242"

	otpHash, err := bcrypt.GenerateFromPassword([]byte(knownOTP), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("bcrypt otp: %v", err)
	}

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tag, err := pool.Exec(ctx,
		`UPDATE mfa_challenges SET otp_hash = $2 WHERE user_id = $1 AND purpose = 'reset' AND consumed_at IS NULL`,
		adminID, string(otpHash))
	if err != nil || tag.RowsAffected() == 0 {
		t.Fatalf("override reset otp hash: %v (rows=%d)", err, tag.RowsAffected())
	}

	// Wrong recovery code: nothing burns.
	confirmBody := fmt.Sprintf(`{"reset_token":%q,"otp":%q,"recovery_code":"AAAA-AAAA-AAAA-AAAA"}`, reset.ResetToken, knownOTP)
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/reset/confirm", bytes.NewBufferString(confirmBody), "")

	var badEnvelope mfaErrorResponse

	decodeAndClose(t, resp, &badEnvelope)

	if resp.StatusCode != http.StatusUnauthorized || badEnvelope.Code != "invalid_mfa_reset" {
		t.Fatalf("bad combo expected 401 invalid_mfa_reset, got %d %q", resp.StatusCode, badEnvelope.Code)
	}

	// Right combo removes MFA and revokes everything.
	confirmBody = fmt.Sprintf(`{"reset_token":%q,"otp":%q,"recovery_code":%q}`, reset.ResetToken, knownOTP, recoveryCodes[0])
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/mfa/reset/confirm", bytes.NewBufferString(confirmBody), "")

	var done struct {
		MFAReset bool `json:"mfa_reset"`
	}

	decodeAndClose(t, resp, &done)

	if resp.StatusCode != http.StatusOK || !done.MFAReset {
		t.Fatalf("reset confirm expected 200 mfa_reset, got %d", resp.StatusCode)
	}

	// The pre-reset session died with the revoke.
	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/auth/introspect", nil, login.AccessToken)
	resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session after reset expected 401, got %d", resp.StatusCode)
	}

	// Password-only login is back to the plain flow.
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/auth/login", bytes.NewBufferString(challengeBody), "")

	var raw map[string]any

	decodeAndClose(t, resp, &raw)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("post-reset login expected 200, got %d", resp.StatusCode)
	}

	if _, ok := raw["mfa_required"]; ok {
		t.Fatal("post-reset login must be plain (no mfa_required)")
	}
}
