package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/apierror"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/gofiber/fiber/v2"
)

const _bearerParts = 2

// errUserLookup marks a token that verified cryptographically but whose owner
// could not be loaded because the user store failed. That is an
// infrastructure fault, not a credential problem: answering 401 would make
// clients discard a valid session during a transient database failure.
var errUserLookup = errors.New("user lookup failed")

type errorResponse struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// Auth returns a JWT authentication middleware for Fiber. Credential problems
// (malformed, expired, revoked, or orphaned tokens) answer 401; a failing user
// store answers 500 so clients keep the session and retry.
func Auth(jwtManager *jwt.Manager, users usecase.User) func(*fiber.Ctx) error {
	return func(ctx *fiber.Ctx) error {
		header := ctx.Get("Authorization")
		if header == "" {
			return middlewareError(ctx, http.StatusUnauthorized, "missing authorization header")
		}

		parts := strings.SplitN(header, " ", _bearerParts)
		if len(parts) != _bearerParts || !strings.EqualFold(parts[0], "Bearer") {
			return middlewareError(ctx, http.StatusUnauthorized, "invalid authorization header format")
		}

		token := strings.TrimSpace(parts[1])
		if token == "" {
			return middlewareError(ctx, http.StatusUnauthorized, "invalid authorization header format")
		}

		user, sessionID, err := authenticateSession(ctx.UserContext(), jwtManager, users, token)
		if errors.Is(err, errUserLookup) {
			if l := RequestLogger(ctx, nil); l != nil {
				l.Error(err, "middleware - Auth")
			}

			return middlewareError(ctx, http.StatusInternalServerError, "internal server error")
		}

		if err != nil {
			return middlewareError(ctx, http.StatusUnauthorized, "invalid or expired token")
		}

		ctx.Locals("user", user)
		ctx.Locals("userID", user.ID)
		ctx.Locals("sessionID", sessionID)

		return ctx.Next()
	}
}

// authenticateSession validates a JWT against the user's current token version
// and returns the user together with the session/family id from the token's
// `sid` claim. The id is empty for legacy access tokens issued before session
// binding.
func authenticateSession(
	ctx context.Context,
	jwtManager *jwt.Manager,
	users usecase.User,
	token string,
) (entity.User, string, error) {
	claims, err := jwtManager.ParseTokenClaims(token)
	if err != nil {
		return entity.User{}, "", fmt.Errorf("middleware - authenticateSession - ParseTokenClaims: %w", err)
	}

	user, err := users.GetUser(ctx, claims.UserID)
	if errors.Is(err, entity.ErrUserNotFound) {
		return entity.User{}, "", entity.ErrInvalidCredentials
	}

	if err != nil {
		return entity.User{}, "", fmt.Errorf("%w: %w", errUserLookup, err)
	}

	if user.TokenVersion != claims.TokenVersion {
		return entity.User{}, "", entity.ErrTokenRevoked
	}

	return user, claims.SessionID, nil
}

func middlewareError(ctx *fiber.Ctx, status int, msg string) error {
	return ctx.Status(status).JSON(errorResponse{
		Error:     msg,
		Code:      apierror.Code(msg),
		Message:   msg,
		RequestID: middlewareRequestID(ctx),
	})
}

func middlewareRequestID(ctx *fiber.Ctx) string {
	requestID, ok := ctx.Locals("requestID").(string)
	if !ok {
		return ""
	}

	return requestID
}
