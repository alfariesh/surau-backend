package webapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testOneSignalErasureAppID      = "7a650cae-1c1e-4b19-a7fe-393c14b894f0"
	testOneSignalErasureExternalID = "11111111-1111-4111-8111-111111111111"
)

func TestOneSignalErasureClientDeleteClassifiesResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		retryAfter string
		reasonCode string
		accepted   bool
		notFound   bool
		retryable  bool
		systemic   bool
	}{
		{name: "accepted", status: http.StatusAccepted, reasonCode: "delete_accepted", accepted: true},
		{name: "already absent", status: http.StatusNotFound, reasonCode: "not_found", notFound: true},
		{name: "invalid request", status: http.StatusBadRequest, reasonCode: "invalid_request"},
		{name: "unauthorized", status: http.StatusUnauthorized, reasonCode: "unauthorized", retryable: true, systemic: true},
		{name: "forbidden", status: http.StatusForbidden, reasonCode: "unauthorized", retryable: true, systemic: true},
		{name: "rate limited", status: http.StatusTooManyRequests, retryAfter: "120", reasonCode: "rate_limited", retryable: true, systemic: true},
		{name: "provider unavailable", status: http.StatusServiceUnavailable, reasonCode: "provider_unavailable", retryable: true, systemic: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				assert.Equal(t, http.MethodDelete, request.Method)
				assert.Equal(
					t,
					"/apps/"+testOneSignalErasureAppID+"/users/by/external_id/"+testOneSignalErasureExternalID,
					request.URL.Path,
				)
				assert.Equal(t, "Key server-secret", request.Header.Get("Authorization"))

				if tc.retryAfter != "" {
					writer.Header().Set("Retry-After", tc.retryAfter)
				}

				writer.WriteHeader(tc.status)
				_, writeErr := writer.Write(
					[]byte(`{"errors":["` + testOneSignalErasureExternalID + ` rejected"]}`),
				)
				require.NoError(t, writeErr)
			}))
			defer server.Close()

			client := NewOneSignalErasureClient(OneSignalOptions{
				BaseURL: server.URL, AppID: testOneSignalErasureAppID,
				RESTAPIKey: "server-secret", Timeout: time.Second,
			})
			result, err := client.DeleteUser(t.Context(), testOneSignalErasureExternalID)

			require.NoError(t, err)
			assert.Equal(t, tc.status, result.HTTPStatus)
			assert.Equal(t, tc.reasonCode, result.ReasonCode)
			assert.Equal(t, tc.accepted, result.Accepted)
			assert.Equal(t, tc.notFound, result.NotFound)
			assert.Equal(t, tc.retryable, result.Retryable)
			assert.Equal(t, tc.systemic, result.Systemic)
			assert.NotContains(t, result.ReasonDetail, testOneSignalErasureExternalID)
			assert.NotContains(t, result.ReasonDetail, "server-secret")

			if tc.retryAfter != "" {
				assert.Equal(t, 2*time.Minute, result.RetryAfter)
			}
		})
	}
}

func TestOneSignalErasureClientViewAndTimeout(t *testing.T) {
	t.Parallel()

	t.Run("existing user remains in verification", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			assert.Equal(t, http.MethodGet, request.Method)
			writer.WriteHeader(http.StatusOK)
			_, writeErr := writer.Write(
				[]byte(`{"identity":{"external_id":"` + testOneSignalErasureExternalID + `"}}`),
			)
			require.NoError(t, writeErr)
		}))
		defer server.Close()

		client := NewOneSignalErasureClient(OneSignalOptions{
			BaseURL: server.URL, AppID: testOneSignalErasureAppID,
			RESTAPIKey: "server-secret", Timeout: time.Second,
		})
		result, err := client.ViewUser(t.Context(), testOneSignalErasureExternalID)

		require.NoError(t, err)
		assert.True(t, result.Accepted)
		assert.Equal(t, "user_exists", result.ReasonCode)
		assert.NotContains(t, result.ReasonDetail, testOneSignalErasureExternalID)
	})

	t.Run("timeout is retryable and systemic", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		client := NewOneSignalErasureClient(OneSignalOptions{
			BaseURL: server.URL, AppID: testOneSignalErasureAppID,
			RESTAPIKey: "server-secret", Timeout: time.Millisecond,
		})
		result, err := client.DeleteUser(context.Background(), testOneSignalErasureExternalID)

		require.Error(t, err)
		assert.Equal(t, "timeout", result.ReasonCode)
		assert.True(t, result.Retryable)
		assert.True(t, result.Systemic)
	})

	t.Run("rejects non canonical external id before HTTP", func(t *testing.T) {
		t.Parallel()

		client := NewOneSignalErasureClient(OneSignalOptions{
			BaseURL: "https://example.invalid", AppID: testOneSignalErasureAppID,
			RESTAPIKey: "server-secret", Timeout: time.Second,
		})
		result, err := client.DeleteUser(
			t.Context(),
			strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		)

		require.ErrorIs(t, err, errOneSignalInvalidExternalID)
		assert.Equal(t, "invalid_external_id", result.ReasonCode)
	})
}
