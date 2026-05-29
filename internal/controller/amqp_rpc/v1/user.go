package v1

import (
	"errors"
	"fmt"

	"github.com/evrone/go-clean-template/internal/controller/amqp_rpc/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/amqp_rpc/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/rabbitmq/rmq_rpc/server"
	"github.com/goccy/go-json"
	amqp "github.com/rabbitmq/amqp091-go"
)

func (r *V1) register() server.CallHandler {
	return func(d *amqp.Delivery) (any, error) {
		var req request.Register

		err := json.Unmarshal(d.Body, &req)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - register")

			return nil, badRequestError("amqp_rpc - V1 - register - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("amqp_rpc - V1 - register - validation", err)
		}

		user, err := r.u.Register(amqpAuthContext(), req.Username, req.Email, req.Password)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - register")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("amqp_rpc - V1 - register - invalid auth input", err)
			}
			if errors.Is(err, entity.ErrEmailDeliveryFailed) {
				return nil, unavailableError("amqp_rpc - V1 - register - email delivery failed", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("amqp_rpc - V1 - register - rate limited", err)
			}

			return nil, fmt.Errorf("amqp_rpc - V1 - register: %w", err)
		}

		return user, nil
	}
}

func (r *V1) login() server.CallHandler {
	return func(d *amqp.Delivery) (any, error) {
		var req request.Login

		err := json.Unmarshal(d.Body, &req)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - login")

			return nil, badRequestError("amqp_rpc - V1 - login - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("amqp_rpc - V1 - login - validation", err)
		}

		token, err := r.u.Login(amqpAuthContext(), req.Email, req.Password)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - login")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("amqp_rpc - V1 - login - invalid auth input", err)
			}
			if errors.Is(err, entity.ErrInvalidCredentials) {
				return nil, unauthenticatedError("amqp_rpc - V1 - login - invalid credentials", err)
			}
			if errors.Is(err, entity.ErrEmailNotVerified) {
				return nil, failedPreconditionError("amqp_rpc - V1 - login - email not verified", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("amqp_rpc - V1 - login - rate limited", err)
			}

			return nil, fmt.Errorf("amqp_rpc - V1 - login: %w", err)
		}

		return response.Token{Token: token}, nil
	}
}

func (r *V1) verifyEmail() server.CallHandler {
	return func(d *amqp.Delivery) (any, error) {
		var req request.VerifyEmail

		err := json.Unmarshal(d.Body, &req)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - verifyEmail")

			return nil, badRequestError("amqp_rpc - V1 - verifyEmail - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("amqp_rpc - V1 - verifyEmail - validation", err)
		}

		if err = r.u.VerifyEmail(amqpAuthContext(), req.Token); err != nil {
			r.l.Error(err, "amqp_rpc - V1 - verifyEmail")
			if errors.Is(err, entity.ErrInvalidVerificationToken) {
				return nil, badRequestError("amqp_rpc - V1 - verifyEmail - invalid token", err)
			}

			return nil, fmt.Errorf("amqp_rpc - V1 - verifyEmail: %w", err)
		}

		return response.EmailVerification{EmailVerified: true}, nil
	}
}

func (r *V1) resendVerification() server.CallHandler {
	return func(d *amqp.Delivery) (any, error) {
		var req request.ResendVerification

		err := json.Unmarshal(d.Body, &req)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - resendVerification")

			return nil, badRequestError("amqp_rpc - V1 - resendVerification - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("amqp_rpc - V1 - resendVerification - validation", err)
		}

		if err = r.u.ResendEmailVerification(amqpAuthContext(), req.Email); err != nil {
			r.l.Error(err, "amqp_rpc - V1 - resendVerification")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("amqp_rpc - V1 - resendVerification - invalid auth input", err)
			}
			if errors.Is(err, entity.ErrVerificationRateLimited) {
				return nil, rateLimitedError("amqp_rpc - V1 - resendVerification - rate limited", err)
			}
			if errors.Is(err, entity.ErrEmailDeliveryFailed) {
				return nil, unavailableError("amqp_rpc - V1 - resendVerification - email delivery failed", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("amqp_rpc - V1 - resendVerification - rate limited", err)
			}

			return nil, fmt.Errorf("amqp_rpc - V1 - resendVerification: %w", err)
		}

		return response.Accepted{Accepted: true}, nil
	}
}

