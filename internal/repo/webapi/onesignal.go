package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	defaultOneSignalBaseURL = "https://api.onesignal.com"
	defaultOneSignalTimeout = 10 * time.Second
	maxOneSignalBodyBytes   = 65536
	maxReasonDetailBytes    = 2000
)

var (
	errOneSignalMissingOutcome     = errors.New("missing notification id and errors")
	errOneSignalIncompleteConfig   = errors.New("incomplete OneSignal configuration")
	errOneSignalInvalidIdempotency = errors.New("invalid idempotency key")
	errOneSignalEmptyPush          = errors.New("empty push recipients or contents")
)

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
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultOneSignalBaseURL
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultOneSignalTimeout
	}

	return &OneSignalClient{
		baseURL:    baseURL,
		appID:      strings.TrimSpace(opts.AppID),
		restAPIKey: strings.TrimSpace(opts.RESTAPIKey),
		httpClient: &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}
}

// Send delivers one push to the given users, targeted by their external_id alias. Every retry of
// the same logical delivery must pass the exact same canonical UUID as idempotencyKey.
func (c *OneSignalClient) Send(
	ctx context.Context,
	message entity.PushNotification,
	idempotencyKey string,
) (entity.PushDeliveryResult, error) {
	externalIDs := dedupeNonEmpty(message.ExternalIDs)

	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if result, err := c.validate(message, externalIDs, idempotencyKey); err != nil {
		return result, err
	}

	req, result, err := c.newSendRequest(ctx, message, externalIDs, idempotencyKey)
	if err != nil {
		return result, err
	}

	return c.executeSendRequest(req, externalIDs)
}

func (c *OneSignalClient) newSendRequest(
	ctx context.Context,
	message entity.PushNotification,
	externalIDs []string,
	idempotencyKey string,
) (*http.Request, entity.PushDeliveryResult, error) {
	body, err := json.Marshal(oneSignalRequest{
		AppID:          c.appID,
		TargetChannel:  "push",
		IncludeAliases: oneSignalAliases{ExternalID: externalIDs},
		Headings:       nonEmptyMap(message.Headings),
		Contents:       message.Contents,
		Data:           nonEmptyMap(message.Data),
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		result := failedPushResult(0, "invalid_request", "could not encode OneSignal request", false, true, 0)

		return nil, result, wrapPushDeliveryError("Marshal", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		result := failedPushResult(0, "invalid_configuration", "invalid OneSignal endpoint", true, true, 0)

		return nil, result, wrapPushDeliveryError("NewRequest", err)
	}

	// OneSignal v5 REST API keys use the "Key" auth scheme. Legacy keys used "Basic <key>".
	req.Header.Set("Authorization", "Key "+c.restAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	return req, entity.PushDeliveryResult{}, nil
}

func (c *OneSignalClient) executeSendRequest(
	req *http.Request,
	externalIDs []string,
) (entity.PushDeliveryResult, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		reasonCode := "network_error"
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			reasonCode = "timeout"
		}

		result := failedPushResult(
			0,
			reasonCode,
			c.sanitizeReason(err.Error(), externalIDs),
			true,
			true,
			0,
		)

		return result, wrapPushDeliveryError("Do", err)
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, maxOneSignalBodyBytes))
	if readErr != nil {
		result := failedPushResult(
			resp.StatusCode,
			"provider_invalid_response",
			c.sanitizeReason(readErr.Error(), externalIDs),
			true,
			true,
			0,
		)

		return result, wrapPushDeliveryError("ReadBody", readErr)
	}

	providerResponse := c.sanitizeReason(strings.TrimSpace(string(responseBody)), externalIDs)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return c.statusResult(resp, providerResponse), nil
	}

	return c.parseSuccessResponse(resp.StatusCode, responseBody, providerResponse, externalIDs)
}

func (c *OneSignalClient) parseSuccessResponse(
	statusCode int,
	responseBody []byte,
	providerResponse string,
	externalIDs []string,
) (entity.PushDeliveryResult, error) {
	var parsed oneSignalResponse
	if err := json.Unmarshal(responseBody, &parsed); err != nil {
		result := failedPushResult(
			statusCode,
			"provider_invalid_response",
			providerResponse,
			true,
			true,
			0,
		)

		return result, wrapPushDeliveryError("Decode", err)
	}

	if strings.TrimSpace(parsed.ID) != "" {
		return entity.PushDeliveryResult{
			Outcome:                entity.PushDeliveryAccepted,
			ProviderNotificationID: strings.TrimSpace(parsed.ID),
			HTTPStatus:             statusCode,
		}, nil
	}

	errorDetail := c.sanitizeReason(oneSignalErrorDetail(parsed.Errors), externalIDs)
	if errorDetail == "" {
		result := failedPushResult(
			statusCode,
			"provider_invalid_response",
			providerResponse,
			true,
			true,
			0,
		)

		return result, wrapPushDeliveryError("Decode", errOneSignalMissingOutcome)
	}

	reasonCode := "provider_rejected"
	if isNoSubscribersReason(errorDetail) {
		reasonCode = "no_subscribers"
	}

	return failedPushResult(statusCode, reasonCode, errorDetail, false, false, 0), nil
}

