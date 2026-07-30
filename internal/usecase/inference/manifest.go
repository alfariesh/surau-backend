package inference

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/entity"
)

//go:embed assets/prompts.json
var promptManifestJSON []byte

func promptManifests() ([]entity.InferencePromptManifest, error) {
	var manifests []entity.InferencePromptManifest
	if err := json.Unmarshal(promptManifestJSON, &manifests); err != nil {
		return nil, fmt.Errorf("inference embedded prompt manifest: %w", err)
	}

	if len(manifests) == 0 {
		return nil, entity.ErrInferenceRegistryConflict
	}

	return manifests, nil
}
