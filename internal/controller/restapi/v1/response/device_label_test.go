package response

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeviceLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		want      string
	}{
		{
			name:      "Chrome on Mac",
			userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36",
			want:      "Chrome di Mac",
		},
		{
			name:      "Edge takes precedence over Chrome",
			userAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0.0.0 Safari/537.36 Edg/126.0.0.0",
			want:      "Edge di Windows",
		},
		{
			name:      "Safari on iPhone",
			userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 Version/17.5 Mobile/15E148 Safari/604.1",
			want:      "Safari di iPhone",
		},
		{
			name:      "Firefox iOS",
			userAgent: "Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 FxiOS/127.0 Mobile/15E148 Safari/605.1.15",
			want:      "Firefox di iPad",
		},
		{
			name:      "Surau Android app",
			userAgent: "SurauAndroid/4.2.0 (Android 15)",
			want:      "Aplikasi Surau di Android",
		},
		{
			name:      "generic Android app",
			userAgent: "okhttp/4.12.0 Android",
			want:      "Aplikasi Android",
		},
		{
			name:      "platform only",
			userAgent: "iPhone",
			want:      "Perangkat iPhone",
		},
		{name: "blank", userAgent: "", want: unknownDeviceLabel},
		{name: "unknown CLI", userAgent: "curl/8.7.1", want: unknownDeviceLabel},
		{name: "HTML payload", userAgent: "<script>Chrome/126 Windows</script>", want: unknownDeviceLabel},
		{name: "control payload", userAgent: "Chrome/126\nWindows", want: unknownDeviceLabel},
		{name: "bidi payload", userAgent: "Chrome/126\u202eWindows", want: unknownDeviceLabel},
		{name: "overlong", userAgent: strings.Repeat("Chrome/126 ", 60), want: unknownDeviceLabel},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := deviceLabel(tc.userAgent)
			assert.Equal(t, tc.want, got)
			assert.NotContains(t, got, "<")
			assert.LessOrEqual(t, len([]rune(got)), 64)
		})
	}
}
