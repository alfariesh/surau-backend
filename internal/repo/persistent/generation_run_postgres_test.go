package persistent

import (
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateGenerationRun(t *testing.T) {
	t.Parallel()

	provider := "openai"
	valid := entity.GenerationRun{
		ID:            uuid.NewString(),
		TaskName:      "reader-translation",
		ModelID:       "gpt-5-mini-2026-06-01",
		PromptVersion: "reader-translation-v1",
		Provider:      &provider,
		Metadata:      entity.RawJSON(`{"temperature":0}`),
	}

	metadata, err := validateGenerationRun(&valid)
	require.NoError(t, err)
	assert.JSONEq(t, `{"temperature":0}`, metadata)

	withoutMetadata := valid
	withoutMetadata.Metadata = nil
	metadata, err = validateGenerationRun(&withoutMetadata)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, metadata)
	assert.JSONEq(t, `{}`, string(withoutMetadata.Metadata))

	tests := []struct {
		name   string
		mutate func(*entity.GenerationRun)
	}{
		{name: "invalid id", mutate: func(run *entity.GenerationRun) { run.ID = "not-a-uuid" }},
		{name: "blank task", mutate: func(run *entity.GenerationRun) { run.TaskName = " " }},
		{name: "task padding", mutate: func(run *entity.GenerationRun) { run.TaskName = " task" }},
		{name: "blank model", mutate: func(run *entity.GenerationRun) { run.ModelID = "" }},
		{name: "prompt padding", mutate: func(run *entity.GenerationRun) { run.PromptVersion = "v1 " }},
		{name: "blank provider", mutate: func(run *entity.GenerationRun) {
			blank := ""
			run.Provider = &blank
		}},
		{name: "metadata array", mutate: func(run *entity.GenerationRun) { run.Metadata = entity.RawJSON(`[]`) }},
		{name: "metadata null", mutate: func(run *entity.GenerationRun) { run.Metadata = entity.RawJSON(`null`) }},
		{name: "metadata invalid", mutate: func(run *entity.GenerationRun) { run.Metadata = entity.RawJSON(`{`) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			run := valid
			test.mutate(&run)
			_, err := validateGenerationRun(&run)
			assert.ErrorIs(t, err, entity.ErrInvalidGenerationRun)
		})
	}

	_, err = validateGenerationRun(nil)
	assert.ErrorIs(t, err, entity.ErrInvalidGenerationRun)
}

func TestGenerationRunIdentity(t *testing.T) {
	t.Parallel()

	run := entity.GenerationRun{
		ID:            uuid.NewString(),
		ModelID:       "model-1",
		PromptVersion: "prompt-v1",
	}
	assert.Equal(t, entity.GenerationIdentity{
		RunID:         run.ID,
		ModelID:       "model-1",
		PromptVersion: "prompt-v1",
	}, run.Identity())
}
