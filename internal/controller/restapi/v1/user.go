package v1

import (
	"context"
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase/authmeta"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Register
// @Description Register a new user
// @ID          register
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.Register true "Registration data"
// @Success     201     {object} entity.User
// @Failure     400     {object} response.Error
// @Failure     409     {object} response.Error
// @Failure     503     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/register [post]
func (r *V1) register(ctx *fiber.Ctx) error {
	var body request.Register

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - register")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - register")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	user, err := r.u.Register(restAuthContext(ctx), registerDisplayName(body), body.Email, body.Password)
	if err != nil {
		r.l.Error(err, "restapi - v1 - register")

		if errors.Is(err, entity.ErrUserAlreadyExists) {
			return errorResponse(ctx, http.StatusConflict, "user already exists")
		}
		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}
		if errors.Is(err, entity.ErrEmailDeliveryFailed) {
			return errorResponse(ctx, http.StatusServiceUnavailable, "email delivery failed")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusCreated).JSON(user)
}

// @Summary     Login
// @Description Authenticate user and get JWT token
// @ID          login
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.Login true "Login credentials"
// @Success     200     {object} response.Token
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     403     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/login [post]
func (r *V1) login(ctx *fiber.Ctx) error {
	var body request.Login

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - login")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - login")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	result, err := r.u.Login(restAuthContext(ctx), body.Email, body.Password)
	if err != nil {
		r.l.Error(err, "restapi - v1 - login")

		if errors.Is(err, entity.ErrInvalidCredentials) {
			return errorResponse(ctx, http.StatusUnauthorized, "invalid credentials")
		}
		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrEmailNotVerified) {
			return errorResponse(ctx, http.StatusForbidden, "email not verified")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) || errors.Is(err, entity.ErrAccountLocked) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.NewToken(result))
}

// @Summary     Refresh session
// @Description Exchange a refresh token for a new access/refresh token pair
// @ID          refresh-session
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.RefreshToken true "Refresh token"
// @Success     200     {object} response.Token
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/refresh [post]
func (r *V1) refreshToken(ctx *fiber.Ctx) error {
	var body request.RefreshToken

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - refreshToken")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - refreshToken")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	result, err := r.u.RefreshSession(restAuthContext(ctx), body.RefreshToken)
	if err != nil {
		r.l.Error(err, "restapi - v1 - refreshToken")

		if errors.Is(err, entity.ErrInvalidRefreshToken) {
			return errorResponse(ctx, http.StatusUnauthorized, "invalid refresh token")
		}
		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.NewToken(result))
}

// @Summary     Logout
// @Description Revoke the session behind a refresh token. Idempotent: unknown tokens return success.
// @ID          logout
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.Logout true "Refresh token"
// @Success     200     {object} response.LoggedOut
// @Failure     400     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/logout [post]
func (r *V1) logout(ctx *fiber.Ctx) error {
	var body request.Logout

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - logout")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - logout")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.u.Logout(restAuthContext(ctx), body.RefreshToken); err != nil {
		r.l.Error(err, "restapi - v1 - logout")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.LoggedOut{LoggedOut: true})
}

// @Summary     Logout everywhere
// @Description Revoke all of the current user's sessions and invalidate outstanding access tokens
// @ID          logout-all
// @Tags        auth
// @Produce     json
// @Success     200 {object} response.SessionsRevoked
// @Failure     401 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /auth/logout-all [post]
func (r *V1) logoutAll(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	if err := r.u.LogoutAll(restAuthContext(ctx), userID); err != nil {
		r.l.Error(err, "restapi - v1 - logoutAll")

		if errors.Is(err, entity.ErrInvalidCredentials) || errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.SessionsRevoked{SessionsRevoked: true})
}

