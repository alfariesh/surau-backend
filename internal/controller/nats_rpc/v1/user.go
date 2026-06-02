package v1

import (
	"errors"
	"fmt"

	"github.com/evrone/go-clean-template/internal/controller/nats_rpc/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/nats_rpc/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/nats/nats_rpc/server"
	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
)

func (r *V1) register() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		var req request.Register

		err := json.Unmarshal(msg.Data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - register")

			return nil, badRequestError("nats_rpc - V1 - register - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - register - validation", err)
		}

		user, err := r.u.Register(natsAuthContext(), registerDisplayName(req), req.Email, req.Password)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - register")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("nats_rpc - V1 - register - invalid auth input", err)
			}
			if errors.Is(err, entity.ErrEmailDeliveryFailed) {
				return nil, unavailableError("nats_rpc - V1 - register - email delivery failed", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - register - rate limited", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - register: %w", err)
		}

		return user, nil
	}
}

func (r *V1) login() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		var req request.Login

		err := json.Unmarshal(msg.Data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - login")

			return nil, badRequestError("nats_rpc - V1 - login - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - login - validation", err)
		}

		token, err := r.u.Login(natsAuthContext(), req.Email, req.Password)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - login")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("nats_rpc - V1 - login - invalid auth input", err)
			}
			if errors.Is(err, entity.ErrInvalidCredentials) {
				return nil, unauthenticatedError("nats_rpc - V1 - login - invalid credentials", err)
			}
			if errors.Is(err, entity.ErrEmailNotVerified) {
				return nil, failedPreconditionError("nats_rpc - V1 - login - email not verified", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - login - rate limited", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - login: %w", err)
		}

		return response.Token{Token: token}, nil
	}
}

func (r *V1) verifyEmail() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		var req request.VerifyEmail

		err := json.Unmarshal(msg.Data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - verifyEmail")

			return nil, badRequestError("nats_rpc - V1 - verifyEmail - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - verifyEmail - validation", err)
		}

		if err = r.u.VerifyEmail(natsAuthContext(), req.Token); err != nil {
			r.l.Error(err, "nats_rpc - V1 - verifyEmail")
			if errors.Is(err, entity.ErrInvalidVerificationToken) {
				return nil, badRequestError("nats_rpc - V1 - verifyEmail - invalid token", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - verifyEmail: %w", err)
		}

		return response.EmailVerification{EmailVerified: true}, nil
	}
}

func (r *V1) resendVerification() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		var req request.ResendVerification

		err := json.Unmarshal(msg.Data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - resendVerification")

			return nil, badRequestError("nats_rpc - V1 - resendVerification - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - resendVerification - validation", err)
		}

		if err = r.u.ResendEmailVerification(natsAuthContext(), req.Email); err != nil {
			r.l.Error(err, "nats_rpc - V1 - resendVerification")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("nats_rpc - V1 - resendVerification - invalid auth input", err)
			}
			if errors.Is(err, entity.ErrVerificationRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - resendVerification - rate limited", err)
			}
			if errors.Is(err, entity.ErrEmailDeliveryFailed) {
				return nil, unavailableError("nats_rpc - V1 - resendVerification - email delivery failed", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - resendVerification - rate limited", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - resendVerification: %w", err)
		}

		return response.Accepted{Accepted: true}, nil
	}
}

func (r *V1) forgotPassword() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		var req request.ForgotPassword

		err := json.Unmarshal(msg.Data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - forgotPassword")

			return nil, badRequestError("nats_rpc - V1 - forgotPassword - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - forgotPassword - validation", err)
		}

		if err = r.u.ForgotPassword(natsAuthContext(), req.Email); err != nil {
			r.l.Error(err, "nats_rpc - V1 - forgotPassword")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("nats_rpc - V1 - forgotPassword - invalid auth input", err)
			}
			if errors.Is(err, entity.ErrPasswordResetRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - forgotPassword - rate limited", err)
			}
			if errors.Is(err, entity.ErrEmailDeliveryFailed) {
				return nil, unavailableError("nats_rpc - V1 - forgotPassword - email delivery failed", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - forgotPassword - rate limited", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - forgotPassword: %w", err)
		}

		return response.Accepted{Accepted: true}, nil
	}
}

func (r *V1) resetPassword() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		var req request.ResetPassword

		err := json.Unmarshal(msg.Data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - resetPassword")

			return nil, badRequestError("nats_rpc - V1 - resetPassword - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - resetPassword - validation", err)
		}

		if err = r.u.ResetPassword(natsAuthContext(), req.Token, req.Password); err != nil {
			r.l.Error(err, "nats_rpc - V1 - resetPassword")
			if errors.Is(err, entity.ErrInvalidAuthInput) || errors.Is(err, entity.ErrInvalidPasswordResetToken) {
				return nil, badRequestError("nats_rpc - V1 - resetPassword - invalid input", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - resetPassword - rate limited", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - resetPassword: %w", err)
		}

		return response.PasswordReset{PasswordReset: true}, nil
	}
}

