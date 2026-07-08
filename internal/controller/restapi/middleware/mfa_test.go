package middleware_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequireFreshMFA locks the step-up gate contract (A-3 AC-1 + AC-2): the
// middleware maps the usecase verdict to pass / 403 enrollment-required /
// 403 step-up-required with the frozen error codes FE branches on.
func TestRequireFreshMFA(t *testing.T) {
	t.Parallel()

	admin := entity.User{ID: "admin-1", Role: entity.UserRoleAdmin}

	tests := []struct {
		name           string
		localUser      entity.User
		gate           entity.MFAGateDecision
		gateErr        error
		expectedStatus int
		expectedCode   string
	}{
		{
			name:           "allowed passes through",
			localUser:      admin,
			gate:           entity.MFAGateAllowed,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "enrollment required locks out (AC-1)",
			localUser:      admin,
			gate:           entity.MFAGateEnrollmentRequired,
			expectedStatus: http.StatusForbidden,
			expectedCode:   "mfa_enrollment_required",
		},
		{
			name:           "stale session demands step-up (AC-2)",
			localUser:      admin,
			gate:           entity.MFAGateStepUpRequired,
			expectedStatus: http.StatusForbidden,
			expectedCode:   "mfa_step_up_required",
		},
		{
			name:           "gate error is a 500, never a silent pass",
			localUser:      admin,
			gateErr:        errors.New("db down"),
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "no authenticated user is unauthorized",
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			app := fiber.New()
			app.Use(func(ctx *fiber.Ctx) error {
				if tc.localUser.ID != "" {
					ctx.Locals("user", tc.localUser)
					ctx.Locals("userID", tc.localUser.ID)
					ctx.Locals("sessionID", "family-1")
				}

				return ctx.Next()
			})
			app.Use(middleware.RequireFreshMFA(&stubUserUseCase{
				user:       tc.localUser,
				mfaGate:    tc.gate,
				mfaGateErr: tc.gateErr,
			}))
			app.Post("/destructive", func(ctx *fiber.Ctx) error {
				return ctx.SendStatus(http.StatusOK)
			})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/destructive", http.NoBody)
			resp, err := app.Test(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.expectedStatus, resp.StatusCode)

			if tc.expectedCode == "" {
				return
			}

			raw, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			var envelope struct {
				Code      string `json:"code"`
				RequestID string `json:"request_id"`
			}
			require.NoError(t, json.Unmarshal(raw, &envelope))
			assert.Equal(t, tc.expectedCode, envelope.Code, "frozen machine code")
		})
	}
}
