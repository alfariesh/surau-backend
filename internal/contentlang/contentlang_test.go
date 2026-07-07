package contentlang

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
		{name: "empty defaults to id", in: "", want: Default},
		{name: "uppercase", in: "EN", want: English},
		{name: "whitespace", in: " id ", want: Default},
		{name: "english region", in: "en-US", want: English},
		{name: "indonesian region", in: "id-ID", want: Default},
		{name: "arabic region", in: "ar-SA", want: Arabic},
		{name: "underscore region", in: "en_US", want: English},
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
