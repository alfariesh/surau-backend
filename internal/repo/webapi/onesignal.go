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

	"github.com/evrone/go-clean-template/internal/entity"
)

const defaultOneSignalBaseURL = "https://api.onesignal.com"

// OneSignalOptions configures OneSignal REST API access.
type OneSignalOptions struct {
	BaseURL    string
	AppID      string
	RESTAPIKey string
	Timeout    time.Duration
}

// OneSignalClient delivers push notifications through the OneSignal REST API.
type OneSignalClient struct {
	baseURL    string
	appID      string
	restAPIKey string
	httpClient *http.Client
}

// NewOneSignalClient creates a OneSignal REST API client.
func NewOneSignalClient(opts OneSignalOptions) *OneSignalClient {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultOneSignalBaseURL
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &OneSignalClient{
		baseURL:    baseURL,
		appID:      opts.AppID,
		restAPIKey: opts.RESTAPIKey,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Send delivers one push to the given users, targeted by their external_id alias. It is a no-op
// when there are no recipients or no content to send.
func (c *OneSignalClient) Send(ctx context.Context, message entity.PushNotification) error {
	if err := c.validate(); err != nil {
		return err
	}

	externalIDs := dedupeNonEmpty(message.ExternalIDs)
	if len(externalIDs) == 0 || len(message.Contents) == 0 {
		return nil
	}

	body, err := json.Marshal(oneSignalRequest{
		AppID:          c.appID,
		TargetChannel:  "push",
		IncludeAliases: oneSignalAliases{ExternalID: externalIDs},
		Headings:       nonEmptyMap(message.Headings),
		Contents:       message.Contents,
		Data:           nonEmptyMap(message.Data),
	})
	if err != nil {
		return fmt.Errorf("%w: OneSignalClient - Send - Marshal: %w", entity.ErrPushDeliveryFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: OneSignalClient - Send - NewRequest: %w", entity.ErrPushDeliveryFailed, err)
	}
	// OneSignal v5 REST API keys use the "Key" auth scheme. Legacy keys used "Basic <key>" — switch
	// the prefix here if an older key is configured.
	req.Header.Set("Authorization", "Key "+c.restAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: OneSignalClient - Send - Do: %w", entity.ErrPushDeliveryFailed, err)
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if readErr != nil {
		return fmt.Errorf("%w: OneSignalClient - Send - ReadBody: %w", entity.ErrPushDeliveryFailed, readErr)
	}
	providerResponse := strings.TrimSpace(string(responseBody))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return c.statusError(resp.StatusCode, providerResponse)
	}

	return nil
}

func (c *OneSignalClient) validate() error {
	if c.baseURL == "" || c.appID == "" || c.restAPIKey == "" {
		return entity.ErrPushDeliveryFailed
	}

	return nil
}

func (c *OneSignalClient) endpoint() string {
	return c.baseURL + "/notifications"
}

func (c *OneSignalClient) statusError(statusCode int, message string) error {
	message = strings.TrimSpace(message)
	if c.restAPIKey != "" {
		message = strings.ReplaceAll(message, c.restAPIKey, "[redacted]")
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return fmt.Errorf("%w: OneSignalClient status %d: %s", entity.ErrPushDeliveryFailed, statusCode, message)
}

func dedupeNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}

	return result
}

func nonEmptyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}

	return m
}

type oneSignalAliases struct {
	ExternalID []string `json:"external_id"`
}

type oneSignalRequest struct {
	AppID          string            `json:"app_id"`
	TargetChannel  string            `json:"target_channel"`
	IncludeAliases oneSignalAliases  `json:"include_aliases"`
	Headings       map[string]string `json:"headings,omitempty"`
	Contents       map[string]string `json:"contents"`
	Data           map[string]string `json:"data,omitempty"`
}
