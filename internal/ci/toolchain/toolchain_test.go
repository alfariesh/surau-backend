package toolchain_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

const repoRoot = "../../.."

var (
	golangImage     = regexp.MustCompile(`(?m)^FROM\s+golang:(\d[^\s@-]*)`)
	pinnedGoVersion = regexp.MustCompile(`(?m)^\s*go-version:\s`)
	setupGo         = regexp.MustCompile(`actions/setup-go@v(\d+)`)
	goVersionFile   = regexp.MustCompile(`(?m)^\s*go-version-file:\s*go\.mod\s*$`)
)

// goModToolchain returns the version in go.mod's toolchain line, e.g. 1.26.8.
func goModToolchain(t *testing.T) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	require.NoError(t, err)

	file, err := modfile.Parse("go.mod", data, nil)
	require.NoError(t, err)
	require.NotNil(t, file.Toolchain, "go.mod must keep its toolchain line: it is the Go version source")

	return strings.TrimPrefix(file.Toolchain.Name, "go")
}

// TestDockerfilesBuildWithTheGoModToolchain matters because the golang image
// sets GOTOOLCHAIN=local: a base image that differs from go.mod compiles with
// its own Go, which is how production ran 1.26.4 while CI tested 1.26.5.
func TestDockerfilesBuildWithTheGoModToolchain(t *testing.T) {
	t.Parallel()

	want := goModToolchain(t)

	for _, dockerfile := range []string{"Dockerfile", "integration-test/Dockerfile"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, dockerfile))
		require.NoError(t, err)

		images := golangImage.FindAllStringSubmatch(string(data), -1)
		require.NotEmpty(t, images, "%s has no golang base image", dockerfile)

		for _, image := range images {
			assert.Equal(t, want, image[1], "%s FROM golang:%s must match go.mod toolchain go%s",
				dockerfile, image[1], want)
		}
	}
}

// TestWorkflowsReadTheGoVersionFromGoMod requires setup-go v6+, which reads
// go.mod's toolchain line; v5 reads only the "go 1.26" line.
func TestWorkflowsReadTheGoVersionFromGoMod(t *testing.T) {
	t.Parallel()

	workflows, err := filepath.Glob(filepath.Join(repoRoot, ".github", "workflows", "*.yml"))
	require.NoError(t, err)
	require.NotEmpty(t, workflows)

	for _, workflow := range workflows {
		data, err := os.ReadFile(workflow)
		require.NoError(t, err)

		content := string(data)
		name := filepath.Base(workflow)

		assert.False(t, pinnedGoVersion.MatchString(content),
			"%s pins go-version; use go-version-file: go.mod", name)

		steps := setupGo.FindAllStringSubmatch(content, -1)
		for _, step := range steps {
			major, err := strconv.Atoi(step[1])
			require.NoError(t, err)
			assert.GreaterOrEqual(t, major, 6, "%s uses actions/setup-go@v%d", name, major)
		}

		assert.Len(t, goVersionFile.FindAllString(content, -1), len(steps),
			"%s: every setup-go step must read go-version-file: go.mod", name)
	}
}