func (c *OneSignalClient) validate(
	message entity.PushNotification,
	externalIDs []string,
	idempotencyKey string,
) (entity.PushDeliveryResult, error) {
	if c.baseURL == "" || c.appID == "" || c.restAPIKey == "" || c.httpClient == nil {
		result := failedPushResult(
			0,
			"invalid_configuration",
			"OneSignal endpoint, app ID, REST API key, and HTTP client are required",
			true,
			true,
			0,
		)

		return result, wrapPushDeliveryError("Validate", errOneSignalIncompleteConfig)
	}

	parsedKey, err := uuid.Parse(idempotencyKey)
	if err != nil || parsedKey == uuid.Nil || parsedKey.String() != idempotencyKey {
		result := failedPushResult(
			0,
			"invalid_idempotency_key",
			"idempotency key must be a canonical non-nil UUID",
			false,
			true,
			0,
		)

		return result, wrapPushDeliveryError("Validate", errOneSignalInvalidIdempotency)
	}

	if len(externalIDs) == 0 || len(message.Contents) == 0 {
		result := failedPushResult(
			0,
			"invalid_request",
			"OneSignal push requires at least one external ID and one content translation",
			false,
			false,
			0,
		)

		return result, wrapPushDeliveryError("Validate", errOneSignalEmptyPush)
	}

	return entity.PushDeliveryResult{}, nil
}

func (c *OneSignalClient) endpoint() string {
	return c.baseURL + "/notifications"
}

func (c *OneSignalClient) statusResult(resp *http.Response, detail string) entity.PushDeliveryResult {
	if detail == "" {
		detail = http.StatusText(resp.StatusCode)
	}

	switch {
	case resp.StatusCode == http.StatusBadRequest:
		return failedPushResult(resp.StatusCode, "invalid_request", detail, false, false, 0)
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return failedPushResult(resp.StatusCode, "unauthorized", detail, true, true, 0)
	case resp.StatusCode == http.StatusTooManyRequests:
		return failedPushResult(
			resp.StatusCode,
			"rate_limited",
			detail,
			true,
			true,
			parseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
		)
	case resp.StatusCode >= http.StatusInternalServerError:
		return failedPushResult(resp.StatusCode, "provider_unavailable", detail, true, true, 0)
	default:
		return failedPushResult(resp.StatusCode, "provider_rejected", detail, false, false, 0)
	}
}

func (c *OneSignalClient) sanitizeReason(detail string, externalIDs []string) string {
	detail = strings.TrimSpace(detail)

	secrets := append([]string(nil), externalIDs...)
	if c.restAPIKey != "" {
		secrets = append(secrets, c.restAPIKey)
	}

	sort.Slice(secrets, func(i, j int) bool {
		return len(secrets[i]) > len(secrets[j])
	})

	for _, secret := range secrets {
		if secret != "" {
			detail = strings.ReplaceAll(detail, secret, "[redacted]")
		}
	}

	return truncateUTF8(detail, maxReasonDetailBytes)
}

func failedPushResult(
	httpStatus int,
	reasonCode string,
	reasonDetail string,
	retryable bool,
	systemic bool,
	retryAfter time.Duration,
) entity.PushDeliveryResult {
	return entity.PushDeliveryResult{
		Outcome:      entity.PushDeliveryFailed,
		HTTPStatus:   httpStatus,
		ReasonCode:   reasonCode,
		ReasonDetail: truncateUTF8(strings.TrimSpace(reasonDetail), maxReasonDetailBytes),
		Retryable:    retryable,
		Systemic:     systemic,
		RetryAfter:   retryAfter,
	}
}

func wrapPushDeliveryError(operation string, err error) error {
	return fmt.Errorf("%w: OneSignalClient - Send - %s: %w", entity.ErrPushDeliveryFailed, operation, err)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	seconds, err := strconv.ParseInt(value, 10, 64)
	if err == nil {
		if seconds <= 0 {
			return 0
		}

		return time.Duration(seconds) * time.Second
	}

	retryAt, err := http.ParseTime(value)
	if err != nil || !retryAt.After(now) {
		return 0
	}

	return retryAt.Sub(now)
}

func oneSignalErrorDetail(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}

	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return ""
		}

		return string(encoded)
	}
}

func isNoSubscribersReason(detail string) bool {
	detail = strings.ToLower(detail)

	return strings.Contains(detail, "not subscribed") ||
		strings.Contains(detail, "no subscribed") ||
		strings.Contains(detail, "no subscriber") ||
		strings.Contains(detail, "no recipient")
}

func isTimeout(err error) bool {
	type timeoutError interface {
		Timeout() bool
	}

	var timeoutErr timeoutError

	return errors.As(err, &timeoutErr) && timeoutErr.Timeout()
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 || value == "" {
		return ""
	}

	if len(value) <= limit {
		return value
	}

	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}

	return value
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
	IdempotencyKey string            `json:"idempotency_key"`
}

type oneSignalResponse struct {
	ID     string          `json:"id"`
	Errors json.RawMessage `json:"errors"`
}
