package readerlang

import (
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{name: "empty defaults to Indonesian", want: "id"},
		{name: "uppercase", in: " EN ", want: "en"},
		{name: "english region", in: "en-US", want: "en"},
		{name: "indonesian region", in: "id-ID", want: "id"},
		{name: "arabic region underscore", in: "ar_SA", want: "ar"},
		{name: "unsupported", in: "fr", wantErr: entity.ErrUnsupportedLanguage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Normalize(tt.in)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
