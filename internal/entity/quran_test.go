package entity

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBookQuranReferenceNormalizationVersionJSON(t *testing.T) {
	t.Parallel()

	version := 1
	for _, test := range []struct {
		name    string
		version *int
		want    string
	}{
		{name: "legacy is explicit null", want: "null"},
		{name: "v1 is explicit integer", version: &version, want: "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			raw, err := json.Marshal(BookQuranReference{NormalizationVersion: test.version})
			require.NoError(t, err)

			var payload map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &payload))
			actual, exists := payload["normalization_version"]
			require.True(t, exists, "legacy state must remain visible rather than omitted")
			assert.JSONEq(t, test.want, string(actual))
		})
	}
}
