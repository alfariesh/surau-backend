package quranutil

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// AyahKey returns the canonical QUL-style "surah:ayah" key.
func AyahKey(surahID, ayahNumber int) string {
	return fmt.Sprintf("%d:%d", surahID, ayahNumber)
}

// ParseAyahKey parses a canonical "surah:ayah" key.
func ParseAyahKey(key string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(key), ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid ayah key %q", key)
	}

	surahID, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid surah in ayah key %q: %w", key, err)
	}

	ayahNumber, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid ayah in ayah key %q: %w", key, err)
	}
	if surahID <= 0 || ayahNumber <= 0 {
		return 0, 0, fmt.Errorf("invalid ayah key %q", key)
	}

	return surahID, ayahNumber, nil
}

// NormalizeKey removes Quranic marks and normalizes Arabic variants for lookup.
// It is intentionally for search/linking only; never use it as display text.
func NormalizeKey(value string) string {
	value = stripArabicMarks(value)
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
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		if unicode.IsSpace(r) {
			return ' '
		}
		return ' '
	}, value)

	return strings.Join(strings.Fields(value), " ")
}

func stripArabicMarks(value string) string {
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
