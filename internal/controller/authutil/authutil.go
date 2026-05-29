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
	claims, err := jwtManager.ParseTokenClaims(token)
	if err != nil {
		return "", fmt.Errorf("authutil - Authenticate - ParseTokenClaims: %w", err)
	}

	user, err := users.GetUser(ctx, claims.UserID)
	if err != nil {
		if errors.Is(err, entity.ErrUserNotFound) {
			return "", entity.ErrInvalidCredentials
		}

		return "", fmt.Errorf("authutil - Authenticate - GetUser: %w", err)
	}
	if user.TokenVersion != claims.TokenVersion {
		return "", entity.ErrTokenRevoked
	}

	return user.ID, nil
}
