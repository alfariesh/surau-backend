package repo

import (
	"context"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// GenerationRunRepo owns the immutable B-6 descriptor registry. Registering
// the same UUID is idempotent only when the full descriptor is identical.
type GenerationRunRepo interface {
	RegisterOrVerify(ctx context.Context, run *entity.GenerationRun) (entity.GenerationRun, error)
	Get(ctx context.Context, id string) (entity.GenerationRun, error)
}
