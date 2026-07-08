package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const cloudflareEmailDeliveryFailedStatus = "deliveryFailed"

// CloudflareEmailEventsOptions configures Cloudflare GraphQL Analytics email polling.
type CloudflareEmailEventsOptions struct {
	BaseURL  string
	APIToken string
	Timeout  time.Duration
}

// CloudflareEmailEventsClient reads Email Service events from Cloudflare GraphQL Analytics.
type CloudflareEmailEventsClient struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

// NewCloudflareEmailEventsClient creates a Cloudflare GraphQL Analytics client.
func NewCloudflareEmailEventsClient(opts CloudflareEmailEventsOptions) *CloudflareEmailEventsClient {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultCloudflareAPIBaseURL
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &CloudflareEmailEventsClient{
		baseURL:    baseURL,
		apiToken:   strings.TrimSpace(opts.APIToken),
		httpClient: &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
}

// PollCloudflareEmailEvents fetches outbound delivery failures for one zone and time window.
func (c *CloudflareEmailEventsClient) PollCloudflareEmailEvents(
	ctx context.Context,
	query entity.CloudflareEmailEventPollQuery,
) ([]entity.CloudflareEmailEvent, error) {
	if err := c.validateEmailEventsQuery(query); err != nil {
		return nil, err
	}
	graphqlQuery := cloudflareEmailEventsGraphQLQuery(query.Limit)
	body, err := json.Marshal(cloudflareGraphQLRequest{
		Query: graphqlQuery,
		Variables: map[string]string{
			"zoneTag": query.ZoneID,
			"start":   query.Start.UTC().Format(time.RFC3339),
			"end":     query.End.UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: CloudflareEmailEventsClient - Poll - Marshal: %w", entity.ErrEmailDeliveryFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: CloudflareEmailEventsClient - Poll - NewRequest: %w", entity.ErrEmailDeliveryFailed, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: CloudflareEmailEventsClient - Poll - Do: %w", entity.ErrEmailDeliveryFailed, err)
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 131072))
	if readErr != nil {
		return nil, fmt.Errorf("%w: CloudflareEmailEventsClient - Poll - ReadBody: %w", entity.ErrEmailDeliveryFailed, readErr)
	}
	providerResponse := strings.TrimSpace(string(responseBody))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, cloudflareStatusError(c.apiToken, resp.StatusCode, providerResponse)
	}

	var parsed cloudflareEmailEventsResponse
	if err = json.Unmarshal(responseBody, &parsed); err != nil {
		return nil, fmt.Errorf("%w: CloudflareEmailEventsClient - Poll - Decode: %w", entity.ErrEmailDeliveryFailed, err)
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf(
			"%w: CloudflareEmailEventsClient - Poll - GraphQL: %s",
			entity.ErrEmailDeliveryFailed,
			parsed.Errors[0].Message,
		)
	}

	return parsed.emailEvents()
}

func (c *CloudflareEmailEventsClient) validateEmailEventsQuery(query entity.CloudflareEmailEventPollQuery) error {
	if c.baseURL == "" || c.apiToken == "" || strings.TrimSpace(query.ZoneID) == "" {
		return entity.ErrEmailDeliveryFailed
	}
	if query.Start.IsZero() || query.End.IsZero() || !query.Start.Before(query.End) || query.Limit <= 0 {
		return entity.ErrEmailDeliveryFailed
	}

	return nil
}

func (c *CloudflareEmailEventsClient) endpoint() string {
	return c.baseURL + "/graphql"
}

func cloudflareEmailEventsGraphQLQuery(limit int) string {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	return fmt.Sprintf(`
query CloudflareEmailSendingEvents($zoneTag: string!, $start: Time!, $end: Time!) {
  viewer {
    zones(filter: { zoneTag: $zoneTag }) {
      emailSendingAdaptive(
        filter: { datetime_geq: $start, datetime_leq: $end, status: "deliveryFailed" }
        limit: %d
        orderBy: [datetime_ASC]
      ) {
        datetime
        from
        to
        subject
        status
        eventType
        sendingDomain
        messageId
        errorCause
        errorDetail
      }
    }
  }
}`, limit)
}

func cloudflareStatusError(apiToken string, statusCode int, message string) error {
	message = strings.TrimSpace(message)
	if apiToken != "" {
		message = strings.ReplaceAll(message, apiToken, "[redacted]")
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return fmt.Errorf("%w: Cloudflare status %d: %s", entity.ErrEmailDeliveryFailed, statusCode, message)
}

type cloudflareGraphQLRequest struct {
	Query     string            `json:"query"`
	Variables map[string]string `json:"variables"`
}

type cloudflareEmailEventsResponse struct {
	Data struct {
		Viewer struct {
			Zones []struct {
				EmailSendingAdaptive []cloudflareEmailEventRow `json:"emailSendingAdaptive"`
			} `json:"zones"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type cloudflareEmailEventRow struct {
	Datetime      string `json:"datetime"`
	From          string `json:"from"`
	To            string `json:"to"`
	Subject       string `json:"subject"`
	Status        string `json:"status"`
	EventType     string `json:"eventType"`
	SendingDomain string `json:"sendingDomain"`
	MessageID     string `json:"messageId"`
	ErrorCause    string `json:"errorCause"`
	ErrorDetail   string `json:"errorDetail"`
}

func (r cloudflareEmailEventsResponse) emailEvents() ([]entity.CloudflareEmailEvent, error) {
	events := make([]entity.CloudflareEmailEvent, 0)
	for _, zone := range r.Data.Viewer.Zones {
		for _, row := range zone.EmailSendingAdaptive {
			event, err := row.emailEvent()
			if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
	}

	return events, nil
}

func (r cloudflareEmailEventRow) emailEvent() (entity.CloudflareEmailEvent, error) {
	occurredAt, err := time.Parse(time.RFC3339, strings.TrimSpace(r.Datetime))
	if err != nil {
		return entity.CloudflareEmailEvent{}, fmt.Errorf(
			"%w: CloudflareEmailEventsClient - Poll - invalid datetime: %w",
			entity.ErrEmailDeliveryFailed,
			err,
		)
	}
	rawPayload, _ := json.Marshal(r)

	return entity.CloudflareEmailEvent{
		Datetime:      occurredAt,
		From:          strings.TrimSpace(r.From),
		To:            strings.TrimSpace(r.To),
		Subject:       strings.TrimSpace(r.Subject),
		Status:        strings.TrimSpace(r.Status),
		EventType:     strings.TrimSpace(r.EventType),
		SendingDomain: strings.TrimSpace(r.SendingDomain),
		MessageID:     strings.TrimSpace(r.MessageID),
		ErrorCause:    strings.TrimSpace(r.ErrorCause),
		ErrorDetail:   strings.TrimSpace(r.ErrorDetail),
		RawPayload:    entity.RawJSON(rawPayload),
	}, nil
}
