package quranutil

//go:generate go run ../../scripts/quran_page_map -output qpc_hafs_page_map_v1_gen.go

// QPCHafsPageNumber returns the frozen QPC Hafs Madani mushaf page for one
// canonical ayah. The boolean is false for coordinates outside the 6,236
// ayahs in the versioned source snapshot.
func QPCHafsPageNumber(surahID, ayahNumber int) (int, bool) {
	if surahID <= 0 || surahID >= len(qpcHafsPageNumbersV1) {
		return 0, false
	}

	pages := qpcHafsPageNumbersV1[surahID]
	if ayahNumber <= 0 || ayahNumber >= len(pages) {
		return 0, false
	}

	page := int(pages[ayahNumber])

	return page, page > 0
}
