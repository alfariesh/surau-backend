package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// A-3 MFA handlers. Public endpoints authenticate with a challenge token
// (proof of password from login); the rest ride the normal Bearer session.

// currentSessionFamily returns the caller's session family ("sid" claim).
func currentSessionFamily(ctx *fiber.Ctx) string {
	familyID, _ := ctx.Locals("sessionID").(string)

	return familyID
}

// @Summary     Complete MFA login
// @Description Exchange the login MFA challenge + a TOTP or recovery code for the token pair
// @ID          mfa-verify
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.MFAVerify true "Challenge token and code"
// @Success     200     {object} response.Token
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/mfa/verify [post]
func (r *V1) mfaVerify(ctx *fiber.Ctx) error {
	var body request.MFAVerify

	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	result, err := r.u.VerifyMFALogin(restAuthContext(ctx), body.MFAToken, body.Code)
	if err != nil {
		r.l.Error(err, "restapi - v1 - mfaVerify")

		return mfaErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.NewToken(&result))
}

// @Summary     Start MFA enrollment
// @Description Provision a pending TOTP secret; confirm it with /auth/mfa/enroll/confirm
// @ID          mfa-enroll
// @Tags        auth
// @Produce     json
// @Success     200 {object} response.MFAEnrollment
// @Failure     401 {object} response.Error
// @Failure     409 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /auth/mfa/enroll [post]
func (r *V1) mfaEnroll(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	enrollment, err := r.u.StartMFAEnrollment(restAuthContext(ctx), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - mfaEnroll")

		return mfaErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.MFAEnrollment{
		Secret:     enrollment.Secret,
		OTPAuthURL: enrollment.OTPAuthURL,
	})
}

// @Summary     Confirm MFA enrollment
// @Description Activate MFA with the first authenticator code; returns the one-time recovery codes
// @ID          mfa-enroll-confirm
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.MFACode true "First TOTP code"
// @Success     200     {object} response.MFARecoveryCodes
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     409     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /auth/mfa/enroll/confirm [post]
func (r *V1) mfaEnrollConfirm(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.MFACode

	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	codes, err := r.u.ConfirmMFAEnrollment(restAuthContext(ctx), userID, currentSessionFamily(ctx), body.Code)
	if err != nil {
		r.l.Error(err, "restapi - v1 - mfaEnrollConfirm")

		return mfaErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.MFARecoveryCodes{RecoveryCodes: codes})
}

// @Summary     MFA step-up
// @Description Re-prove a second factor so destructive admin actions open for the freshness window
// @ID          mfa-step-up
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.MFACode true "TOTP or recovery code"
// @Success     200     {object} response.MFAStepUp
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /auth/mfa/step-up [post]
func (r *V1) mfaStepUp(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.MFACode

	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	expiresAt, err := r.u.StepUpMFA(restAuthContext(ctx), userID, currentSessionFamily(ctx), body.Code)
	if err != nil {
		r.l.Error(err, "restapi - v1 - mfaStepUp")

		return mfaErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.MFAStepUp{SteppedUp: true, ExpiresAt: expiresAt})
}

// @Summary     Disable MFA
// @Description Remove MFA (requires a fresh step-up); revokes every session and returns a fresh pair
// @ID          mfa-disable
// @Tags        auth
// @Produce     json
// @Success     200 {object} response.Token
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /auth/mfa/disable [post]
func (r *V1) mfaDisable(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	result, err := r.u.DisableMFA(restAuthContext(ctx), userID, currentSessionFamily(ctx))
	if err != nil {
		r.l.Error(err, "restapi - v1 - mfaDisable")

		return mfaErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.NewToken(&result))
}

// @Summary     Regenerate recovery codes
// @Description Replace all recovery codes (requires a fresh step-up); old codes stop working
// @ID          mfa-recovery-codes
// @Tags        auth
// @Produce     json
// @Success     200 {object} response.MFARecoveryCodes
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /auth/mfa/recovery-codes [post]
func (r *V1) mfaRecoveryCodes(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	codes, err := r.u.RegenerateMFARecoveryCodes(restAuthContext(ctx), userID, currentSessionFamily(ctx))
	if err != nil {
		r.l.Error(err, "restapi - v1 - mfaRecoveryCodes")

		return mfaErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.MFARecoveryCodes{RecoveryCodes: codes})
}

