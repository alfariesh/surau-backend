package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
)

const defaultCloudflareAPIBaseURL = "https://api.cloudflare.com/client/v4"

// CloudflareEmailOptions configures Cloudflare Email Service REST API access.
type CloudflareEmailOptions struct {
	BaseURL     string
	AccountID   string
	APIToken    string
	FromAddress string
	FromName    string
	ReplyTo     string
	Timeout     time.Duration
}

// CloudflareEmailClient sends transactional emails using Cloudflare Email Service.
type CloudflareEmailClient struct {
	baseURL     string
	accountID   string
	apiToken    string
	fromAddress string
	fromName    string
	replyTo     string
	httpClient  *http.Client
}

// NewCloudflareEmailClient creates a Cloudflare Email Service client.
func NewCloudflareEmailClient(opts CloudflareEmailOptions) *CloudflareEmailClient {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultCloudflareAPIBaseURL
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	return &CloudflareEmailClient{
		baseURL:     baseURL,
		accountID:   opts.AccountID,
		apiToken:    opts.APIToken,
		fromAddress: opts.FromAddress,
		fromName:    opts.FromName,
		replyTo:     opts.ReplyTo,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

// Send sends a single transactional email.
func (c *CloudflareEmailClient) Send(
	ctx context.Context,
	message entity.EmailMessage,
) (entity.EmailSendResult, error) {
	if err := c.validate(); err != nil {
		return entity.EmailSendResult{}, err
	}

	body, err := json.Marshal(c.newRequestBody(message))
	if err != nil {
		return entity.EmailSendResult{}, fmt.Errorf(
			"%w: CloudflareEmailClient - Send - Marshal: %w",
			entity.ErrEmailDeliveryFailed,
			err,
		)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return entity.EmailSendResult{}, fmt.Errorf(
			"%w: CloudflareEmailClient - Send - NewRequest: %w",
			entity.ErrEmailDeliveryFailed,
			err,
		)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return entity.EmailSendResult{}, fmt.Errorf(
			"%w: CloudflareEmailClient - Send - Do: %w",
			entity.ErrEmailDeliveryFailed,
			err,
		)
	}
	defer resp.Body.Close()

	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if readErr != nil {
		return entity.EmailSendResult{}, fmt.Errorf(
			"%w: CloudflareEmailClient - Send - ReadBody: %w",
			entity.ErrEmailDeliveryFailed,
			readErr,
		)
	}
	providerResponse := strings.TrimSpace(string(responseBody))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return entity.EmailSendResult{Provider: entity.EmailProviderCloudflare, ProviderResponse: providerResponse},
			c.statusError(resp.StatusCode, providerResponse)
	}

	var parsed cloudflareEmailResponse
	if err = json.Unmarshal(responseBody, &parsed); err != nil {
		return entity.EmailSendResult{
				Provider:         entity.EmailProviderCloudflare,
				ProviderResponse: providerResponse,
			}, fmt.Errorf(
				"%w: CloudflareEmailClient - Send - Decode: %w",
				entity.ErrEmailDeliveryFailed,
				err,
			)
	}
	result := entity.EmailSendResult{
		Provider:         entity.EmailProviderCloudflare,
		ProviderResponse: providerResponse,
	}
	if !parsed.Success {
		return result, fmt.Errorf(
			"%w: CloudflareEmailClient - Send - unsuccessful response",
			entity.ErrEmailDeliveryFailed,
		)
	}
	if parsed.Result == nil {
		return result, fmt.Errorf("%w: CloudflareEmailClient - Send - missing result", entity.ErrEmailDeliveryFailed)
	}
	result.Delivered = parsed.Result.Delivered
	result.Queued = parsed.Result.Queued
	result.PermanentBounces = parsed.Result.PermanentBounces
	if containsEmail(parsed.Result.PermanentBounces, message.To) {
		return result, fmt.Errorf(
			"%w: %w for %s",
			entity.ErrEmailDeliveryFailed,
			entity.ErrEmailPermanentBounce,
			message.To,
		)
	}

	return result, nil
}

func (c *CloudflareEmailClient) validate() error {
	if c.baseURL == "" || c.accountID == "" || c.apiToken == "" || c.fromAddress == "" {
		return entity.ErrEmailDeliveryFailed
	}

	return nil
}

func (c *CloudflareEmailClient) endpoint() string {
	return c.baseURL + "/accounts/" + url.PathEscape(c.accountID) + "/email/sending/send"
}

func (c *CloudflareEmailClient) newRequestBody(message entity.EmailMessage) cloudflareEmailRequest {
	body := cloudflareEmailRequest{
		To:      message.To,
		From:    cloudflareEmailAddress{Address: c.fromAddress, Name: c.fromName},
		Subject: message.Subject,
		HTML:    message.HTML,
		Text:    message.Text,
	}
	if c.replyTo != "" {
		body.ReplyTo = c.replyTo
	}
	body.Headers = emailHeaders(message)

	return body
}

func (c *CloudflareEmailClient) statusError(statusCode int, message string) error {
	message = strings.TrimSpace(message)
	if c.apiToken != "" {
		message = strings.ReplaceAll(message, c.apiToken, "[redacted]")
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return fmt.Errorf("%w: CloudflareEmailClient status %d: %s", entity.ErrEmailDeliveryFailed, statusCode, message)
}

func containsEmail(values []string, email string) bool {
	for _, value := range values {
		if strings.EqualFold(value, email) {
			return true
		}
	}

	return false
}

type cloudflareEmailAddress struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
}

type cloudflareEmailRequest struct {
	To      string                 `json:"to"`
	From    cloudflareEmailAddress `json:"from"`
	ReplyTo string                 `json:"reply_to,omitempty"`
	Subject string                 `json:"subject"`
	HTML    string                 `json:"html"`
	Text    string                 `json:"text"`
	Headers map[string]string      `json:"headers,omitempty"`
}

type cloudflareEmailResponse struct {
	Success bool `json:"success"`
	Result  *struct {
		Delivered        []string `json:"delivered"`
		PermanentBounces []string `json:"permanent_bounces"`
		Queued           []string `json:"queued"`
	} `json:"result"`
}

func trackingHeaders(message entity.EmailMessage) map[string]string {
	headers := map[string]string{}
	if strings.TrimSpace(message.MessageID) != "" {
		headers["X-Surau-Message-ID"] = strings.TrimSpace(message.MessageID)
	}
	if strings.TrimSpace(message.CampaignID) != "" {
		headers["X-Surau-Campaign-ID"] = strings.TrimSpace(message.CampaignID)
	}
	if strings.TrimSpace(message.CampaignRecipient) != "" {
		headers["X-Surau-Campaign-Recipient-ID"] = strings.TrimSpace(message.CampaignRecipient)
	}
	if len(headers) == 0 {
		return nil
	}

	return headers
}

func emailHeaders(message entity.EmailMessage) map[string]string {
	headers := map[string]string{}
	for name, value := range message.Headers {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if invalidEmailHeader(name, value) {
			continue
		}
		headers[name] = value
	}
	for name, value := range trackingHeaders(message) {
		headers[name] = value
	}
	if len(headers) == 0 {
		return nil
	}

	return headers
}

func invalidEmailHeader(name, value string) bool {
	return name == "" ||
		value == "" ||
		strings.ContainsAny(name, "\r\n") ||
		strings.ContainsAny(value, "\r\n")
}
