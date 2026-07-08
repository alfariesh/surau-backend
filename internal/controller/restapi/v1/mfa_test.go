package v1

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newMFATestApp mounts the MFA handlers the way the router does: public
// challenge endpoints bare, session endpoints with locals injected.
func newMFATestApp(user *fakeAuthUser) *fiber.App {
	app := fiber.New()
	controller := &V1{
		u: user,
		l: logger.New("error"),
		v: validator.New(validator.WithRequiredStructEnabled()),
	}

	withSession := func(handler fiber.Handler) fiber.Handler {
		return func(ctx *fiber.Ctx) error {
			ctx.Locals("userID", "user-id-123")
			ctx.Locals("sessionID", "family-123")

			return handler(ctx)
		}
	}

	app.Post("/auth/mfa/verify", controller.mfaVerify)
	app.Post("/auth/mfa/reset/request", controller.mfaResetRequest)
	app.Post("/auth/mfa/reset/confirm", controller.mfaResetConfirm)
	app.Post("/auth/mfa/enroll", withSession(controller.mfaEnroll))
	app.Post("/auth/mfa/enroll/confirm", withSession(controller.mfaEnrollConfirm))
	app.Post("/auth/mfa/step-up", withSession(controller.mfaStepUp))
	app.Post("/auth/mfa/disable", withSession(controller.mfaDisable))
	app.Post("/auth/mfa/recovery-codes", withSession(controller.mfaRecoveryCodes))
	app.Get("/auth/mfa/status", withSession(controller.mfaStatus))
	// Login rides the same fake to prove the challenge branch.
	app.Post("/auth/login", controller.login)

	return app
}

func mfaJSONRequest(t *testing.T, app *fiber.App, method, path string, body any) (*http.Response, []byte) {
	t.Helper()

	var reader io.Reader = http.NoBody

	if body != nil {
		payload, err := json.Marshal(body)
		require.NoError(t, err)

		reader = bytes.NewReader(payload)
	}

	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp, raw
}