// @Summary     MFA status
// @Description Current MFA state for the calling account (settings screen, enrollment banners)
// @ID          mfa-status
// @Tags        auth
// @Produce     json
// @Success     200 {object} response.MFAStatus
// @Failure     401 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /auth/mfa/status [get]
func (r *V1) mfaStatus(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	status, err := r.u.MFAStatus(restAuthContext(ctx), userID, currentSessionFamily(ctx))
	if err != nil {
		r.l.Error(err, "restapi - v1 - mfaStatus")

		return mfaErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.NewMFAStatus(&status))
}

// @Summary     Request MFA reset
// @Description Lost-device flow: from the login challenge, email a reset OTP to combine with a recovery code
// @ID          mfa-reset-request
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.MFAResetRequest true "Login challenge token"
// @Success     202     {object} response.MFAResetChallenge
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     503     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/mfa/reset/request [post]
func (r *V1) mfaResetRequest(ctx *fiber.Ctx) error {
	var body request.MFAResetRequest

	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	resetToken, expiresAt, err := r.u.RequestMFAReset(restAuthContext(ctx), body.MFAToken)
	if err != nil {
		r.l.Error(err, "restapi - v1 - mfaResetRequest")

		if errors.Is(err, entity.ErrEmailDeliveryFailed) {
			return errorResponse(ctx, http.StatusServiceUnavailable, "email delivery failed")
		}

		return mfaErrorResponse(ctx, err)
	}

	expiresIn := max(int64(time.Until(expiresAt).Seconds()), 0)

	return ctx.Status(http.StatusAccepted).JSON(response.MFAResetChallenge{ResetToken: resetToken, ExpiresIn: expiresIn})
}

// @Summary     Confirm MFA reset
// @Description Remove MFA with the emailed OTP + one recovery code; every session is revoked
// @ID          mfa-reset-confirm
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.MFAResetConfirm true "Reset token, OTP, and recovery code"
// @Success     200     {object} response.MFAResetDone
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/mfa/reset/confirm [post]
func (r *V1) mfaResetConfirm(ctx *fiber.Ctx) error {
	var body request.MFAResetConfirm

	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.u.ConfirmMFAReset(restAuthContext(ctx), body.ResetToken, body.OTP, body.RecoveryCode); err != nil {
		r.l.Error(err, "restapi - v1 - mfaResetConfirm")

		return mfaErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.MFAResetDone{MFAReset: true})
}

// mfaErrorResponse is the shared usecase-error → envelope ladder for the MFA
// endpoints (message literals are frozen in apierror).
func mfaErrorResponse(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrInvalidMFACode):
		return errorResponse(ctx, http.StatusUnauthorized, "invalid mfa code")
	case errors.Is(err, entity.ErrInvalidMFAChallenge):
		return errorResponse(ctx, http.StatusUnauthorized, "invalid mfa challenge")
	case errors.Is(err, entity.ErrInvalidMFAReset):
		return errorResponse(ctx, http.StatusUnauthorized, "invalid mfa reset")
	case errors.Is(err, entity.ErrMFAAlreadyEnabled):
		return errorResponse(ctx, http.StatusConflict, "mfa already enabled")
	case errors.Is(err, entity.ErrMFANotEnabled):
		return errorResponse(ctx, http.StatusBadRequest, "mfa not enabled")
	case errors.Is(err, entity.ErrMFAEnrollmentNotStarted):
		return errorResponse(ctx, http.StatusBadRequest, "mfa enrollment not started")
	case errors.Is(err, entity.ErrMFAStepUpRequired):
		return errorResponse(ctx, http.StatusForbidden, "mfa step-up required")
	case errors.Is(err, entity.ErrMFAEnrollmentRequired):
		return errorResponse(ctx, http.StatusForbidden, "mfa enrollment required")
	case errors.Is(err, entity.ErrUserNotFound):
		return errorResponse(ctx, http.StatusUnauthorized, "invalid mfa challenge")
	case errors.Is(err, entity.ErrAuthRateLimited), errors.Is(err, entity.ErrAccountLocked):
		return rateLimitedResponse(ctx, err)
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}
