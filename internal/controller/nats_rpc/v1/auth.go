package v1

import (
	"context"

	"github.com/evrone/go-clean-template/internal/controller/authutil"
	"github.com/evrone/go-clean-template/internal/controller/nats_rpc/v1/request"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/internal/usecase/authmeta"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/goccy/go-json"
	"github.com/nats-io/nats.go"
)

func natsAuthContext() context.Context {
	return authmeta.With(context.Background(), authmeta.Meta{
		ClientIP:  "nats",
		Transport: "nats",
	})
}

func extractUserID(
	msg *nats.Msg,
	jwtManager *jwt.Manager,
	users usecase.User,
) (userID string, data json.RawMessage, err error) {
	var req request.AuthenticatedRequest

	err = json.Unmarshal(msg.Data, &req)
	if err != nil {
		return "", nil, badRequestError("invalid request format", err)
	}

	userID, err = authutil.Authenticate(natsAuthContext(), jwtManager, users, req.Token)
	if err != nil {
		return "", nil, unauthenticatedError("invalid or expired token", err)
	}

	return userID, req.Data, nil
}
