package quranutil

import "strings"

//go:generate go run -tags normalization_generator ./gen_v1_unicode

const (
	SearchKeyV1ProfileName    = "search-key"
	SearchKeyV1ProfileVersion = 1
)

type unicodeV1Range struct {
	lo rune
	hi rune
}

// NormalizeKeyV1 is the immutable search-key/v1 implementation. Its Unicode
// letter/digit table is pinned to Go's Unicode 15.0 data, so a runtime upgrade
// cannot silently change keys already persisted in PostgreSQL.
func NormalizeKeyV1(value string) string {
	value = stripArabicMarksV1(value)
	value = strings.Map(func(r rune) rune {
		switch r {
		case 'أ', 'إ', 'آ', 'ٱ':
			return 'ا'
		case 'ى':
			return 'ي'
		case 'ؤ':
			return 'و'
		case 'ئ':
			return 'ي'
		case 'ـ':
			return -1
		}

		if isV1LetterOrDigit(r) {
			return r
		}

		return ' '
	}, value)

	return strings.Join(strings.Fields(value), " ")
}

func isV1LetterOrDigit(r rune) bool {
	left, right := 0, len(normalizationV1LetterDigitRanges)
	for left < right {
		middle := left + (right-left)/2 //nolint:mnd // binary-search midpoint
		interval := normalizationV1LetterDigitRanges[middle]

		switch {
		case r < interval.lo:
			right = middle
		case r > interval.hi:
			left = middle + 1
		default:
			return true
		}
	}

	return false
}

func stripArabicMarksV1(value string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '\u0610' && r <= '\u061a':
			return -1
		case r >= '\u064b' && r <= '\u065f':
			return -1
		case r == '\u0670':
			return -1
		case r >= '\u06d6' && r <= '\u06ed':
			return -1
		default:
			return r
		}
	}, value)
}
