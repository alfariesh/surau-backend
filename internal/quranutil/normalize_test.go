package quranutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	got := NormalizeKey("إِنَّا أَنزَلْنَـٰهُ فِي لَيْلَةِ ٱلْقَدْرِ")

	assert.Equal(t, "انا انزلنه في ليلة القدر", got)
}
