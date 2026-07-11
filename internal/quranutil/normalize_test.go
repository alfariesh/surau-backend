package quranutil

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	normalizationV1GoldenSHA256  = "c3b5e72310e8b35684769b26cf7a4fd6ade8debedd10505745bb941569097bb7"
	normalizationV1UnicodeSHA256 = "0f6a0f2050b296525bd83f5b107d6474341f7e8e6410b33ea008bb04ac62cd91"
)

type normalizationGoldenCorpus struct {
	Profile struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	} `json:"profile"`
	Vectors []struct {
		Name     string `json:"name"`
		Input    string `json:"input"`
		Expected string `json:"expected"`
	} `json:"vectors"`
}

type normalizationUnicodeCorpus struct {
	Profile struct {
		Name    string `json:"name"`
		Version int    `json:"version"`
	} `json:"profile"`
	SourceUnicodeVersion string   `json:"source_unicode_version"`
	Ranges               [][2]int `json:"ranges"`
}

func TestAyahKey(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "73:4", AyahKey(73, 4))
}

func TestParseAyahKey(t *testing.T) {
	t.Parallel()

	surahID, ayahNumber, err := ParseAyahKey("73:4")
	require.NoError(t, err)
	assert.Equal(t, 73, surahID)
	assert.Equal(t, 4, ayahNumber)

	_, _, err = ParseAyahKey("73")
	require.Error(t, err)

	_, _, err = ParseAyahKey("0:4")
	require.Error(t, err)
}

func TestNormalizeKey(t *testing.T) {
	t.Parallel()

	corpus := loadNormalizationGoldenCorpus(t)
	require.NotEmpty(t, corpus.Vectors)

	for _, vector := range corpus.Vectors {
		t.Run(vector.Name, func(t *testing.T) {
			t.Parallel()

			got := NormalizeKey(vector.Input)

			assert.Equal(t, vector.Expected, got)
			assert.Equal(t, got, NormalizeKey(got), "normalization must be idempotent")
			assertCanonicalSearchKey(t, got)
		})
	}
}

func TestNormalizationV1ContractIsFrozen(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("normalization_v1_vectors.json")
	require.NoError(t, err)

	digest := sha256.Sum256(raw)
	assert.Equal(t, normalizationV1GoldenSHA256, fmt.Sprintf("%x", digest),
		"v1 corpus is immutable; introduce a new version and corpus for semantic changes")

	corpus := loadNormalizationGoldenCorpus(t)
	assert.Equal(t, ProfileName, corpus.Profile.Name)
	assert.Equal(t, ProfileVersion, corpus.Profile.Version)
	assert.Equal(t, "search-key", ProfileName)
	assert.Equal(t, 1, ProfileVersion)
	assert.Equal(t, SearchKeyV1ProfileName, ProfileName)
	assert.Equal(t, SearchKeyV1ProfileVersion, ProfileVersion)
}

func TestNormalizationV1UnicodeParityIsExhaustive(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("normalization_v1_unicode_ranges.json")
	require.NoError(t, err)

	digest := sha256.Sum256(raw)
	require.Equal(t, normalizationV1UnicodeSHA256, fmt.Sprintf("%x", digest))

	var corpus normalizationUnicodeCorpus
	require.NoError(t, json.Unmarshal(raw, &corpus))
	require.Equal(t, ProfileName, corpus.Profile.Name)
	require.Equal(t, ProfileVersion, corpus.Profile.Version)
	require.Equal(t, "15.0.0", corpus.SourceUnicodeVersion)
	require.NotEmpty(t, corpus.Ranges)

	rangeIndex := 0

	const (
		maxRune   = 0x10FFFF
		batchSize = 4096
	)

	var (
		input          strings.Builder
		expectedTokens = make([]string, 0, batchSize)
	)

	for codePoint := 0; codePoint <= maxRune; codePoint++ {
		for rangeIndex < len(corpus.Ranges) && codePoint > corpus.Ranges[rangeIndex][1] {
			rangeIndex++
		}

		expectedClass := rangeIndex < len(corpus.Ranges) &&
			codePoint >= corpus.Ranges[rangeIndex][0]
		if actual := isV1LetterOrDigit(rune(codePoint)); actual != expectedClass {
			t.Fatalf("classification mismatch at U+%04X: got %t, want %t", codePoint, actual, expectedClass)
		}

		// UTF-8 excludes surrogate code points, but their classification above
		// is still checked against the shared table.
		if codePoint >= 0xD800 && codePoint <= 0xDFFF {
			continue
		}

		r := rune(codePoint)
		input.WriteRune(r)
		input.WriteByte('|')

		if token := expectedSingleRuneV1(r, expectedClass); token != "" {
			expectedTokens = append(expectedTokens, token)
		}

		if (codePoint+1)%batchSize == 0 || codePoint == maxRune {
			expected := strings.Join(expectedTokens, " ")
			if actual := NormalizeKeyV1(input.String()); actual != expected {
				t.Fatalf("normalization mismatch in batch ending U+%04X", codePoint)
			}

			input.Reset()

			expectedTokens = expectedTokens[:0]
		}
	}
}

func loadNormalizationGoldenCorpus(t *testing.T) normalizationGoldenCorpus {
	t.Helper()

	raw, err := os.ReadFile("normalization_v1_vectors.json")
	require.NoError(t, err)

	var corpus normalizationGoldenCorpus
	require.NoError(t, json.Unmarshal(raw, &corpus))

	return corpus
}

func assertCanonicalSearchKey(t *testing.T, value string) {
	t.Helper()

	assert.Equal(t, strings.TrimSpace(value), value)
	assert.NotContains(t, value, "  ")

	for _, r := range value {
		assert.Truef(t, isV1LetterOrDigit(r) || r == ' ',
			"unexpected rune %U in canonical search key", r)
	}
}

func expectedSingleRuneV1(r rune, accepted bool) string {
	switch {
	case r >= '\u0610' && r <= '\u061a':
		return ""
	case r >= '\u064b' && r <= '\u065f':
		return ""
	case r == '\u0670':
		return ""
	case r >= '\u06d6' && r <= '\u06ed':
		return ""
	}

	switch r {
	case 'أ', 'إ', 'آ', 'ٱ':
		return "ا"
	case 'ى', 'ئ':
		return "ي"
	case 'ؤ':
		return "و"
	case 'ـ':
		return ""
	}

	if accepted {
		return string(r)
	}

	return ""
}
