package repo

import (
	"context"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// ServicePrincipalFilter controls the bounded admin registry list.
type ServicePrincipalFilter struct {
	Limit  int
	Offset int
}

// ServiceIdentityRepo owns the A-2 registry, lifecycle events, authentication
// lookup, and durable request audit. Mutations are atomic with their events.
type ServiceIdentityRepo interface {
	CreateServicePrincipal(
		ctx context.Context,
		principal entity.ServicePrincipal,
		actorID string,
	) (entity.ServicePrincipal, error)
	ListServicePrincipals(
		ctx context.Context,
		filter ServicePrincipalFilter,
	) ([]entity.ServicePrincipal, int, error)
	GetServicePrincipal(ctx context.Context, id string) (entity.ServicePrincipal, error)
	UpdateServicePrincipal(
		ctx context.Context,
		id, description string,
		scopes []string,
		actorID string,
		expected *time.Time,
		force bool,
	) (entity.ServicePrincipal, error)
	IssueServiceToken(
		ctx context.Context,
		token entity.ServiceTokenRecord,
		actorID string,
		expected *time.Time,
		force bool,
	) (entity.ServicePrincipal, error)
	RevokeServiceToken(
		ctx context.Context,
		principalID, tokenID, actorID string,
		expected *time.Time,
		force bool,
	) (entity.ServicePrincipal, error)
	RevokeServicePrincipal(
		ctx context.Context,
		principalID, actorID string,
		expected *time.Time,
		force bool,
	) (entity.ServicePrincipal, error)
	GetServiceCredentialByID(ctx context.Context, tokenID string) (entity.ServiceCredential, error)
	GetServiceCredentialByHash(ctx context.Context, hash []byte) (entity.ServiceCredential, error)
	BootstrapLegacyServiceToken(
		ctx context.Context,
		principal entity.ServicePrincipal,
		token entity.ServiceTokenRecord,
	) (entity.ServicePrincipal, error)
	CreateServiceRequestAudit(ctx context.Context, audit entity.ServiceRequestAudit) error
	FinishServiceRequestAudit(
		ctx context.Context,
		id, outcome string,
		status int,
		finishedAt time.Time,
	) error
	CleanupServiceRequestAudits(ctx context.Context, before time.Time) (int64, error)
}