func (r *V1) forgotPassword() server.CallHandler {
	return func(d *amqp.Delivery) (any, error) {
		var req request.ForgotPassword

		err := json.Unmarshal(d.Body, &req)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - forgotPassword")

			return nil, badRequestError("amqp_rpc - V1 - forgotPassword - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("amqp_rpc - V1 - forgotPassword - validation", err)
		}

		if err = r.u.ForgotPassword(amqpAuthContext(), req.Email); err != nil {
			r.l.Error(err, "amqp_rpc - V1 - forgotPassword")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("amqp_rpc - V1 - forgotPassword - invalid auth input", err)
			}
			if errors.Is(err, entity.ErrPasswordResetRateLimited) {
				return nil, rateLimitedError("amqp_rpc - V1 - forgotPassword - rate limited", err)
			}
			if errors.Is(err, entity.ErrEmailDeliveryFailed) {
				return nil, unavailableError("amqp_rpc - V1 - forgotPassword - email delivery failed", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("amqp_rpc - V1 - forgotPassword - rate limited", err)
			}

			return nil, fmt.Errorf("amqp_rpc - V1 - forgotPassword: %w", err)
		}

		return response.Accepted{Accepted: true}, nil
	}
}

func (r *V1) resetPassword() server.CallHandler {
	return func(d *amqp.Delivery) (any, error) {
		var req request.ResetPassword

		err := json.Unmarshal(d.Body, &req)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - resetPassword")

			return nil, badRequestError("amqp_rpc - V1 - resetPassword - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("amqp_rpc - V1 - resetPassword - validation", err)
		}

		if err = r.u.ResetPassword(amqpAuthContext(), req.Token, req.Password); err != nil {
			r.l.Error(err, "amqp_rpc - V1 - resetPassword")
			if errors.Is(err, entity.ErrInvalidAuthInput) || errors.Is(err, entity.ErrInvalidPasswordResetToken) {
				return nil, badRequestError("amqp_rpc - V1 - resetPassword - invalid input", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("amqp_rpc - V1 - resetPassword - rate limited", err)
			}

			return nil, fmt.Errorf("amqp_rpc - V1 - resetPassword: %w", err)
		}

		return response.PasswordReset{PasswordReset: true}, nil
	}
}

func (r *V1) changePassword() server.CallHandler {
	return func(d *amqp.Delivery) (any, error) {
		userID, data, err := extractUserID(d, r.j, r.u)
		if err != nil {
			return nil, fmt.Errorf("amqp_rpc - V1 - changePassword - auth: %w", err)
		}

		var req request.ChangePassword

		err = json.Unmarshal(data, &req)
		if err != nil {
			r.l.Error(err, "amqp_rpc - V1 - changePassword")

			return nil, badRequestError("amqp_rpc - V1 - changePassword - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("amqp_rpc - V1 - changePassword - validation", err)
		}

		if err = r.u.ChangePassword(amqpAuthContext(), userID, req.CurrentPassword, req.NewPassword); err != nil {
			r.l.Error(err, "amqp_rpc - V1 - changePassword")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("amqp_rpc - V1 - changePassword - invalid input", err)
			}
			if errors.Is(err, entity.ErrInvalidCredentials) {
				return nil, unauthenticatedError("amqp_rpc - V1 - changePassword - invalid credentials", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("amqp_rpc - V1 - changePassword - rate limited", err)
			}

			return nil, fmt.Errorf("amqp_rpc - V1 - changePassword: %w", err)
		}

		return response.PasswordChanged{PasswordChanged: true}, nil
	}
}
