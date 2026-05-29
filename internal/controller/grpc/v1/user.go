package v1

import (
	"context"
	"errors"

	v1 "github.com/evrone/go-clean-template/docs/proto/v1"
	grpcmw "github.com/evrone/go-clean-template/internal/controller/grpc/middleware"
	"github.com/evrone/go-clean-template/internal/controller/grpc/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase/authmeta"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// Register -.
func (c *AuthController) Register(ctx context.Context, req *v1.RegisterRequest) (*v1.RegisterResponse, error) {
	user, err := c.u.Register(grpcAuthContext(ctx), req.GetUsername(), req.GetEmail(), req.GetPassword())
	if err != nil {
		c.l.Error(err, "grpc - v1 - Register")

		if errors.Is(err, entity.ErrUserAlreadyExists) {
			return nil, status.Error(codes.AlreadyExists, "user already exists")
		}
		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return nil, status.Error(codes.InvalidArgument, "invalid auth input")
		}
		if errors.Is(err, entity.ErrEmailDeliveryFailed) {
			return nil, status.Error(codes.Unavailable, "email delivery failed")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return nil, status.Error(codes.ResourceExhausted, "too many auth attempts")
		}

		return nil, status.Error(codes.Internal, "internal server error")
	}

	return response.NewRegisterResponse(&user), nil
}

// Login -.
func (c *AuthController) Login(ctx context.Context, req *v1.LoginRequest) (*v1.LoginResponse, error) {
	token, err := c.u.Login(grpcAuthContext(ctx), req.GetEmail(), req.GetPassword())
	if err != nil {
		c.l.Error(err, "grpc - v1 - Login")

		if errors.Is(err, entity.ErrInvalidCredentials) {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}
		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return nil, status.Error(codes.InvalidArgument, "invalid auth input")
		}
		if errors.Is(err, entity.ErrEmailNotVerified) {
			return nil, status.Error(codes.FailedPrecondition, "email not verified")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return nil, status.Error(codes.ResourceExhausted, "too many auth attempts")
		}

		return nil, status.Error(codes.Internal, "internal server error")
	}

	return &v1.LoginResponse{Token: token}, nil
}

// VerifyEmail -.
func (c *AuthController) VerifyEmail(ctx context.Context, req *v1.VerifyEmailRequest) (*v1.VerifyEmailResponse, error) {
	if err := c.u.VerifyEmail(grpcAuthContext(ctx), req.GetToken()); err != nil {
		c.l.Error(err, "grpc - v1 - VerifyEmail")

		if errors.Is(err, entity.ErrInvalidVerificationToken) {
			return nil, status.Error(codes.InvalidArgument, "invalid verification token")
		}

		return nil, status.Error(codes.Internal, "internal server error")
	}

	return &v1.VerifyEmailResponse{EmailVerified: true}, nil
}

// ResendEmailVerification -.
func (c *AuthController) ResendEmailVerification(
	ctx context.Context,
	req *v1.ResendEmailVerificationRequest,
) (*v1.ResendEmailVerificationResponse, error) {
	if err := c.u.ResendEmailVerification(grpcAuthContext(ctx), req.GetEmail()); err != nil {
		c.l.Error(err, "grpc - v1 - ResendEmailVerification")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return nil, status.Error(codes.InvalidArgument, "invalid auth input")
		}
		if errors.Is(err, entity.ErrVerificationRateLimited) {
			return nil, status.Error(codes.ResourceExhausted, "verification email recently sent")
		}
		if errors.Is(err, entity.ErrEmailDeliveryFailed) {
			return nil, status.Error(codes.Unavailable, "email delivery failed")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return nil, status.Error(codes.ResourceExhausted, "too many auth attempts")
		}

		return nil, status.Error(codes.Internal, "internal server error")
	}

	return &v1.ResendEmailVerificationResponse{Accepted: true}, nil
}

