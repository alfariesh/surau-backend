package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudflareEmailEventsClient_PollCloudflareEmailEvents(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 6, 5, 1, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	var requestBody cloudflareGraphQLRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/graphql", r.URL.Path)
		assert.Equal(t, "Bearer analytics-token", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&requestBody))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"data":{
				"viewer":{
					"zones":[{
						"emailSendingAdaptive":[{
							"datetime":"2026-06-05T01:02:03Z",
							"from":"noreply@mail.surau.org",
							"to":"user@example.com",
							"subject":"Verify",
							"status":"deliveryFailed",
							"eventType":"smtp_delivery",
							"sendingDomain":"mail.surau.org",
							"messageId":"message-id",
							"errorCause":"bounce",
							"errorDetail":"550 5.1.1 user unknown"
						}]
					}]
				}
			}
		}`))
	}))
	defer server.Close()
	client := NewCloudflareEmailEventsClient(CloudflareEmailEventsOptions{
		BaseURL:  server.URL,
		APIToken: "analytics-token",
		Timeout:  2 * time.Second,
	})

	events, err := client.PollCloudflareEmailEvents(context.Background(), entity.CloudflareEmailEventPollQuery{
		ZoneID: "zone-id",
		Start:  start,
		End:    end,
		Limit:  25,
	})

	require.NoError(t, err)
	assert.Contains(t, requestBody.Query, "emailSendingAdaptive")
	assert.Contains(t, requestBody.Query, `status: "deliveryFailed"`)
	assert.Contains(t, requestBody.Query, "limit: 25")
	assert.Contains(t, requestBody.Query, "orderBy: [datetime_ASC]")
	assert.Equal(t, map[string]string{
		"zoneTag": "zone-id",
		"start":   "2026-06-05T01:00:00Z",
		"end":     "2026-06-05T01:30:00Z",
	}, requestBody.Variables)
	require.Len(t, events, 1)
	assert.Equal(t, "user@example.com", events[0].To)
	assert.Equal(t, "deliveryFailed", events[0].Status)
	assert.Equal(t, "message-id", events[0].MessageID)
	assert.Equal(t, "550 5.1.1 user unknown", events[0].ErrorDetail)
	assert.JSONEq(t, `{"datetime":"2026-06-05T01:02:03Z","from":"noreply@mail.surau.org","to":"user@example.com","subject":"Verify","status":"deliveryFailed","eventType":"smtp_delivery","sendingDomain":"mail.surau.org","messageId":"message-id","errorCause":"bounce","errorDetail":"550 5.1.1 user unknown"}`, string(events[0].RawPayload))
}

func TestCloudflareEmailEventsClient_PollCloudflareEmailEventsErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "graphql error",
			statusCode: http.StatusOK,
			body:       `{"errors":[{"message":"permission denied"}]}`,
		},
		{
			name:       "http error",
			statusCode: http.StatusForbidden,
			body:       `{"success":false,"errors":[{"message":"forbidden"}]}`,
		},
		{
			name:       "malformed json",
			statusCode: http.StatusOK,
			body:       `{`,
		},
		{
			name:       "invalid datetime",
			statusCode: http.StatusOK,
			body:       `{"data":{"viewer":{"zones":[{"emailSendingAdaptive":[{"datetime":"bad","to":"user@example.com","status":"deliveryFailed"}]}]}}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client := NewCloudflareEmailEventsClient(CloudflareEmailEventsOptions{
				BaseURL:  server.URL,
				APIToken: "analytics-token",
			})

			_, err := client.PollCloudflareEmailEvents(context.Background(), validCloudflareEmailEventPollQuery())

			require.True(t, errors.Is(err, entity.ErrEmailDeliveryFailed))
		})
	}
}

func TestCloudflareEmailEventsClient_PollCloudflareEmailEventsEmpty(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"viewer":{"zones":[{"emailSendingAdaptive":[]}]}}}`))
	}))
	defer server.Close()
	client := NewCloudflareEmailEventsClient(CloudflareEmailEventsOptions{
		BaseURL:  server.URL,
		APIToken: "analytics-token",
	})

	events, err := client.PollCloudflareEmailEvents(context.Background(), validCloudflareEmailEventPollQuery())

	require.NoError(t, err)
	assert.Empty(t, events)
}

func TestCloudflareEmailEventsClient_PollCloudflareEmailEventsMissingConfig(t *testing.T) {
	t.Parallel()

	client := NewCloudflareEmailEventsClient(CloudflareEmailEventsOptions{})

	_, err := client.PollCloudflareEmailEvents(context.Background(), validCloudflareEmailEventPollQuery())

	require.True(t, errors.Is(err, entity.ErrEmailDeliveryFailed))
}

func validCloudflareEmailEventPollQuery() entity.CloudflareEmailEventPollQuery {
	start := time.Date(2026, 6, 5, 1, 0, 0, 0, time.UTC)

	return entity.CloudflareEmailEventPollQuery{
		ZoneID: "zone-id",
		Start:  start,
		End:    start.Add(time.Minute),
		Limit:  100,
	}
}
