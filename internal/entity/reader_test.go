package entity

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRawJSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := struct {
		Metadata RawJSON `json:"metadata"`
	}{Metadata: RawJSON(`{"source":"resolver","score":0.95}`)}

	encoded, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded struct {
		Metadata RawJSON `json:"metadata"`
	}
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.JSONEq(t, string(original.Metadata), string(decoded.Metadata))
}

func TestRawJSONUnmarshalNullAndInvalid(t *testing.T) {
	t.Parallel()

	value := RawJSON(`{"kept":true}`)
	require.NoError(t, json.Unmarshal([]byte("null"), &value))
	require.Nil(t, value)
	require.Error(t, json.Unmarshal([]byte(`{"broken":`), &value))
}
