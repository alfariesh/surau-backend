package response_test

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
)

// F1-D (decision F1-D3): list envelopes are a live FE/mobile contract.
// The structs below are the FROZEN legacy exceptions that predate the
// {items,total} rule — their JSON keys must never change; every NEW list
// endpoint must use literal `items` + `total` (docs/module-conventions.md).
func TestLegacyListEnvelopesAreFrozen(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		value    any
		wantTags []string
	}{
		{"AdminUserList", response.AdminUserList{}, []string{"users", "total"}},
		{"AdminUserActivityList", response.AdminUserActivityList{}, []string{"activity", "total"}},
		{"ProductionProjectList", response.ProductionProjectList{}, []string{"projects", "total"}},
		{"ProductionCandidateList", response.ProductionCandidateList{}, []string{"candidates", "total"}},
		{"ProductionEventList", response.ProductionEventList{}, []string{"events", "total"}},
		{"ProductionDraftRevisionList", response.ProductionDraftRevisionList{}, []string{"revisions", "total"}},
		{"SourceEditRevisionList", response.SourceEditRevisionList{}, []string{"revisions", "total"}},
		{"TranslationFeedbackList", response.TranslationFeedbackList{}, []string{"feedbacks", "total"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.wantTags, jsonKeys(tc.value),
				"%s is a FROZEN legacy envelope — changing its JSON keys breaks live clients", tc.name)
		})
	}
}

// Representative {items,total} envelopes: the standing rule for every list.
func TestStandardListEnvelopesUseItemsTotal(t *testing.T) {
	t.Parallel()

	for _, value := range []any{
		response.AuthorList{},
		response.EmailMessageList{},
	} {
		keys := jsonKeys(value)
		assert.Equal(t, []string{"items", "total"}, keys,
			"%T must keep the standard {items,total} envelope", value)
	}
}

// The error envelope's field set is part of the same contract.
func TestErrorEnvelopeFieldsAreFrozen(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		[]string{"error", "code", "message", "details", "retry_after", "request_id"},
		jsonKeys(response.Error{}),
	)
	assert.Equal(t,
		[]string{"error", "code", "request_id", "existing_project_id"},
		jsonKeys(response.ProductionProjectConflict{}),
	)
}

// jsonKeys returns the struct's json tag names in field order (omitempty
// stripped).
func jsonKeys(value any) []string {
	typ := reflect.TypeOf(value)
	keys := make([]string, 0, typ.NumField())

	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}

		if comma := len(tag); comma > 0 {
			for j, r := range tag {
				if r == ',' {
					comma = j

					break
				}
			}

			tag = tag[:comma]
		}

		keys = append(keys, tag)
	}

	return keys
}