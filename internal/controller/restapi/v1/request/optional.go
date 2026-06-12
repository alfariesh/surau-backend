package request

import (
	"bytes"
	"encoding/json"
)

// Optional distinguishes an absent JSON key from an explicit null so PATCH
// bodies can express "leave unchanged" (absent) versus "clear" (null).
// Set is true when the key appeared in the body; Value is nil for explicit null.
type Optional[T any] struct {
	Set   bool
	Value *T
}

// UnmarshalJSON marks the field as present and decodes non-null payloads.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = nil

		return nil
	}

	return json.Unmarshal(data, &o.Value)
}

// MarshalJSON keeps round-tripping symmetric for tests and docs tooling.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Value == nil {
		return []byte("null"), nil
	}

	return json.Marshal(o.Value)
}