// @Summary     List active sessions
// @Description List the current user's active devices/sessions (manage devices)
// @ID          list-sessions
// @Tags        auth
// @Produce     json
// @Success     200 {object} response.SessionList
// @Failure     401 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /auth/sessions [get]
func (r *V1) listSessions(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	sessions, err := r.u.ListSessions(restAuthContext(ctx), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listSessions")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	currentFamilyID, _ := ctx.Locals("sessionID").(string)

	return ctx.Status(http.StatusOK).JSON(response.NewSessionList(sessions, currentFamilyID))
}

// @Summary     Revoke a session
// @Description Revoke one of the current user's sessions (sign out a single device)
// @ID          revoke-session
// @Tags        auth
// @Produce     json
// @Param       id  path     string true "Session ID"
// @Success     200 {object} response.SessionRevoked
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /auth/sessions/{id} [delete]
func (r *V1) revokeSession(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	sessionID := ctx.Params("id")

	if err := r.u.RevokeSession(restAuthContext(ctx), userID, sessionID); err != nil {
		r.l.Error(err, "restapi - v1 - revokeSession")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request")
		}
		if errors.Is(err, entity.ErrAuthSessionNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "session not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.SessionRevoked{SessionRevoked: true})
}

// @Summary     Verify email
// @Description Verify a user's email address using a one-time token or 6-digit OTP
// @ID          verify-email
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.VerifyEmail true "Email verification token or email OTP"
// @Success     200     {object} response.EmailVerification
// @Failure     400     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/verify-email [post]
func (r *V1) verifyEmail(ctx *fiber.Ctx) error {
	var body request.VerifyEmail

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - verifyEmail")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - verifyEmail")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.u.VerifyEmail(restAuthContext(ctx), body.Token, body.Email, body.OTP); err != nil {
		r.l.Error(err, "restapi - v1 - verifyEmail")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrInvalidVerificationToken) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid verification token")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.EmailVerification{EmailVerified: true})
}

// @Summary     Resend verification email
// @Description Resend email verification for an existing unverified user
// @ID          resend-verification
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.ResendVerification true "Email address"
// @Success     202     {object} response.Accepted
// @Failure     400     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     503     {object} response.Error
// @Router      /auth/resend-verification [post]
func (r *V1) resendVerification(ctx *fiber.Ctx) error {
	var body request.ResendVerification

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - resendVerification")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - resendVerification")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.u.ResendEmailVerification(restAuthContext(ctx), body.Email); err != nil {
		r.l.Error(err, "restapi - v1 - resendVerification")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrVerificationRateLimited) {
			return errorResponse(ctx, http.StatusTooManyRequests, "verification email recently sent")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}
		if errors.Is(err, entity.ErrEmailDeliveryFailed) {
			return errorResponse(ctx, http.StatusServiceUnavailable, "email delivery failed")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusAccepted).JSON(response.Accepted{Accepted: true})
}

// @Summary     Forgot password
// @Description Send a password reset email when the account exists
// @ID          forgot-password
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.ForgotPassword true "Email address"
// @Success     202     {object} response.Accepted
// @Failure     400     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     503     {object} response.Error
// @Router      /auth/forgot-password [post]
func (r *V1) forgotPassword(ctx *fiber.Ctx) error {
	var body request.ForgotPassword

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - forgotPassword")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - forgotPassword")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.u.ForgotPassword(restAuthContext(ctx), body.Email); err != nil {
		r.l.Error(err, "restapi - v1 - forgotPassword")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrPasswordResetRateLimited) {
			return errorResponse(ctx, http.StatusTooManyRequests, "password reset email recently sent")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}
		if errors.Is(err, entity.ErrEmailDeliveryFailed) {
			return errorResponse(ctx, http.StatusServiceUnavailable, "email delivery failed")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusAccepted).JSON(response.Accepted{Accepted: true})
}

// @Summary     Reset password
// @Description Reset password using a one-time token
// @ID          reset-password
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.ResetPassword true "Password reset token and new password"
// @Success     200     {object} response.PasswordReset
// @Failure     400     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /auth/reset-password [post]
func (r *V1) resetPassword(ctx *fiber.Ctx) error {
	var body request.ResetPassword

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - resetPassword")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - resetPassword")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.u.ResetPassword(restAuthContext(ctx), body.Token, body.Password); err != nil {
		r.l.Error(err, "restapi - v1 - resetPassword")

		if errors.Is(err, entity.ErrInvalidAuthInput) || errors.Is(err, entity.ErrInvalidPasswordResetToken) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.PasswordReset{PasswordReset: true})
}

// @Summary     Change password
// @Description Change the current user's password and revoke older JWTs
// @ID          change-password
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.ChangePassword true "Current and new password"
// @Success     200     {object} response.PasswordChanged
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /auth/change-password [post]
func (r *V1) changePassword(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.ChangePassword

	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - changePassword")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - changePassword")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	result, err := r.u.ChangePassword(restAuthContext(ctx), userID, body.CurrentPassword, body.NewPassword)
	if err != nil {
		r.l.Error(err, "restapi - v1 - changePassword")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrInvalidCredentials) {
			return errorResponse(ctx, http.StatusUnauthorized, "invalid credentials")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	token := response.NewToken(result)

	return ctx.Status(http.StatusOK).JSON(response.PasswordChanged{
		PasswordChanged: true,
		Token:           token.Token,
		AccessToken:     token.AccessToken,
		RefreshToken:    token.RefreshToken,
		TokenType:       token.TokenType,
		ExpiresIn:       token.ExpiresIn,
		SessionID:       token.SessionID,
	})
}

