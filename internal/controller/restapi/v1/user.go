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

	user, err := r.u.Register(restAuthContext(ctx), body.Username, body.Email, body.Password)
	if err != nil {
		r.l.Error(err, "restapi - v1 - register")

		if errors.Is(err, entity.ErrUserAlreadyExists) {
			return errorResponse(ctx, http.StatusConflict, "user already exists")
		}
		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return errorResponse(ctx, http.StatusTooManyRequests, "too many auth attempts")
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

	token, err := r.u.Login(restAuthContext(ctx), body.Email, body.Password)
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
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return errorResponse(ctx, http.StatusTooManyRequests, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.Token{Token: token})
}

// @Summary     Verify email
// @Description Verify a user's email address using a one-time token
// @ID          verify-email
// @Tags        auth
// @Accept      json
// @Produce     json
// @Param       request body     request.VerifyEmail true "Email verification token"
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

	if err := r.u.VerifyEmail(restAuthContext(ctx), body.Token); err != nil {
		r.l.Error(err, "restapi - v1 - verifyEmail")

		if errors.Is(err, entity.ErrInvalidVerificationToken) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid verification token")
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
			return errorResponse(ctx, http.StatusTooManyRequests, "too many auth attempts")
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
			return errorResponse(ctx, http.StatusTooManyRequests, "too many auth attempts")
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
			return errorResponse(ctx, http.StatusTooManyRequests, "too many auth attempts")
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

	if err := r.u.ChangePassword(restAuthContext(ctx), userID, body.CurrentPassword, body.NewPassword); err != nil {
		r.l.Error(err, "restapi - v1 - changePassword")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
		if errors.Is(err, entity.ErrInvalidCredentials) {
			return errorResponse(ctx, http.StatusUnauthorized, "invalid credentials")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return errorResponse(ctx, http.StatusTooManyRequests, "too many auth attempts")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.PasswordChanged{PasswordChanged: true})
}

// @Summary     Get profile
// @Description Get current user profile
// @ID          profile
// @Tags        user
// @Produce     json
// @Success     200 {object} entity.User
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

	user, err := r.u.GetUser(ctx.UserContext(), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - profile")

		if errors.Is(err, entity.ErrUserNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "user not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(user)
}

func restAuthContext(ctx *fiber.Ctx) context.Context {
	return authmeta.With(ctx.UserContext(), authmeta.Meta{
		ClientIP:  ctx.IP(),
		UserAgent: ctx.Get("User-Agent"),
		Transport: "rest",
	})
}
