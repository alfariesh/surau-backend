package apierror

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "known auth token code",
			msg:  "invalid or expired token",
			want: CodeAuthTokenInvalid,
		},
		{
			name: "known auth credentials code",
			msg:  "invalid credentials",
			want: CodeAuthCredentials,
		},
		{
			name: "legacy fallback snake case",
			msg:  "unsupported language",
			want: "unsupported_language",
		},
		{
			name: "empty fallback",
			msg:  " ",
			want: "error",
		},
	}

	for _, tt := range tests {
		localTT := tt

		t.Run(localTT.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, localTT.want, Code(localTT.msg))
		})
	}
}
