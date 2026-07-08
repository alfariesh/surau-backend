package integration_test

import (
	"net/http"
	"testing"
)

// TestUnknownRouteReturnsErrorEnvelope proves the F1-D catch-all: unmatched
// routes answer with the standard JSON envelope (frozen message/code)
// instead of fiber's plain-text 404.
func TestUnknownRouteReturnsErrorEnvelope(t *testing.T) {
	resp := doJSON(t, http.MethodGet, baseURL()+"/v1/definitely-not-a-route", nil, "")

	var body struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
	}

	decodeAndClose(t, resp, &body)

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown route expected 404, got %d", resp.StatusCode)
	}

	if body.Error != "not found" || body.Code != "not_found" {
		t.Fatalf("unexpected envelope: %+v", body)
	}

	if body.RequestID == "" {
		t.Fatal("catch-all 404 must carry request_id")
	}
}