// @Summary     Request email change
// @Description Send a verification link to a new email address
// @ID          request-email-change
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.RequestEmailChange true "Current password and new email"
// @Success     202     {object} response.Accepted
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     409     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     503     {object} response.Error
// @Security    BearerAuth
// @Router      /auth/change-email/request [post]
func (r *V1) requestEmailChange(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.RequestEmailChange
	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - requestEmailChange")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - requestEmailChange")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.u.RequestEmailChange(restAuthContext(ctx), userID, body.CurrentPassword, body.NewEmail); err != nil {
		r.l.Error(err, "restapi - v1 - requestEmailChange")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrInvalidCredentials) {
			return errorResponse(ctx, http.StatusUnauthorized, "invalid credentials")
		}
		if errors.Is(err, entity.ErrUserAlreadyExists) {
			return errorResponse(ctx, http.StatusConflict, "user already exists")
		}
		if errors.Is(err, entity.ErrEmailChangeRateLimited) || errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}
		if errors.Is(err, entity.ErrEmailDeliveryFailed) {
			return errorResponse(ctx, http.StatusServiceUnavailable, "email delivery failed")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusAccepted).JSON(response.Accepted{Accepted: true})
}

// @Summary     Verify email change
// @Description Confirm an email change token or OTP for the current user
// @ID          verify-email-change
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.VerifyEmailChange true "Email change token or OTP"
// @Success     200     {object} response.EmailChanged
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     409     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /auth/change-email/verify [post]
func (r *V1) verifyEmailChange(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.VerifyEmailChange
	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - verifyEmailChange")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - verifyEmailChange")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	result, err := r.u.VerifyEmailChange(restAuthContext(ctx), userID, body.Token, body.OTP)
	if err != nil {
		r.l.Error(err, "restapi - v1 - verifyEmailChange")

		if errors.Is(err, entity.ErrInvalidAuthInput) || errors.Is(err, entity.ErrInvalidEmailChangeToken) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrUserAlreadyExists) {
			return errorResponse(ctx, http.StatusConflict, "user already exists")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	token := response.NewToken(result)

	return ctx.Status(http.StatusOK).JSON(response.EmailChanged{
		EmailChanged: true,
		Token:        token.Token,
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		TokenType:    token.TokenType,
		ExpiresIn:    token.ExpiresIn,
		SessionID:    token.SessionID,
	})
}

// @Summary     Delete account
// @Description Soft-delete and anonymize the current account
// @ID          delete-account
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.DeleteAccount true "Current password"
// @Success     200     {object} response.AccountDeleted
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     429     {object} response.Error
// @Failure     500     {object} response.Error
// @Security    BearerAuth
// @Router      /auth/delete-account [post]
func (r *V1) deleteAccount(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.DeleteAccount
	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - deleteAccount")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - deleteAccount")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.u.DeleteAccount(restAuthContext(ctx), userID, body.CurrentPassword); err != nil {
		r.l.Error(err, "restapi - v1 - deleteAccount")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrInvalidCredentials) {
			return errorResponse(ctx, http.StatusUnauthorized, "invalid credentials")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return rateLimitedResponse(ctx, err, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.AccountDeleted{AccountDeleted: true})
}

// @Summary     Introspect access token
// @Description Return the authenticated identity (id, username, role, session) for the presented Bearer token. Built for service-to-service auth bridging (e.g. the collab websocket server): the Auth middleware has already verified signature, token_version and session revocation, so the response reflects live session state with no extra queries.
// @ID          auth-introspect
// @Tags        auth
// @Produce     json
// @Success     200 {object} response.Introspection
// @Failure     401 {object} response.Error
// @Security    BearerAuth
// @Router      /auth/introspect [get]
func (r *V1) introspect(ctx *fiber.Ctx) error {
	user, ok := ctx.Locals("user").(entity.User)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	sessionID := ""
	if value, ok := ctx.Locals("sessionID").(string); ok {
		sessionID = value
	}

	return ctx.Status(http.StatusOK).JSON(response.Introspection{
		UserID:    user.ID,
		Username:  user.Username,
		Role:      user.Role,
		SessionID: sessionID,
	})
}

