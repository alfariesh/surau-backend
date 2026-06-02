package authutil

import (
	"context"
	"errors"
	"fmt"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/pkg/jwt"
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
	claims, err := jwtManager.ParseTokenClaims(token)
	if err != nil {
		return entity.User{}, fmt.Errorf("authutil - AuthenticateUser - ParseTokenClaims: %w", err)
	}

	user, err := users.GetUser(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return entity.User{}, entity.ErrInvalidCredentials
		}

		return entity.User{}, fmt.Errorf("authutil - AuthenticateUser - GetUser: %w", err)
	}
	if user.TokenVersion != claims.TokenVersion {
		return entity.User{}, entity.ErrTokenRevoked
	}

	return user, nil
}
