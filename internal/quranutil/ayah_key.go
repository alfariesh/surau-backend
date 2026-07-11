package quranutil

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var errInvalidAyahKey = errors.New("invalid ayah key")

// AyahKey returns the canonical QUL-style "surah:ayah" key.
func AyahKey(surahID, ayahNumber int) string {
	return fmt.Sprintf("%d:%d", surahID, ayahNumber)
}

// ParseAyahKey parses a canonical "surah:ayah" key.
func ParseAyahKey(key string) (surahID, ayahNumber int, err error) {
	const ayahKeyParts = 2

	parts := strings.Split(strings.TrimSpace(key), ":")
	if len(parts) != ayahKeyParts {
		return 0, 0, fmt.Errorf("%w %q", errInvalidAyahKey, key)
	}

	surahID, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid surah in ayah key %q: %w", key, err)
	}

	ayahNumber, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid ayah in ayah key %q: %w", key, err)
	}

	if surahID <= 0 || ayahNumber <= 0 {
		return 0, 0, fmt.Errorf("%w %q", errInvalidAyahKey, key)
	}

	return surahID, ayahNumber, nil
}
