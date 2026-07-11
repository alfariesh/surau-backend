package persistent

import (
	"database/sql"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestProductionGenerationIdentity(t *testing.T) {
	t.Parallel()

	assert.Nil(t, productionGenerationIdentity(
		sql.NullString{},
		sql.NullString{String: "model", Valid: true},
		sql.NullString{String: "prompt", Valid: true},
	))

	identity := productionGenerationIdentity(
		sql.NullString{String: "99010100-0000-4000-8000-000000000001", Valid: true},
		sql.NullString{String: "integration-model", Valid: true},
		sql.NullString{String: "reader-translation-v1", Valid: true},
	)
	assert.Equal(t, &entity.GenerationIdentity{
		RunID:         "99010100-0000-4000-8000-000000000001",
		ModelID:       "integration-model",
		PromptVersion: "reader-translation-v1",
	}, identity)
}
