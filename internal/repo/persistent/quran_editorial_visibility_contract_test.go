package persistent

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPublicQuranReadsUseEditorialVisibilityViews prevents future public
// queries from bypassing the Q-1 published+permitted predicate.
func TestPublicQuranReadsUseEditorialVisibilityViews(t *testing.T) {
	t.Parallel()

	source, err := os.ReadFile("quran_postgres.go")
	require.NoError(t, err)

	require.Contains(t, string(source), "quran_surah_editorial_public")
	require.Contains(t, string(source), "quran_ayah_editorial_public")

	baseRead := regexp.MustCompile(`(?i)\b(?:from|join)\s+quran_(?:surah|ayah)_editorial\b`)
	require.False(t, baseRead.Match(source), "public Quran repo must not read base editorial tables")
}
