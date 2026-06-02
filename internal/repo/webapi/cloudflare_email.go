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
func (c *CloudflareEmailClient) Send(ctx context.Context, message entity.EmailMessage) error {
	if err := c.validate(); err != nil {
		return err
	}

	body, err := json.Marshal(c.newRequestBody(message))
	if err != nil {
		return fmt.Errorf("%w: CloudflareEmailClient - Send - Marshal: %w", entity.ErrEmailDeliveryFailed, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: CloudflareEmailClient - Send - NewRequest: %w", entity.ErrEmailDeliveryFailed, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: CloudflareEmailClient - Send - Do: %w", entity.ErrEmailDeliveryFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return c.statusError(resp)
	}

	var parsed cloudflareEmailResponse
	if err = json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("%w: CloudflareEmailClient - Send - Decode: %w", entity.ErrEmailDeliveryFailed, err)
	}
	if !parsed.Success {
		return fmt.Errorf("%w: CloudflareEmailClient - Send - unsuccessful response", entity.ErrEmailDeliveryFailed)
	}
	if parsed.Result == nil {
		return fmt.Errorf("%w: CloudflareEmailClient - Send - missing result", entity.ErrEmailDeliveryFailed)
	}
	if containsEmail(parsed.Result.PermanentBounces, message.To) {
		return fmt.Errorf(
			"%w: %w for %s",
			entity.ErrEmailDeliveryFailed,
			entity.ErrEmailPermanentBounce,
			message.To,
		)
	}

	return nil
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

	return body
}

func (c *CloudflareEmailClient) statusError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(body))
	if c.apiToken != "" {
		message = strings.ReplaceAll(message, c.apiToken, "[redacted]")
	}
	if message == "" {
		message = resp.Status
	}

	return fmt.Errorf("%w: CloudflareEmailClient status %d: %s", entity.ErrEmailDeliveryFailed, resp.StatusCode, message)
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
}

type cloudflareEmailResponse struct {
	Success bool `json:"success"`
	Result  *struct {
		Delivered        []string `json:"delivered"`
		PermanentBounces []string `json:"permanent_bounces"`
		Queued           []string `json:"queued"`
	} `json:"result"`
}
