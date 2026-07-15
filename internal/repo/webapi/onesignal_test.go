package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testOneSignalIdempotencyKey = "f53db014-1d01-4c02-8f63-458e6f32b012"

var errTestOneSignalNetwork = errors.New("dial failed")

func TestOneSignalClientSendAcceptedUsesStableIdempotencyKey(t *testing.T) {
	t.Parallel()

	var (
		mu            sync.Mutex
		requests      []oneSignalRequest
		notifications = make(map[string]string)
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/notifications", r.URL.Path)
		assert.Equal(t, "Key api-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var request oneSignalRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		mu.Lock()

		requests = append(requests, request)

		providerID, exists := notifications[request.IdempotencyKey]
		if !exists {
			providerID = fmt.Sprintf("provider-notification-%d", len(notifications)+1)
			notifications[request.IdempotencyKey] = providerID
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, err := fmt.Fprintf(w, `{"id":%q}`, providerID)
		assert.NoError(t, err)
	}))
	defer server.Close()

	client := newTestOneSignalClient(server.URL)
	for range 2 {
		result, err := client.Send(context.Background(), testPushNotification(), testOneSignalIdempotencyKey)

		require.NoError(t, err)
		assert.Equal(t, entity.PushDeliveryAccepted, result.Outcome)
		assert.Equal(t, "provider-notification-1", result.ProviderNotificationID)
		assert.Equal(t, http.StatusOK, result.HTTPStatus)
	}

	mu.Lock()
	defer mu.Unlock()

	require.Len(t, requests, 2)
	require.Len(t, notifications, 1, "the fake provider must create one notification for repeated idempotency keys")

	for _, request := range requests {
		assert.Equal(t, "app-id", request.AppID)
		assert.Equal(t, "push", request.TargetChannel)
		assert.Equal(t, []string{"user-one", "user-two"}, request.IncludeAliases.ExternalID)
		assert.Equal(t, testOneSignalIdempotencyKey, request.IdempotencyKey)
		assert.Equal(t, map[string]string{"en": "Read Quran"}, request.Contents)
	}
}

func TestOneSignalClientSendClassifiesProviderResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		retryAfter string
		wantReason string
		wantRetry  bool
		wantSystem bool
		wantAfter  time.Duration
		wantErr    bool
	}{
		{
			name:       "success without subscribers",
			statusCode: http.StatusOK,
			body:       `{"id":"","errors":["All included players are not subscribed"]}`,
			wantReason: "no_subscribers",
		},
		{
			name:       "success rejected by provider",
			statusCode: http.StatusOK,
			body:       `{"id":"","errors":{"invalid_aliases":{"external_id":["unknown"]}}}`,
			wantReason: "provider_rejected",
		},
		{
			name:       "bad request",
			statusCode: http.StatusBadRequest,
			body:       `{"errors":["invalid payload"]}`,
			wantReason: "invalid_request",
		},
		{
			name:       "unauthorized",
			statusCode: http.StatusUnauthorized,
			body:       `{"errors":["invalid REST API key"]}`,
			wantReason: "unauthorized",
			wantRetry:  true,
			wantSystem: true,
		},
		{
			name:       "forbidden",
			statusCode: http.StatusForbidden,
			body:       `{"errors":["forbidden"]}`,
			wantReason: "unauthorized",
			wantRetry:  true,
			wantSystem: true,
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{"errors":["slow down"]}`,
			retryAfter: "120",
			wantReason: "rate_limited",
			wantRetry:  true,
			wantSystem: true,
			wantAfter:  2 * time.Minute,
		},
		{
			name:       "provider server error",
			statusCode: http.StatusInternalServerError,
			body:       `{"errors":["temporary failure"]}`,
			wantReason: "provider_unavailable",
			wantRetry:  true,
			wantSystem: true,
		},
		{
			name:       "provider unavailable",
			statusCode: http.StatusServiceUnavailable,
			body:       `service unavailable`,
			wantReason: "provider_unavailable",
			wantRetry:  true,
			wantSystem: true,
		},
		{
			name:       "other permanent rejection",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"errors":["unprocessable"]}`,
			wantReason: "provider_rejected",
		},
		{
			name:       "malformed success body",
			statusCode: http.StatusOK,
			body:       `{`,
			wantReason: "provider_invalid_response",
			wantRetry:  true,
			wantSystem: true,
			wantErr:    true,
		},
		{
			name:       "success body missing outcome",
			statusCode: http.StatusOK,
			body:       `{}`,
			wantReason: "provider_invalid_response",
			wantRetry:  true,
			wantSystem: true,
			wantErr:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}

				w.WriteHeader(test.statusCode)
				_, err := w.Write([]byte(test.body))
				assert.NoError(t, err)
			}))
			defer server.Close()

			client := newTestOneSignalClient(server.URL)
			result, err := client.Send(context.Background(), testPushNotification(), testOneSignalIdempotencyKey)

			if test.wantErr {
				require.ErrorIs(t, err, entity.ErrPushDeliveryFailed)
			} else {
				require.NoError(t, err)
			}

			assert.Equal(t, entity.PushDeliveryFailed, result.Outcome)
			assert.Equal(t, test.statusCode, result.HTTPStatus)
			assert.Equal(t, test.wantReason, result.ReasonCode)
			assert.Equal(t, test.wantRetry, result.Retryable)
			assert.Equal(t, test.wantSystem, result.Systemic)
			assert.Equal(t, test.wantAfter, result.RetryAfter)
			assert.LessOrEqual(t, len(result.ReasonDetail), maxReasonDetailBytes)
		})
	}
}

func TestOneSignalClientSendClassifiesTransportFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		transport  error
		wantReason string
	}{
		{name: "timeout", transport: context.DeadlineExceeded, wantReason: "timeout"},
		{name: "network error", transport: errTestOneSignalNetwork, wantReason: "network_error"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			client := newTestOneSignalClient("https://onesignal.invalid")
			client.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, test.transport
			})}

			result, err := client.Send(context.Background(), testPushNotification(), testOneSignalIdempotencyKey)

			require.ErrorIs(t, err, entity.ErrPushDeliveryFailed)
			assert.Equal(t, entity.PushDeliveryFailed, result.Outcome)
			assert.Equal(t, test.wantReason, result.ReasonCode)
			assert.True(t, result.Retryable)
			assert.True(t, result.Systemic)
		})
	}
}

func TestOneSignalClientSendValidatesConfigurationAndInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		client         *OneSignalClient
		message        entity.PushNotification
		idempotencyKey string
		wantReason     string
		wantRetry      bool
		wantSystemic   bool
	}{
		{
			name:           "missing credentials",
			client:         NewOneSignalClient(OneSignalOptions{}),
			message:        testPushNotification(),
			idempotencyKey: testOneSignalIdempotencyKey,
			wantReason:     "invalid_configuration",
			wantRetry:      true,
			wantSystemic:   true,
		},
		{
			name:           "invalid UUID",
			client:         newTestOneSignalClient("https://onesignal.invalid"),
			message:        testPushNotification(),
			idempotencyKey: "not-a-uuid",
			wantReason:     "invalid_idempotency_key",
			wantSystemic:   true,
		},
		{
			name:           "nil UUID",
			client:         newTestOneSignalClient("https://onesignal.invalid"),
			message:        testPushNotification(),
			idempotencyKey: "00000000-0000-0000-0000-000000000000",
			wantReason:     "invalid_idempotency_key",
			wantSystemic:   true,
		},
		{
			name:           "non canonical UUID",
			client:         newTestOneSignalClient("https://onesignal.invalid"),
			message:        testPushNotification(),
			idempotencyKey: strings.ToUpper(testOneSignalIdempotencyKey),
			wantReason:     "invalid_idempotency_key",
			wantSystemic:   true,
		},
		{
			name:           "empty recipients",
			client:         newTestOneSignalClient("https://onesignal.invalid"),
			message:        entity.PushNotification{Contents: map[string]string{"en": "hello"}},
			idempotencyKey: testOneSignalIdempotencyKey,
			wantReason:     "invalid_request",
		},
		{
			name:           "empty contents",
			client:         newTestOneSignalClient("https://onesignal.invalid"),
			message:        entity.PushNotification{ExternalIDs: []string{"user"}},
			idempotencyKey: testOneSignalIdempotencyKey,
			wantReason:     "invalid_request",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := test.client.Send(
				context.Background(),
				test.message,
				test.idempotencyKey,
			)

			require.ErrorIs(t, err, entity.ErrPushDeliveryFailed)
			assert.Equal(t, entity.PushDeliveryFailed, result.Outcome)
			assert.Equal(t, test.wantReason, result.ReasonCode)
			assert.Equal(t, test.wantSystemic, result.Systemic)
			assert.Equal(t, test.wantRetry, result.Retryable)
		})
	}
}

func TestOneSignalClientSendRedactsAndBoundsReasonDetail(t *testing.T) {
	t.Parallel()

	const (
		apiKey     = "super-secret-api-key"
		externalID = "external-user-secret"
	)

	body := fmt.Sprintf(
		`{"errors":["key=%s user=%s %s"]}`,
		apiKey,
		externalID,
		strings.Repeat("é", maxReasonDetailBytes),
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, err := w.Write([]byte(body))
		assert.NoError(t, err)
	}))
	defer server.Close()

	client := NewOneSignalClient(OneSignalOptions{
		BaseURL:    server.URL,
		AppID:      "app-id",
		RESTAPIKey: apiKey,
		Timeout:    time.Second,
	})
	message := testPushNotification()
	message.ExternalIDs = []string{externalID, externalID, "", " "}

	result, err := client.Send(context.Background(), message, testOneSignalIdempotencyKey)

	require.NoError(t, err)
	assert.Equal(t, "invalid_request", result.ReasonCode)
	assert.NotContains(t, result.ReasonDetail, apiKey)
	assert.NotContains(t, result.ReasonDetail, externalID)
	assert.Contains(t, result.ReasonDetail, "[redacted]")
	assert.LessOrEqual(t, len(result.ReasonDetail), maxReasonDetailBytes)
	assert.True(t, utf8.ValidString(result.ReasonDetail))
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)

	assert.Equal(t, 90*time.Second, parseRetryAfter("90", now))
	assert.Equal(t, 2*time.Minute, parseRetryAfter("Wed, 15 Jul 2026 12:02:00 GMT", now))
	assert.Zero(t, parseRetryAfter("0", now))
	assert.Zero(t, parseRetryAfter("invalid", now))
}

func newTestOneSignalClient(baseURL string) *OneSignalClient {
	return NewOneSignalClient(OneSignalOptions{
		BaseURL:    baseURL,
		AppID:      "app-id",
		RESTAPIKey: "api-token",
		Timeout:    time.Second,
	})
}

func testPushNotification() entity.PushNotification {
	return entity.PushNotification{
		ExternalIDs: []string{"user-one", "user-one", " ", "user-two"},
		Headings:    map[string]string{"en": "Reminder"},
		Contents:    map[string]string{"en": "Read Quran"},
		Data:        map[string]string{"deep_link": "/quran"},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
