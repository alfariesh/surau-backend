package evalrubric

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAllowsOnlyVersionedGroundednessAndIkhtilaf(t *testing.T) {
	t.Parallel()

	groundedness, err := Resolve(GroundednessV1)
	require.NoError(t, err)
	assert.Equal(t, GroundednessV1, groundedness.Version)
	assert.Len(t, groundedness.SHA256, 64)
	assert.Contains(t, string(groundedness.Content), `"kind": "groundedness"`)

	ikhtilaf, err := Resolve(IkhtilafV1)
	require.NoError(t, err)
	assert.Equal(t, IkhtilafV1, ikhtilaf.Version)
	assert.Len(t, ikhtilaf.SHA256, 64)
	assert.NotEqual(t, groundedness.SHA256, ikhtilaf.SHA256)

	_, err = Resolve("free-form-rubric")
	require.Error(t, err)
	assert.True(t, IsNotFound(err))
}
