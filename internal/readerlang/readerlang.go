// Package readerlang normalizes public kitab language codes.
package readerlang

import "github.com/evrone/go-clean-template/internal/contentlang"

const (
	Default = contentlang.Default
	Arabic  = contentlang.Arabic
	English = contentlang.English
)

// Normalize returns the supported primary language code for kitab APIs.
func Normalize(lang string) (string, error) {
	return contentlang.Normalize(lang)
}

// MustNormalize returns a supported language or the default.
func MustNormalize(lang string) string {
	return contentlang.MustNormalize(lang)
}

// IsArabic reports whether the normalized language is the source language.
func IsArabic(lang string) bool {
	return contentlang.IsArabic(lang)
}
