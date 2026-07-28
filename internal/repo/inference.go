package repo

import (
	"context"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// InferenceProvider is implemented only by approved provider adapters.
type InferenceProvider interface {
	Chat(
		ctx context.Context,
		route entity.InferenceRoute,
		request entity.InferenceProviderRequest,
	) (entity.InferenceProviderResponse, error)
	Embed(
		ctx context.Context,
		route entity.InferenceRoute,
		request entity.InferenceProviderRequest,
	) (entity.InferenceProviderResponse, error)
}

// InferenceRepo owns the registry, attribution ledger, atomic budget and cache.
type InferenceRepo interface {
	SyncManifest(ctx context.Context, manifests []entity.InferencePromptManifest) error
	SyncRoutes(ctx context.Context, routes []entity.InferenceRoute) error
	ResolveRoutes(ctx context.Context, taskKey, sessionID string) ([]entity.InferenceRoute, error)
	CreateCall(ctx context.Context, call entity.InferenceCall) error
	CreateAttempt(ctx context.Context, attempt entity.InferenceAttempt) error
	FinishAttempt(ctx context.Context, attempt entity.InferenceAttempt) error
	FinishCall(ctx context.Context, call entity.InferenceCall) error
	ReserveBudget(ctx context.Context, callID string, maximumNanoUSD int64) (entity.InferenceBudgetStatus, error)
	SettleBudget(ctx context.Context, callID string) error
	GetCache(ctx context.Context, cacheKey string) (entity.InferenceCacheEntry, bool, error)
	PutCache(ctx context.Context, entry entity.InferenceCacheEntry) error
	CreateSession(ctx context.Context, taskKey string) (entity.InferenceSession, error)
	PinSession(ctx context.Context, sessionID, providerKey, modelKey string) error
	FailSession(ctx context.Context, sessionID string) error
	Budget(ctx context.Context, now time.Time) (entity.InferenceBudgetStatus, error)
	UpdateBudget(
		ctx context.Context,
		actorID string,
		expectedRevision int64,
		patch entity.InferenceBudgetPatch,
	) (entity.InferenceBudgetStatus, error)
	Usage(
		ctx context.Context,
		from, to time.Time,
		groupBy string,
	) ([]entity.InferenceUsageRow, error)
	Registry(ctx context.Context) ([]entity.InferenceRoute, error)
}
