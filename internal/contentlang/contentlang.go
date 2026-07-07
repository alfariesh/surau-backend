// Package contentlang normalizes public content language codes.
package contentlang

import (
	"strings"

	"github.com/alfariesh/surau-backend/internal/entity"
)

const (
	Default = "id"
	Arabic  = "ar"
	English = "en"
)

// Normalize returns a supported primary language code for public content APIs.
func Normalize(lang string) (string, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang == "" {
		return Default, nil
	}

	lang = strings.ReplaceAll(lang, "_", "-")
	if before, _, ok := strings.Cut(lang, "-"); ok {
		lang = before
	}

	switch lang {
	case Arabic, Default, English:
		return lang, nil
	default:
		return "", entity.ErrUnsupportedLanguage
	}
}

// MustNormalize returns a supported language or the default.
func MustNormalize(lang string) string {
	normalized, err := Normalize(lang)
	if err != nil {
		return Default
	}

	return normalized
}

// IsArabic reports whether the normalized language is the source language.
func IsArabic(lang string) bool {
	return lang == Arabic
}
