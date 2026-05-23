package postgres

import "testing"

func TestSafeIntToInt32(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int
		want int32
	}{
		{name: "positive", in: 12, want: 12},
		{name: "zero uses default", in: 0, want: _defaultMaxPoolSize},
		{name: "negative uses default", in: -1, want: _defaultMaxPoolSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := safeIntToInt32(tt.in); got != tt.want {
				t.Fatalf("safeIntToInt32(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