// @Summary     Get profile
// @Description Get current user profile
// @ID          profile
// @Tags        user
// @Produce     json
// @Success     200 {object} entity.UserAccount
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /user/profile [get]
func (r *V1) profile(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	user, err := r.u.GetUserAccount(ctx.UserContext(), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - profile")

		if errors.Is(err, entity.ErrUserNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "user not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(user)
}

// @Summary     Update profile
// @Description Update normal profile fields for the current user
// @ID          update-profile
// @Tags        user
// @Accept      json
// @Produce     json
// @Param       request body request.UserProfilePatch true "Profile changes"
// @Success     200 {object} entity.UserAccount
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /user/profile [patch]
func (r *V1) updateProfile(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.UserProfilePatch
	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - updateProfile")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - updateProfile")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	account, err := r.u.UpdateUserProfile(ctx.UserContext(), userID, profilePatchRequestToEntity(body))
	if err != nil {
		r.l.Error(err, "restapi - v1 - updateProfile")

		return userPreferenceErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(account)
}

// @Summary     Complete onboarding
// @Description Store first-run profile and reader preferences for the current user
// @ID          user-onboarding
// @Tags        user
// @Accept      json
// @Produce     json
// @Param       request body request.UserOnboarding true "Onboarding answers"
// @Success     200 {object} entity.UserAccount
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /user/onboarding [patch]
func (r *V1) updateOnboarding(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.UserOnboarding
	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - updateOnboarding")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - updateOnboarding")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	account, err := r.u.CompleteOnboarding(ctx.UserContext(), userID, onboardingRequestToEntity(body))
	if err != nil {
		r.l.Error(err, "restapi - v1 - updateOnboarding")

		return userPreferenceErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(account)
}

// @Summary     Update preferences
// @Description Update reader and Quran preferences for the current user
// @ID          user-preferences
// @Tags        user
// @Accept      json
// @Produce     json
// @Param       request body request.UserPreferencesPatch true "Preference changes"
// @Success     200 {object} entity.UserAccount
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /user/preferences [patch]
func (r *V1) updatePreferences(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok || userID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.UserPreferencesPatch
	if err := ctx.BodyParser(&body); err != nil {
		r.l.Error(err, "restapi - v1 - updatePreferences")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		r.l.Error(err, "restapi - v1 - updatePreferences")

		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	account, err := r.u.UpdateUserPreferences(ctx.UserContext(), userID, preferencesPatchRequestToEntity(body))
	if err != nil {
		r.l.Error(err, "restapi - v1 - updatePreferences")

		return userPreferenceErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(account)
}

func restAuthContext(ctx *fiber.Ctx) context.Context {
	return authmeta.With(ctx.UserContext(), authmeta.Meta{
		ClientIP:  ctx.IP(),
		UserAgent: ctx.Get("User-Agent"),
		Transport: "rest",
	})
}

func registerDisplayName(body request.Register) string {
	if body.DisplayName != "" {
		return body.DisplayName
	}
	if body.Name != "" {
		return body.Name
	}

	return body.Username
}

func profilePatchRequestToEntity(body request.UserProfilePatch) entity.UserProfilePatch {
	return entity.UserProfilePatch{
		DisplayName:            body.DisplayName,
		Timezone:               body.Timezone,
		CountryCode:            body.CountryCode,
		PersonalizationEnabled: body.PersonalizationEnabled,
	}
}

func onboardingRequestToEntity(body request.UserOnboarding) entity.UserOnboarding {
	return entity.UserOnboarding{
		DisplayName:              body.DisplayName,
		Timezone:                 body.Timezone,
		CountryCode:              body.CountryCode,
		PersonalizationEnabled:   body.PersonalizationEnabled,
		PreferredUILang:          body.PreferredUILang,
		PreferredContentLang:     body.PreferredContentLang,
		FallbackLangs:            body.FallbackLangs,
		ArabicLevel:              body.ArabicLevel,
		ReaderMode:               body.ReaderMode,
		Interests:                body.Interests,
		DailyGoalMinutes:         body.DailyGoalMinutes,
		QuranTranslationSourceID: body.QuranTranslationSourceID,
		QuranRecitationID:        body.QuranRecitationID,
	}
}

func preferencesPatchRequestToEntity(body request.UserPreferencesPatch) entity.UserPreferencesPatch {
	return entity.UserPreferencesPatch{
		PreferredUILang:          body.PreferredUILang,
		PreferredContentLang:     body.PreferredContentLang,
		FallbackLangs:            body.FallbackLangs,
		ArabicLevel:              body.ArabicLevel,
		ReaderMode:               body.ReaderMode,
		Interests:                body.Interests,
		DailyGoalMinutes:         body.DailyGoalMinutes,
		QuranTranslationSourceID: body.QuranTranslationSourceID,
		QuranRecitationID:        body.QuranRecitationID,
	}
}

func userPreferenceErrorResponse(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrUnsupportedLanguage):
		return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
	case errors.Is(err, entity.ErrInvalidUserPreference):
		return errorResponse(ctx, http.StatusBadRequest, "invalid user preference")
	case errors.Is(err, entity.ErrUserNotFound):
		return errorResponse(ctx, http.StatusNotFound, "user not found")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}
