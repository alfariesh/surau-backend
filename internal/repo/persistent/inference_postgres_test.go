package persistent

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errInferenceRouteRowFixture = errors.New("invalid inference route row fixture")

type inferenceRouteRowFixture []any

func (values inferenceRouteRowFixture) Scan(dest ...any) error {
	if len(dest) != len(values) {
		return fmt.Errorf(
			"%w: destination count %d, want %d",
			errInferenceRouteRowFixture,
			len(dest),
			len(values),
		)
	}

	for index, value := range values {
		if value == nil {
			continue
		}

		target := reflect.ValueOf(dest[index]).Elem()

		source := reflect.ValueOf(value)
		if !source.Type().AssignableTo(target.Type()) {
			return fmt.Errorf(
				"%w: column %d type %s cannot assign to %s",
				errInferenceRouteRowFixture,
				index,
				source.Type(),
				target.Type(),
			)
		}

		target.Set(source)
	}

	return nil
}

func TestScanInferenceRouteAllowsPromptWithoutPolicyHash(t *testing.T) {
	t.Parallel()

	row := inferenceRouteRowFixture{
		"book-rag-answer", "answer", "structured", 600, false, 1,
		"deterministic-rollout", "deterministic://local", "INFERENCE_DETERMINISTIC_NO_SECRET",
		"deterministic-rollout", "deterministic-rollout", "deterministic-v1",
		int64(0), int64(0), int64(0), true, false, 30000, 4096,
		"book-rag-answer-v1", "prompt-sha", nil,
		[]byte(`[{"role":"user","content":"{{question}}"}]`),
		"book-rag-answer-schema-v1", "schema-sha",
		[]byte(`{"type":"object"}`), 0.2,
	}

	route, err := scanInferenceRoute(row)
	require.NoError(t, err)

	assert.Equal(t, "book-rag-answer", route.TaskKey)
	assert.Empty(t, route.PolicySHA256)
	assert.JSONEq(t, `[{"role":"user","content":"{{question}}"}]`, string(route.MessagesTemplate))
	assert.JSONEq(t, `{"type":"object"}`, string(route.ResponseSchema))
}