func (r *V1) changePassword() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		userID, data, err := extractUserID(msg, r.j, r.u)
		if err != nil {
			return nil, fmt.Errorf("nats_rpc - V1 - changePassword - auth: %w", err)
		}

		var req request.ChangePassword

		err = json.Unmarshal(data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - changePassword")

			return nil, badRequestError("nats_rpc - V1 - changePassword - json.Unmarshal", err)
		}

		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - changePassword - validation", err)
		}

		if err = r.u.ChangePassword(natsAuthContext(), userID, req.CurrentPassword, req.NewPassword); err != nil {
			r.l.Error(err, "nats_rpc - V1 - changePassword")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("nats_rpc - V1 - changePassword - invalid input", err)
			}
			if errors.Is(err, entity.ErrInvalidCredentials) {
				return nil, unauthenticatedError("nats_rpc - V1 - changePassword - invalid credentials", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - changePassword - rate limited", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - changePassword: %w", err)
		}

		return response.PasswordChanged{PasswordChanged: true}, nil
	}
}

func (r *V1) requestEmailChange() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		userID, data, err := extractUserID(msg, r.j, r.u)
		if err != nil {
			return nil, fmt.Errorf("nats_rpc - V1 - requestEmailChange - auth: %w", err)
		}

		var req request.RequestEmailChange
		err = json.Unmarshal(data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - requestEmailChange")

			return nil, badRequestError("nats_rpc - V1 - requestEmailChange - json.Unmarshal", err)
		}
		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - requestEmailChange - validation", err)
		}

		if err = r.u.RequestEmailChange(natsAuthContext(), userID, req.CurrentPassword, req.NewEmail); err != nil {
			r.l.Error(err, "nats_rpc - V1 - requestEmailChange")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("nats_rpc - V1 - requestEmailChange - invalid input", err)
			}
			if errors.Is(err, entity.ErrInvalidCredentials) {
				return nil, unauthenticatedError("nats_rpc - V1 - requestEmailChange - invalid credentials", err)
			}
			if errors.Is(err, entity.ErrUserAlreadyExists) {
				return nil, failedPreconditionError("nats_rpc - V1 - requestEmailChange - email already exists", err)
			}
			if errors.Is(err, entity.ErrEmailChangeRateLimited) || errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - requestEmailChange - rate limited", err)
			}
			if errors.Is(err, entity.ErrEmailDeliveryFailed) {
				return nil, unavailableError("nats_rpc - V1 - requestEmailChange - email delivery failed", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - requestEmailChange: %w", err)
		}

		return response.Accepted{Accepted: true}, nil
	}
}

func (r *V1) verifyEmailChange() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		userID, data, err := extractUserID(msg, r.j, r.u)
		if err != nil {
			return nil, fmt.Errorf("nats_rpc - V1 - verifyEmailChange - auth: %w", err)
		}

		var req request.VerifyEmailChange
		err = json.Unmarshal(data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - verifyEmailChange")

			return nil, badRequestError("nats_rpc - V1 - verifyEmailChange - json.Unmarshal", err)
		}
		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - verifyEmailChange - validation", err)
		}

		if err = r.u.VerifyEmailChange(natsAuthContext(), userID, req.Token); err != nil {
			r.l.Error(err, "nats_rpc - V1 - verifyEmailChange")
			if errors.Is(err, entity.ErrInvalidAuthInput) || errors.Is(err, entity.ErrInvalidEmailChangeToken) {
				return nil, badRequestError("nats_rpc - V1 - verifyEmailChange - invalid input", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - verifyEmailChange - rate limited", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - verifyEmailChange: %w", err)
		}

		return response.EmailChanged{EmailChanged: true}, nil
	}
}

func (r *V1) deleteAccount() server.CallHandler {
	return func(msg *nats.Msg) (any, error) {
		userID, data, err := extractUserID(msg, r.j, r.u)
		if err != nil {
			return nil, fmt.Errorf("nats_rpc - V1 - deleteAccount - auth: %w", err)
		}

		var req request.DeleteAccount
		err = json.Unmarshal(data, &req)
		if err != nil {
			r.l.Error(err, "nats_rpc - V1 - deleteAccount")

			return nil, badRequestError("nats_rpc - V1 - deleteAccount - json.Unmarshal", err)
		}
		if err = r.v.Struct(req); err != nil {
			return nil, badRequestError("nats_rpc - V1 - deleteAccount - validation", err)
		}

		if err = r.u.DeleteAccount(natsAuthContext(), userID, req.CurrentPassword); err != nil {
			r.l.Error(err, "nats_rpc - V1 - deleteAccount")
			if errors.Is(err, entity.ErrInvalidAuthInput) {
				return nil, badRequestError("nats_rpc - V1 - deleteAccount - invalid input", err)
			}
			if errors.Is(err, entity.ErrInvalidCredentials) {
				return nil, unauthenticatedError("nats_rpc - V1 - deleteAccount - invalid credentials", err)
			}
			if errors.Is(err, entity.ErrAuthRateLimited) {
				return nil, rateLimitedError("nats_rpc - V1 - deleteAccount - rate limited", err)
			}

			return nil, fmt.Errorf("nats_rpc - V1 - deleteAccount: %w", err)
		}

		return response.AccountDeleted{AccountDeleted: true}, nil
	}
}

func registerDisplayName(req request.Register) string {
	if req.DisplayName != "" {
		return req.DisplayName
	}
	if req.Name != "" {
		return req.Name
	}

	return req.Username
}
