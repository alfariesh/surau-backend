package persistent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// activityDate must bucket events into the calendar date of their own UTC
// offset so late-night local reading counts on the local day.
func TestActivityDateUsesClientLocalDay(t *testing.T) {
	t.Parallel()

	wib := time.FixedZone("WIB", 7*60*60)

	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{
			name: "early local morning is previous UTC day",
			// 00:30 WIB on June 13 = 17:30 UTC on June 12; local day wins.
			at:   time.Date(2026, 6, 13, 0, 30, 0, 0, wib),
			want: "2026-06-13",
		},
		{
			name: "late local night stays on local day",
			at:   time.Date(2026, 6, 12, 23, 50, 0, 0, wib),
			want: "2026-06-12",
		},
		{
			name: "utc timestamp buckets by utc day",
			at:   time.Date(2026, 6, 12, 23, 50, 0, 0, time.UTC),
			want: "2026-06-12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, activityDate(tt.at))
		})
	}
}
