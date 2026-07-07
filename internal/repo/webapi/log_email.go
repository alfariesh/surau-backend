package webapi

import (
	"context"
	"log"
	"strings"
	"unicode"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// LogEmailSender writes transactional email details to stdout for local development.
type LogEmailSender struct{}

// NewLogEmailSender creates an email sender that does not call an external provider.
func NewLogEmailSender() *LogEmailSender {
	return &LogEmailSender{}
}

// Send logs the email recipient, subject, and first detected link.
func (s *LogEmailSender) Send(ctx context.Context, message entity.EmailMessage) (entity.EmailSendResult, error) {
	if err := ctx.Err(); err != nil {
		return entity.EmailSendResult{}, err
	}

	link := firstHTTPLink(message.Text)
	if link == "" {
		link = firstHTTPLink(message.HTML)
	}

	log.Printf(
		"DEV_EMAIL to=%q subject=%q link=%q",
		message.To,
		message.Subject,
		link,
	)

	return entity.EmailSendResult{
		Provider:         entity.EmailProviderLog,
		Delivered:        []string{message.To},
		ProviderResponse: "log",
	}, nil
}

func firstHTTPLink(text string) string {
	for _, field := range strings.FieldsFunc(text, isEmailLogLinkSeparator) {
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			return field
		}
	}

	return ""
}

func isEmailLogLinkSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune("<>\"'()", r)
}
