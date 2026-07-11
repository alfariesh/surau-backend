package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	domainrepo "github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const generationRunSelect = `
id, task_name, model_id, prompt_version, provider, metadata, created_at`

var _ domainrepo.GenerationRunRepo = (*GenerationRunRepo)(nil)

// GenerationRunRepo persists immutable generation descriptors.
type GenerationRunRepo struct {
	*postgres.Postgres
}

// NewGenerationRunRepo constructs the B-6 registry adapter.
func NewGenerationRunRepo(pg *postgres.Postgres) *GenerationRunRepo {
	return &GenerationRunRepo{Postgres: pg}
}

// RegisterOrVerify inserts a descriptor once. A retry with the same UUID is
// accepted only if task/model/prompt/provider/metadata match byte-independent
// JSONB semantics; conflicting attribution is never overwritten.
func (r *GenerationRunRepo) RegisterOrVerify(
	ctx context.Context,
	run *entity.GenerationRun,
) (entity.GenerationRun, error) {
	metadata, err := validateGenerationRun(run)
	if err != nil {
		return entity.GenerationRun{}, err
	}

	var createdAt *time.Time
	if !run.CreatedAt.IsZero() {
		createdAt = &run.CreatedAt
	}

	_, err = r.Pool.Exec(
		ctx, `
INSERT INTO generation_runs (
    id, task_name, model_id, prompt_version, provider, metadata, created_at
)
VALUES ($1, $2, $3, $4, $5, $6::jsonb, COALESCE($7, now()))
ON CONFLICT (id) DO NOTHING`,
		run.ID,
		run.TaskName,
		run.ModelID,
		run.PromptVersion,
		run.Provider,
		metadata,
		createdAt,
	)
	if err != nil {
		return entity.GenerationRun{}, fmt.Errorf("GenerationRunRepo.RegisterOrVerify insert: %w", err)
	}

	row := r.Pool.QueryRow(
		ctx, `
SELECT `+generationRunSelect+`,
       task_name = $2
       AND model_id = $3
       AND prompt_version = $4
       AND provider IS NOT DISTINCT FROM $5::text
       AND metadata = $6::jsonb AS descriptor_matches
FROM generation_runs
WHERE id = $1`,
		run.ID,
		run.TaskName,
		run.ModelID,
		run.PromptVersion,
		run.Provider,
		metadata,
	)

	stored, matches, err := scanGenerationRunMatch(row)
	if err != nil {
		return entity.GenerationRun{}, fmt.Errorf("GenerationRunRepo.RegisterOrVerify read: %w", err)
	}

	if !matches {
		return entity.GenerationRun{}, entity.ErrGenerationRunConflict
	}

	return stored, nil
}

// Get returns one immutable descriptor by UUID.
func (r *GenerationRunRepo) Get(ctx context.Context, id string) (entity.GenerationRun, error) {
	if _, err := uuid.Parse(id); err != nil {
		return entity.GenerationRun{}, entity.ErrInvalidGenerationRun
	}

	row := r.Pool.QueryRow(ctx, "SELECT "+generationRunSelect+" FROM generation_runs WHERE id = $1", id)

	run, err := scanGenerationRun(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.GenerationRun{}, entity.ErrGenerationRunNotFound
	}

	if err != nil {
		return entity.GenerationRun{}, fmt.Errorf("GenerationRunRepo.Get: %w", err)
	}

	return run, nil
}

func validateGenerationRun(run *entity.GenerationRun) (string, error) {
	if run == nil {
		return "", entity.ErrInvalidGenerationRun
	}

	if !validGenerationRunID(run.ID) {
		return "", entity.ErrInvalidGenerationRun
	}

	if !validGenerationRunLabel(run.TaskName) ||
		!validGenerationRunLabel(run.ModelID) ||
		!validGenerationRunLabel(run.PromptVersion) {
		return "", entity.ErrInvalidGenerationRun
	}

	if run.Provider != nil && !validGenerationRunLabel(*run.Provider) {
		return "", entity.ErrInvalidGenerationRun
	}

	return normalizeGenerationRunMetadata(run)
}

func validGenerationRunLabel(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func validGenerationRunID(value string) bool {
	_, err := uuid.Parse(value)

	return err == nil
}

func normalizeGenerationRunMetadata(run *entity.GenerationRun) (string, error) {
	metadata := run.Metadata
	if len(metadata) == 0 {
		metadata = entity.RawJSON(`{}`)
	}

	var object map[string]any
	if err := json.Unmarshal(metadata, &object); err != nil || object == nil {
		return "", entity.ErrInvalidGenerationRun
	}

	run.Metadata = metadata

	return string(metadata), nil
}

type generationRunScanner interface {
	Scan(dest ...any) error
}

func scanGenerationRun(row generationRunScanner) (entity.GenerationRun, error) {
	var (
		run      entity.GenerationRun
		metadata []byte
	)

	if err := row.Scan(
		&run.ID,
		&run.TaskName,
		&run.ModelID,
		&run.PromptVersion,
		&run.Provider,
		&metadata,
		&run.CreatedAt,
	); err != nil {
		return entity.GenerationRun{}, err
	}

	run.Metadata = entity.RawJSON(metadata)

	return run, nil
}

func scanGenerationRunMatch(row generationRunScanner) (entity.GenerationRun, bool, error) {
	var (
		run      entity.GenerationRun
		metadata []byte
		matches  bool
	)

	if err := row.Scan(
		&run.ID,
		&run.TaskName,
		&run.ModelID,
		&run.PromptVersion,
		&run.Provider,
		&metadata,
		&run.CreatedAt,
		&matches,
	); err != nil {
		return entity.GenerationRun{}, false, err
	}

	run.Metadata = entity.RawJSON(metadata)

	return run, matches, nil
}
