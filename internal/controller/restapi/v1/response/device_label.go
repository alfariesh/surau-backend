package response

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const unknownDeviceLabel = "Perangkat tidak dikenal"

// deviceLabel turns untrusted User-Agent metadata into a small, fixed
// vocabulary suitable for display. It never echoes arbitrary input.
func deviceLabel(userAgent string) string {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" || utf8.RuneCountInString(userAgent) > 512 || unsafeDeviceMetadata(userAgent) {
		return unknownDeviceLabel
	}

	ua := strings.ToLower(userAgent)

	platform := devicePlatform(ua)
	if native := nativeAppLabel(ua, platform); native != "" {
		return native
	}

	browser := deviceBrowser(ua)
	if browser != "" && platform != "" {
		return browser + " di " + platform
	}

	if browser != "" {
		return browser
	}

	if platform != "" {
		return "Perangkat " + platform
	}

	return unknownDeviceLabel
}

func unsafeDeviceMetadata(value string) bool {
	if strings.ContainsAny(value, "<>") {
		return true
	}

	for _, r := range value {
		if unicode.IsControl(r) || isBidiControl(r) {
			return true
		}
	}

	return false
}

func isBidiControl(r rune) bool {
	switch r {
	case '\u061c', '\u200e', '\u200f', '\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
		'\u2066', '\u2067', '\u2068', '\u2069':
		return true
	default:
		return false
	}
}

func devicePlatform(ua string) string {
	switch {
	case strings.Contains(ua, "iphone"):
		return "iPhone"
	case strings.Contains(ua, "ipad"):
		return "iPad"
	case strings.Contains(ua, "android"):
		return "Android"
	case strings.Contains(ua, "windows"):
		return "Windows"
	case strings.Contains(ua, "macintosh") || strings.Contains(ua, "mac os x"):
		return "Mac"
	case strings.Contains(ua, "linux"):
		return "Linux"
	default:
		return ""
	}
}

func deviceBrowser(ua string) string {
	switch {
	case matchesDeviceClient(ua, "edge", "edg/", "edga/", "edgios/"):
		return "Edge"
	case matchesDeviceClient(ua, "chrome", "chrome/", "crios/"):
		return "Chrome"
	case matchesDeviceClient(ua, "firefox", "firefox/", "fxios/"):
		return "Firefox"
	case ua == "safari" || matchesAllDeviceTokens(ua, "version/", "safari/"):
		return "Safari"
	default:
		return ""
	}
}

func matchesDeviceClient(ua, exact string, tokens ...string) bool {
	if ua == exact {
		return true
	}

	for _, token := range tokens {
		if strings.Contains(ua, token) {
			return true
		}
	}

	return false
}

func matchesAllDeviceTokens(ua string, tokens ...string) bool {
	for _, token := range tokens {
		if !strings.Contains(ua, token) {
			return false
		}
	}

	return true
}

func nativeAppLabel(ua, platform string) string {
	if strings.Contains(ua, "surauandroid/") || strings.Contains(ua, "surau-android/") {
		return "Aplikasi Surau di Android"
	}

	if strings.Contains(ua, "surauios/") || strings.Contains(ua, "surau-ios/") {
		return "Aplikasi Surau di iOS"
	}

	if strings.Contains(ua, "okhttp/") && platform == "Android" {
		return "Aplikasi Android"
	}

	if strings.Contains(ua, "cfnetwork/") || strings.Contains(ua, "darwin/") {
		return "Aplikasi iOS"
	}

	return ""
}
