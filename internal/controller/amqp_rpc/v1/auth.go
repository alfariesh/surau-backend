package v1

import (
	"context"

	"github.com/evrone/go-clean-template/internal/controller/amqp_rpc/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/authutil"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/internal/usecase/authmeta"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/goccy/go-json"
	amqp "github.com/rabbitmq/amqp091-go"
)

func amqpAuthContext() context.Context {
	return authmeta.With(context.Background(), authmeta.Meta{
		ClientIP:  "amqp",
		Transport: "amqp",
	})
}

func extractUserID(
	d *amqp.Delivery,
	jwtManager *jwt.Manager,
	users usecase.User,
) (userID string, data json.RawMessage, err error) {
	var req request.AuthenticatedRequest

	err = json.Unmarshal(d.Body, &req)
	if err != nil {
		return "", nil, badRequestError("invalid request format", err)
	}

	userID, err = authutil.Authenticate(amqpAuthContext(), jwtManager, users, req.Token)
	if err != nil {
		return "", nil, unauthenticatedError("invalid or expired token", err)
	}

	return userID, req.Data, nil
}
