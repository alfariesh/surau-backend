package webapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloudflareEmailClient_Send(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantErr    bool
	}{
		{
			name:       "delivered",
			statusCode: http.StatusOK,
			body:       `{"success":true,"result":{"delivered":["user@example.com"],"permanent_bounces":[],"queued":[]}}`,
		},
		{
			name:       "queued",
			statusCode: http.StatusOK,
			body:       `{"success":true,"result":{"delivered":[],"permanent_bounces":[],"queued":["user@example.com"]}}`,
		},
		{
			name:       "permanent bounce",
			statusCode: http.StatusOK,
			body:       `{"success":true,"result":{"delivered":[],"permanent_bounces":["user@example.com"],"queued":[]}}`,
			wantErr:    true,
		},
		{
			name:       "bad request",
			statusCode: http.StatusBadRequest,
			body:       `{"success":false,"errors":[{"code":10001,"message":"email.sending.error.invalid_request_schema"}]}`,
			wantErr:    true,
		},
		{
			name:       "rate limited",
			statusCode: http.StatusTooManyRequests,
			body:       `{"success":false}`,
			wantErr:    true,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			body:       `{"success":false}`,
			wantErr:    true,
		},
		{
			name:       "malformed response",
			statusCode: http.StatusOK,
			body:       `{`,
			wantErr:    true,
		},
		{
			name:       "accepted with empty recipient status",
			statusCode: http.StatusOK,
			body:       `{"success":true,"result":{"delivered":[],"permanent_bounces":[],"queued":[]}}`,
		},
		{
			name:       "missing result",
			statusCode: http.StatusOK,
			body:       `{"success":true}`,
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/accounts/account-id/email/sending/send", r.URL.Path)
				assert.Equal(t, "Bearer api-token", r.Header.Get("Authorization"))
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := newTestCloudflareEmailClient(server.URL, 2*time.Second)

			err := client.Send(context.Background(), testEmailMessage())

			if tc.wantErr {
				require.ErrorIs(t, err, entity.ErrEmailDeliveryFailed)

				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCloudflareEmailClient_SendTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newTestCloudflareEmailClient(server.URL, time.Nanosecond)

	err := client.Send(context.Background(), testEmailMessage())

	require.ErrorIs(t, err, entity.ErrEmailDeliveryFailed)
}

func TestCloudflareEmailClient_SendMissingConfig(t *testing.T) {
	t.Parallel()

	client := NewCloudflareEmailClient(CloudflareEmailOptions{})

	err := client.Send(context.Background(), testEmailMessage())

	require.True(t, errors.Is(err, entity.ErrEmailDeliveryFailed))
}

func newTestCloudflareEmailClient(baseURL string, timeout time.Duration) *CloudflareEmailClient {
	return NewCloudflareEmailClient(CloudflareEmailOptions{
		BaseURL:     baseURL,
		AccountID:   "account-id",
		APIToken:    "api-token",
		FromAddress: "noreply@example.com",
		FromName:    "Surau",
		Timeout:     timeout,
	})
}

func testEmailMessage() entity.EmailMessage {
	return entity.EmailMessage{
		To:      "user@example.com",
		Subject: "Verify",
		HTML:    "<p>Verify</p>",
		Text:    "Verify",
	}
}
