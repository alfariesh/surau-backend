package usecase

import (
	"context"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// ServiceIdentity is the A-2 registry and request-authentication boundary.
type ServiceIdentity interface {
	CreateServicePrincipal(
		ctx context.Context,
		actorID, name, description string,
		scopes []string,
	) (entity.ServicePrincipal, error)
	ListServicePrincipals(
		ctx context.Context,
		limit, offset int,
	) ([]entity.ServicePrincipal, int, error)
	GetServicePrincipal(ctx context.Context, id string) (entity.ServicePrincipal, error)
	UpdateServicePrincipal(
		ctx context.Context,
		actorID, id, description string,
		scopes []string,
		expected *time.Time,
		force bool,
	) (entity.ServicePrincipal, error)
	IssueServiceToken(
		ctx context.Context,
		actorID, principalID string,
		expiresAt *time.Time,
		expected *time.Time,
		force bool,
	) (entity.ServiceTokenIssueResult, error)
	RevokeServiceToken(
		ctx context.Context,
		actorID, principalID, tokenID string,
		expected *time.Time,
		force bool,
	) (entity.ServicePrincipal, error)
	RevokeServicePrincipal(
		ctx context.Context,
		actorID, principalID string,
		expected *time.Time,
		force bool,
	) (entity.ServicePrincipal, error)
	AuthenticateServiceToken(
		ctx context.Context,
		rawToken, requiredScope string,
	) (entity.ServiceAuthentication, error)
	CreateServiceRequestAudit(ctx context.Context, audit entity.ServiceRequestAudit) (string, error)
	FinishServiceRequestAudit(ctx context.Context, id string, status int) error
	CleanupServiceRequestAudits(ctx context.Context) (entity.ServiceAuditCleanupResult, error)
}
