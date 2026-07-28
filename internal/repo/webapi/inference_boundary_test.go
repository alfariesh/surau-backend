package webapi_test

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var forbiddenInferenceFragments = []string{ //nolint:gochecknoglobals // CI boundary contract
	"/chat/completions",
	"/embeddings",
	"from openai import",
	"import openai",
	"OpenAI(",
	"AsyncOpenAI(",
	"langextract[openai]",
	"OpenAICompatibleClient",
	"NewOpenAICompatible",
	"NewDeterministicRolloutLLMClient(",
	"LANGEXTRACT_LLM_API_KEY",
	"DEEPSEEK_API_KEY",
	"RAG_LLM_API_KEY",
	"SUMMARY_LLM_API_KEY",
}

func TestNoAdHocProviderCallSites(t *testing.T) {
	t.Parallel()

	var violations []string

	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)

	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "tmp", "docs", "roadmap":
				return filepath.SkipDir
			}

			return nil
		}

		isRequirements := filepath.Base(path) == "requirements.txt"
		if path == filename ||
			(!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".py") && !isRequirements) ||
			strings.HasSuffix(path, "_test.go") ||
			strings.HasPrefix(filepath.Base(path), "test_") {
			return nil
		}

		content, readErr := os.ReadFile(path) //nolint:gosec // read-only scanner over the resolved repository root
		if readErr != nil {
			return readErr
		}

		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}

		violations = append(violations, inferenceBoundaryViolations(relative, string(content))...)

		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, violations,
		"all active Go/Python LLM calls must pass task-key input through U-0: %v", violations)
}

func TestBoundaryContractCatchesInjectedAdHocProviderCall(t *testing.T) {
	t.Parallel()

	violations := inferenceBoundaryViolations(
		"scripts/injected_generator.py",
		`from openai import OpenAI
client = OpenAI()
client.post("/chat/completions")
`,
	)
	assert.Len(t, violations, 3)

	dependencyViolations := inferenceBoundaryViolations(
		"scripts/injected/requirements.txt",
		"langextract[openai]\n",
	)
	assert.Equal(t, []string{
		`scripts/injected/requirements.txt contains "langextract[openai]"`,
	}, dependencyViolations)
}

func inferenceBoundaryViolations(path, content string) []string {
	if filepath.ToSlash(path) == "internal/repo/webapi/inference_provider.go" ||
		filepath.ToSlash(path) == "internal/repo/webapi/deterministic_rollout_llm.go" {
		return nil
	}
	// App boot selects environment-variable names for registry routes; only
	// the private adapter dereferences them. No secret value crosses here.
	if filepath.ToSlash(path) == "internal/app/app.go" {
		content = strings.ReplaceAll(content, `"RAG_LLM_API_KEY"`, "")
		content = strings.ReplaceAll(content, `"DEEPSEEK_API_KEY"`, "")
	}

	violations := make([]string, 0)

	for _, fragment := range forbiddenInferenceFragments {
		if strings.Contains(content, fragment) {
			violations = append(violations, fmt.Sprintf("%s contains %q", path, fragment))
		}
	}

	return violations
}
