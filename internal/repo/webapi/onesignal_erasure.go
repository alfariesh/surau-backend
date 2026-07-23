package webapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/google/uuid"
)

var errOneSignalInvalidExternalID = errors.New("invalid OneSignal external id")

var (
	errOneSignalErasureRequest  = errors.New("OneSignal erasure request failed")
	errOneSignalErasureResponse = errors.New("OneSignal erasure response could not be read")
)

// OneSignalErasureClient deletes users and verifies asynchronous provider erasure.
type OneSignalErasureClient struct {
	client *OneSignalClient
}

func NewOneSignalErasureClient(opts OneSignalOptions) *OneSignalErasureClient {
	return &OneSignalErasureClient{client: NewOneSignalClient(opts)}
}

func (c *OneSignalErasureClient) DeleteUser(
	ctx context.Context,
	externalID string,
) (entity.OneSignalErasureProviderResult, error) {
	return c.request(ctx, http.MethodDelete, externalID)
}

func (c *OneSignalErasureClient) ViewUser(
	ctx context.Context,
	externalID string,
) (entity.OneSignalErasureProviderResult, error) {
	return c.request(ctx, http.MethodGet, externalID)
}

func (c *OneSignalErasureClient) request(
	ctx context.Context,
	method,
	externalID string,
) (entity.OneSignalErasureProviderResult, error) {
	externalID = strings.TrimSpace(externalID)

	endpoint, err := c.endpoint(externalID)
	if errors.Is(err, errOneSignalInvalidExternalID) {
		return erasureFailure(0, "invalid_external_id", "external id must be a canonical UUID", false, false, 0),
			err
	}

	if err != nil {
		return erasureFailure(0, "invalid_configuration", "OneSignal erasure client is not configured", true, true, 0),
			err
	}

	request, err := http.NewRequestWithContext(ctx, method, endpoint, http.NoBody)
	if err != nil {
		return erasureFailure(0, "invalid_configuration", "invalid OneSignal erasure endpoint", true, true, 0),
			errOneSignalIncompleteConfig
	}

	request.Header.Set("Authorization", "Key "+c.client.restAPIKey)
	request.Header.Set("Accept", "application/json")

	response, err := c.client.httpClient.Do(request)
	if err != nil {
		return c.transportFailure(err, externalID)
	}
	defer response.Body.Close()

	return c.responseResult(method, externalID, response)
}

func (c *OneSignalErasureClient) endpoint(externalID string) (string, error) {
	if c == nil || c.client == nil || c.client.baseURL == "" ||
		c.client.appID == "" || c.client.restAPIKey == "" || c.client.httpClient == nil {
		return "", errOneSignalIncompleteConfig
	}

	parsedID, err := uuid.Parse(externalID)
	if err != nil || parsedID == uuid.Nil || parsedID.String() != externalID {
		return "", errOneSignalInvalidExternalID
	}

	return fmt.Sprintf(
		"%s/apps/%s/users/by/external_id/%s",
		c.client.baseURL,
		url.PathEscape(c.client.appID),
		url.PathEscape(externalID),
	), nil
}

func (c *OneSignalErasureClient) responseResult(
	method,
	externalID string,
	response *http.Response,
) (entity.OneSignalErasureProviderResult, error) {
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxOneSignalBodyBytes))
	if readErr != nil {
		return erasureFailure(
			response.StatusCode,
			"provider_invalid_response",
			c.client.sanitizeReason(readErr.Error(), []string{externalID}),
			true,
			true,
			0,
		), errOneSignalErasureResponse
	}

	detail := c.client.sanitizeReason(strings.TrimSpace(string(body)), []string{externalID})

	return classifyErasureResponse(method, response, detail), nil
}

func (c *OneSignalErasureClient) transportFailure(
	err error,
	externalID string,
) (entity.OneSignalErasureProviderResult, error) {
	reasonCode := "network_error"
	if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
		reasonCode = "timeout"
	}

	return erasureFailure(
		0,
		reasonCode,
		c.client.sanitizeReason(err.Error(), []string{externalID}),
		true,
		true,
		0,
	), errOneSignalErasureRequest
}

func classifyErasureResponse(
	method string,
	response *http.Response,
	detail string,
) entity.OneSignalErasureProviderResult {
	switch {
	case response.StatusCode == http.StatusNotFound:
		return entity.OneSignalErasureProviderResult{
			HTTPStatus: response.StatusCode, ReasonCode: "not_found",
			ReasonDetail: detail, NotFound: true,
		}
	case response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices:
		return entity.OneSignalErasureProviderResult{
			HTTPStatus: response.StatusCode, ReasonCode: erasureSuccessReason(method),
			ReasonDetail: detail, Accepted: true,
		}
	case response.StatusCode == http.StatusBadRequest:
		return erasureFailure(response.StatusCode, "invalid_request", detail, false, false, 0)
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return erasureFailure(response.StatusCode, "unauthorized", detail, true, true, 0)
	case response.StatusCode == http.StatusTooManyRequests:
		return erasureFailure(
			response.StatusCode,
			"rate_limited",
			detail,
			true,
			true,
			parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
		)
	case response.StatusCode >= http.StatusInternalServerError:
		return erasureFailure(response.StatusCode, "provider_unavailable", detail, true, true, 0)
	default:
		return erasureFailure(response.StatusCode, "provider_rejected", detail, false, false, 0)
	}
}

func erasureSuccessReason(method string) string {
	if method == http.MethodGet {
		return "user_exists"
	}

	return "delete_accepted"
}

func erasureFailure(
	httpStatus int,
	reasonCode,
	reasonDetail string,
	retryable,
	systemic bool,
	retryAfter time.Duration,
) entity.OneSignalErasureProviderResult {
	return entity.OneSignalErasureProviderResult{
		HTTPStatus: httpStatus, ReasonCode: reasonCode,
		ReasonDetail: truncateUTF8(strings.TrimSpace(reasonDetail), maxReasonDetailBytes),
		Retryable:    retryable, Systemic: systemic, RetryAfter: retryAfter,
	}
}
