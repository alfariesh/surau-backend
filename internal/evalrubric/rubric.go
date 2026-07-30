// Package evalrubric owns the immutable U-6 judge rubrics. The generic U-0
// judge task may receive only material resolved through this allowlist.
package evalrubric

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	GroundednessV1 = "groundedness-v1"
	IkhtilafV1     = "ikhtilaf-v1"
)

var (
	//go:embed assets/*.json
	rubricAssets embed.FS

	errRubricNotFound = errors.New("eval rubric not found")
)

// Rubric is immutable judge material plus its content hash.
type Rubric struct {
	Version string
	SHA256  string
	Content []byte
}

// Resolve returns only allowlisted rubric versions.
func Resolve(version string) (Rubric, error) {
	version = strings.TrimSpace(version)
	switch version {
	case GroundednessV1, IkhtilafV1:
	default:
		return Rubric{}, fmt.Errorf("%w: %q", errRubricNotFound, version)
	}

	content, err := rubricAssets.ReadFile(path.Join("assets", version+".json"))
	if err != nil {
		return Rubric{}, fmt.Errorf("read eval rubric: %w", err)
	}

	sum := sha256.Sum256(content)

	return Rubric{
		Version: version,
		SHA256:  hex.EncodeToString(sum[:]),
		Content: content,
	}, nil
}

// IsNotFound reports a caller-selected rubric outside the allowlist.
func IsNotFound(err error) bool {
	return errors.Is(err, errRubricNotFound)
}
