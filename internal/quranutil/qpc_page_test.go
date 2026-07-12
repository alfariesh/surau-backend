package quranutil

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQPCHafsPageMapV1CoverageAndChecksum(t *testing.T) {
	t.Parallel()

	var snapshot bytes.Buffer

	pages := make(map[int]struct{}, 604)
	ayahCount := 0
	lastPage := 0

	for surahID := 1; surahID < len(qpcHafsPageNumbersV1); surahID++ {
		for ayahNumber := 1; ayahNumber < len(qpcHafsPageNumbersV1[surahID]); ayahNumber++ {
			page, ok := QPCHafsPageNumber(surahID, ayahNumber)
			require.True(t, ok)
			require.GreaterOrEqual(t, page, lastPage, "page map must follow canonical ayah order")
			fmt.Fprintf(&snapshot, "%d:%d\t%d\n", surahID, ayahNumber, page)
			pages[page] = struct{}{}
			ayahCount++
			lastPage = page
		}
	}

	checksum := sha256.Sum256(snapshot.Bytes())

	assert.Equal(t, 6236, ayahCount)
	assert.Len(t, pages, 604)
	assert.Equal(t, QPCHafsPageMapSnapshotSHA256, hex.EncodeToString(checksum[:]))
	assert.Equal(t, 1, QPCHafsPageMapProfileVersion)
}

func TestQPCHafsPageNumber(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		surah int
		ayah  int
		page  int
	}{
		{surah: 1, ayah: 1, page: 1},
		{surah: 2, ayah: 255, page: 42},
		{surah: 73, ayah: 4, page: 574},
		{surah: 114, ayah: 6, page: 604},
	} {
		page, ok := QPCHafsPageNumber(test.surah, test.ayah)
		require.True(t, ok)
		assert.Equal(t, test.page, page)
	}

	for _, invalid := range [][2]int{{0, 1}, {1, 0}, {1, 8}, {114, 7}, {115, 1}} {
		page, ok := QPCHafsPageNumber(invalid[0], invalid[1])
		assert.False(t, ok)
		assert.Zero(t, page)
	}
}