// ForgotPassword -.
func (c *AuthController) ForgotPassword(ctx context.Context, req *v1.ForgotPasswordRequest) (*v1.ForgotPasswordResponse, error) {
	if err := c.u.ForgotPassword(grpcAuthContext(ctx), req.GetEmail()); err != nil {
		c.l.Error(err, "grpc - v1 - ForgotPassword")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return nil, status.Error(codes.InvalidArgument, "invalid auth input")
		}
		if errors.Is(err, entity.ErrPasswordResetRateLimited) {
			return nil, status.Error(codes.ResourceExhausted, "password reset email recently sent")
		}
		if errors.Is(err, entity.ErrEmailDeliveryFailed) {
			return nil, status.Error(codes.Unavailable, "email delivery failed")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return nil, status.Error(codes.ResourceExhausted, "too many auth attempts")
		}

		return nil, status.Error(codes.Internal, "internal server error")
	}

	return &v1.ForgotPasswordResponse{Accepted: true}, nil
}

// ResetPassword -.
func (c *AuthController) ResetPassword(ctx context.Context, req *v1.ResetPasswordRequest) (*v1.ResetPasswordResponse, error) {
	if err := c.u.ResetPassword(grpcAuthContext(ctx), req.GetToken(), req.GetPassword()); err != nil {
		c.l.Error(err, "grpc - v1 - ResetPassword")

		if errors.Is(err, entity.ErrInvalidAuthInput) || errors.Is(err, entity.ErrInvalidPasswordResetToken) {
			return nil, status.Error(codes.InvalidArgument, "invalid password reset input")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return nil, status.Error(codes.ResourceExhausted, "too many auth attempts")
		}

		return nil, status.Error(codes.Internal, "internal server error")
	}

	return &v1.ResetPasswordResponse{PasswordReset: true}, nil
}

// ChangePassword -.
func (c *AuthController) ChangePassword(
	ctx context.Context,
	req *v1.ChangePasswordRequest,
) (*v1.ChangePasswordResponse, error) {
	userID, ok := grpcmw.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "unauthorized")
	}

	if err := c.u.ChangePassword(grpcAuthContext(ctx), userID, req.GetCurrentPassword(), req.GetNewPassword()); err != nil {
		c.l.Error(err, "grpc - v1 - ChangePassword")

		if errors.Is(err, entity.ErrInvalidAuthInput) {
			return nil, status.Error(codes.InvalidArgument, "invalid auth input")
		}
		if errors.Is(err, entity.ErrInvalidCredentials) {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}
		if errors.Is(err, entity.ErrAuthRateLimited) {
			return nil, status.Error(codes.ResourceExhausted, "too many auth attempts")
		}

		return nil, status.Error(codes.Internal, "internal server error")
	}

	return &v1.ChangePasswordResponse{PasswordChanged: true}, nil
}

// GetProfile -.
func (c *AuthController) GetProfile(ctx context.Context, _ *v1.GetProfileRequest) (*v1.GetProfileResponse, error) {
	userID, ok := grpcmw.UserIDFromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "unauthorized")
	}

	user, err := c.u.GetUser(ctx, userID)
	if err != nil {
		c.l.Error(err, "grpc - v1 - GetProfile")

		if errors.Is(err, entity.ErrUserNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}

		return nil, status.Error(codes.Internal, "internal server error")
	}

	return response.NewGetProfileResponse(&user), nil
}

func grpcAuthContext(ctx context.Context) context.Context {
	meta := authmeta.Meta{Transport: "grpc"}
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get("x-forwarded-for"); len(values) > 0 {
			meta.ClientIP = values[0]
		}
		if values := md.Get("user-agent"); len(values) > 0 {
			meta.UserAgent = values[0]
		}
	}
	if meta.ClientIP == "" {
		if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
			meta.ClientIP = p.Addr.String()
		}
	}

	return authmeta.With(ctx, meta)
}