func TestLoginReturnsMFAChallenge(t *testing.T) {
	t.Parallel()

	fake := &fakeAuthUser{}
	fake.mfaVerifyResult = entity.LoginResult{}
	fake.loginResult = entity.LoginResult{
		MFARequired:       true,
		MFAToken:          "challenge-token-abcdef",
		MFATokenExpiresAt: time.Now().Add(5 * time.Minute),
	}

	app := newMFATestApp(fake)

	resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/login", map[string]string{
		"email":    "admin@example.com",
		"password": "password123",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		MFARequired  bool   `json:"mfa_required"`
		MFAToken     string `json:"mfa_token"`
		ExpiresIn    int64  `json:"expires_in"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	assert.True(t, body.MFARequired)
	assert.Equal(t, "challenge-token-abcdef", body.MFAToken)
	assert.Positive(t, body.ExpiresIn)
	assert.Empty(t, body.AccessToken, "no tokens before the second factor")
	assert.Empty(t, body.RefreshToken)
}

func TestMFAVerifyHandler(t *testing.T) {
	t.Parallel()

	t.Run("success returns the token pair", func(t *testing.T) {
		t.Parallel()

		fake := &fakeAuthUser{mfaVerifyResult: entity.LoginResult{
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
			SessionID:    "family-123",
		}}
		app := newMFATestApp(fake)

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/verify", map[string]string{
			"mfa_token": "challenge-token-abcdef",
			"code":      "123456",
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(raw), "access-1")
	})

	t.Run("error statuses and frozen codes", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name       string
			err        error
			wantStatus int
			wantCode   string
		}{
			{"wrong code", entity.ErrInvalidMFACode, http.StatusUnauthorized, "invalid_mfa_code"},
			{"dead challenge", entity.ErrInvalidMFAChallenge, http.StatusUnauthorized, "invalid_mfa_challenge"},
			{"rate limited", entity.ErrAuthRateLimited, http.StatusTooManyRequests, ""},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				app := newMFATestApp(&fakeAuthUser{mfaVerifyErr: tc.err})

				resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/verify", map[string]string{
					"mfa_token": "challenge-token-abcdef",
					"code":      "123456",
				})
				assert.Equal(t, tc.wantStatus, resp.StatusCode)

				if tc.wantCode != "" {
					var envelope struct {
						Code string `json:"code"`
					}
					require.NoError(t, json.Unmarshal(raw, &envelope))
					assert.Equal(t, tc.wantCode, envelope.Code)
				}
			})
		}
	})

	t.Run("malformed body is a 400", func(t *testing.T) {
		t.Parallel()

		app := newMFATestApp(&fakeAuthUser{})

		resp, _ := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/verify", map[string]string{"code": "123456"})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestMFAEnrollHandlers(t *testing.T) {
	t.Parallel()

	t.Run("enroll returns provisioning material", func(t *testing.T) {
		t.Parallel()

		fake := &fakeAuthUser{mfaEnrollment: entity.MFAEnrollment{
			Secret:     "JBSWY3DPEHPK3PXP",
			OTPAuthURL: "otpauth://totp/Surau:a@b?secret=JBSWY3DPEHPK3PXP",
		}}
		app := newMFATestApp(fake)

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/enroll", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(raw), "otpauth://totp/")
	})

	t.Run("enroll conflict when already enabled", func(t *testing.T) {
		t.Parallel()

		app := newMFATestApp(&fakeAuthUser{mfaEnrollErr: entity.ErrMFAAlreadyEnabled})

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/enroll", nil)
		assert.Equal(t, http.StatusConflict, resp.StatusCode)

		var envelope struct {
			Code string `json:"code"`
		}
		require.NoError(t, json.Unmarshal(raw, &envelope))
		assert.Equal(t, "mfa_already_enabled", envelope.Code)
	})

	t.Run("confirm returns the recovery codes once", func(t *testing.T) {
		t.Parallel()

		fake := &fakeAuthUser{mfaRecoveryCodes: []string{"AAAA-BBBB-CCCC-DDDD", "EEEE-FFFF-GGGG-HHHH"}}
		app := newMFATestApp(fake)

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/enroll/confirm", map[string]string{"code": "123456"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(raw), "AAAA-BBBB-CCCC-DDDD")
	})

	t.Run("confirm without pending enrollment is a 400", func(t *testing.T) {
		t.Parallel()

		app := newMFATestApp(&fakeAuthUser{mfaConfirmErr: entity.ErrMFAEnrollmentNotStarted})

		resp, _ := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/enroll/confirm", map[string]string{"code": "123456"})
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestMFAStepUpAndManagementHandlers(t *testing.T) {
	t.Parallel()

	t.Run("step-up returns the freshness deadline", func(t *testing.T) {
		t.Parallel()

		deadline := time.Now().Add(10 * time.Minute).UTC()
		app := newMFATestApp(&fakeAuthUser{mfaStepUpAt: deadline})

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/step-up", map[string]string{"code": "123456"})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(raw), `"stepped_up":true`)
	})

	t.Run("disable requires fresh step-up", func(t *testing.T) {
		t.Parallel()

		app := newMFATestApp(&fakeAuthUser{mfaDisableErr: entity.ErrMFAStepUpRequired})

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/disable", nil)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)

		var envelope struct {
			Code string `json:"code"`
		}
		require.NoError(t, json.Unmarshal(raw, &envelope))
		assert.Equal(t, "mfa_step_up_required", envelope.Code)
	})

	t.Run("recovery-codes regenerate returns fresh set", func(t *testing.T) {
		t.Parallel()

		app := newMFATestApp(&fakeAuthUser{mfaRecoveryCodes: []string{"NEW1-NEW1-NEW1-NEW1"}})

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/recovery-codes", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(raw), "NEW1-NEW1-NEW1-NEW1")
	})

	t.Run("status maps the entity fields", func(t *testing.T) {
		t.Parallel()

		enforced := time.Now().UTC()
		app := newMFATestApp(&fakeAuthUser{mfaStatus: entity.MFAStatus{
			Enabled: true, Required: true, EnforcedFrom: &enforced, RecoveryCodesRemaining: 9,
		}})

		resp, raw := mfaJSONRequest(t, app, http.MethodGet, "/auth/mfa/status", nil)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var body struct {
			Enabled                bool `json:"enabled"`
			Required               bool `json:"required"`
			RecoveryCodesRemaining int  `json:"recovery_codes_remaining"`
		}
		require.NoError(t, json.Unmarshal(raw, &body))
		assert.True(t, body.Enabled)
		assert.True(t, body.Required)
		assert.Equal(t, 9, body.RecoveryCodesRemaining)
	})
}

func TestMFAResetHandlers(t *testing.T) {
	t.Parallel()

	t.Run("request returns 202 with the reset token", func(t *testing.T) {
		t.Parallel()

		fake := &fakeAuthUser{
			mfaResetToken:     "reset-token-xyz",
			mfaResetExpiresAt: time.Now().Add(15 * time.Minute),
		}
		app := newMFATestApp(fake)

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/reset/request", map[string]string{
			"mfa_token": "challenge-token-abcdef",
		})
		require.Equal(t, http.StatusAccepted, resp.StatusCode)
		assert.Contains(t, string(raw), "reset-token-xyz")
	})

	t.Run("request surfaces email outage as 503", func(t *testing.T) {
		t.Parallel()

		app := newMFATestApp(&fakeAuthUser{mfaResetRequestErr: entity.ErrEmailDeliveryFailed})

		resp, _ := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/reset/request", map[string]string{
			"mfa_token": "challenge-token-abcdef",
		})
		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	})

	t.Run("confirm succeeds", func(t *testing.T) {
		t.Parallel()

		app := newMFATestApp(&fakeAuthUser{})

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/reset/confirm", map[string]string{
			"reset_token":   "reset-token-xyz-12345",
			"otp":           "123456",
			"recovery_code": "AAAA-BBBB-CCCC-DDDD",
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, string(raw), `"mfa_reset":true`)
	})

	t.Run("confirm rejects a bad combo with the anti-oracle code", func(t *testing.T) {
		t.Parallel()

		app := newMFATestApp(&fakeAuthUser{mfaResetConfirmErr: entity.ErrInvalidMFAReset})

		resp, raw := mfaJSONRequest(t, app, http.MethodPost, "/auth/mfa/reset/confirm", map[string]string{
			"reset_token":   "reset-token-xyz-12345",
			"otp":           "123456",
			"recovery_code": "AAAA-BBBB-CCCC-DDDD",
		})
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

		var envelope struct {
			Code string `json:"code"`
		}
		require.NoError(t, json.Unmarshal(raw, &envelope))
		assert.Equal(t, "invalid_mfa_reset", envelope.Code)
	})
}
