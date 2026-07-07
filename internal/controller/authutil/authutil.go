package authutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/jwt"
)

// Authenticate validates a JWT and checks it against the user's current token version.
func Authenticate(ctx context.Context, jwtManager *jwt.Manager, users usecase.User, token string) (string, error) {
	user, err := AuthenticateUser(ctx, jwtManager, users, token)
	if err != nil {
		return "", err
	}

	return user.ID, nil
}

// AuthenticateUser validates a JWT and returns the current user record.
func AuthenticateUser(ctx context.Context, jwtManager *jwt.Manager, users usecase.User, token string) (entity.User, error) {
	user, _, err := AuthenticateUserSession(ctx, jwtManager, users, token)

	return user, err
}

// AuthenticateUserSession validates a JWT and returns the current user record
// together with the session/family id from the token's `sid` claim. The id is
// empty for legacy access tokens issued before session binding.
func AuthenticateUserSession(
	ctx context.Context,
	jwtManager *jwt.Manager,
	users usecase.User,
	token string,
) (entity.User, string, error) {
	claims, err := jwtManager.ParseTokenClaims(token)
	if err != nil {
		return entity.User{}, "", fmt.Errorf("authutil - AuthenticateUserSession - ParseTokenClaims: %w", err)
	}

	user, err := users.GetUser(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return entity.User{}, "", entity.ErrInvalidCredentials
		}

		return entity.User{}, "", fmt.Errorf("authutil - AuthenticateUserSession - GetUser: %w", err)
	}
	if user.TokenVersion != claims.TokenVersion {
		return entity.User{}, "", entity.ErrTokenRevoked
	}

	return user, claims.SessionID, nil
}
