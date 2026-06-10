package user

import (
	"context"
	"fmt"
	"html"
	"strconv"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
)

const (
	refreshReuseAlertScanLimit = 200
	adminRecipientLookupLimit  = 200
	refreshReuseAlertMaxRows   = 20
)

// AlertRefreshReuse scans the audit log for new refresh-token-reuse events
// since the last run and, when any are found, emails the configured admins a
// digest. It is invoked by the app's periodic alert job and returns the number
// of new events handled.
//
// The watermark is in-memory and seeded at boot, so a redeploy resets it to
// "now": events that occur while the process is down are not emailed (they stay
// in auth_audit_logs as the durable record). This keeps the feature dependency
// free; persisting the watermark is a straightforward follow-up if needed.
func (uc *UseCase) AlertRefreshReuse(ctx context.Context) (int, error) {
	if !uc.alert.Enabled || uc.auditLogger == nil || uc.emailSender == nil {
		return 0, nil
	}

	uc.alertMu.Lock()
	watermark := uc.alertWatermark
	uc.alertMu.Unlock()

	events, err := uc.auditLogger.ListAuthAuditEventsSince(
		ctx,
		authEventRefreshReuse,
		watermark,
		refreshReuseAlertScanLimit,
	)
	if err != nil {
		return 0, fmt.Errorf("UserUseCase - AlertRefreshReuse - ListAuthAuditEventsSince: %w", err)
	}
	if len(events) == 0 {
		return 0, nil
	}

	newest := watermark
	for _, event := range events {
		if event.CreatedAt.After(newest) {
			newest = event.CreatedAt
		}
	}

	recipients, err := uc.alertRecipients(ctx)
	if err != nil {
		return 0, fmt.Errorf("UserUseCase - AlertRefreshReuse - alertRecipients: %w", err)
	}
	if len(recipients) == 0 {
		// Nobody to notify — advance the watermark so we don't rescan forever.
		uc.advanceAlertWatermark(newest)

		return len(events), nil
	}

	subject, htmlBody, textBody := buildRefreshReuseAlert(events)
	sentAtLeastOne := false
	var sendErr error
	for _, to := range recipients {
		if _, err := uc.emailSender.Send(ctx, entity.EmailMessage{
			To:       to,
			Subject:  subject,
			HTML:     htmlBody,
			Text:     textBody,
			Category: "security_alert",
			Critical: true,
		}); err != nil {
			sendErr = err

			continue
		}
		sentAtLeastOne = true
	}

	// Advance once at least one admin was reached so a single flaky recipient
	// cannot cause the whole digest to resend every interval.
	if sentAtLeastOne {
		uc.advanceAlertWatermark(newest)
	}
	if sendErr != nil {
		return len(events), fmt.Errorf("UserUseCase - AlertRefreshReuse - Send: %w", sendErr)
	}

	return len(events), nil
}

func (uc *UseCase) advanceAlertWatermark(t time.Time) {
	uc.alertMu.Lock()
	if t.After(uc.alertWatermark) {
		uc.alertWatermark = t
	}
	uc.alertMu.Unlock()
}

// alertRecipients returns the explicit recipient list, or every admin user's
// email when no recipients are configured.
func (uc *UseCase) alertRecipients(ctx context.Context) ([]string, error) {
	if len(uc.alert.Recipients) > 0 {
		return uc.alert.Recipients, nil
	}
	if uc.repo == nil {
		return nil, nil
	}

	accounts, _, err := uc.repo.ListAccounts(ctx, repo.UserFilter{
		Role:  entity.UserRoleAdmin,
		Limit: adminRecipientLookupLimit,
	})
	if err != nil {
		return nil, err
	}

	emails := make([]string, 0, len(accounts))
	for _, account := range accounts {
		if email := strings.TrimSpace(account.Email); email != "" {
			emails = append(emails, email)
		}
	}

	return emails, nil
}

// buildRefreshReuseAlert composes the admin digest email for a batch of
// refresh-token-reuse events.
func buildRefreshReuseAlert(events []entity.AuthAuditLog) (subject, htmlBody, textBody string) {
	subject = fmt.Sprintf("[Surau security] %s refresh-token reuse detected", strconv.Itoa(len(events)))

	var text strings.Builder
	var rows strings.Builder
	text.WriteString(fmt.Sprintf(
		"%d refresh-token reuse event(s) were detected. Each is a strong signal of a stolen refresh token; the affected session family was revoked automatically.\n\n",
		len(events),
	))

	shown := events
	truncated := 0
	if len(shown) > refreshReuseAlertMaxRows {
		truncated = len(shown) - refreshReuseAlertMaxRows
		shown = shown[:refreshReuseAlertMaxRows]
	}

	for _, event := range shown {
		ts := event.CreatedAt.UTC().Format(time.RFC3339)
		familyID := event.Metadata["family_id"]
		revoked := event.Metadata["revoked_sessions"]

		text.WriteString(fmt.Sprintf(
			"- %s | user=%s | ip=%s | family=%s | revoked_sessions=%s\n",
			ts, fallbackDash(event.UserID), fallbackDash(event.ClientIP), fallbackDash(familyID), fallbackDash(revoked),
		))
		rows.WriteString("<tr>" +
			"<td>" + html.EscapeString(ts) + "</td>" +
			"<td>" + html.EscapeString(fallbackDash(event.UserID)) + "</td>" +
			"<td>" + html.EscapeString(fallbackDash(event.ClientIP)) + "</td>" +
			"<td>" + html.EscapeString(fallbackDash(familyID)) + "</td>" +
			"<td>" + html.EscapeString(fallbackDash(revoked)) + "</td>" +
			"</tr>")
	}
	if truncated > 0 {
		text.WriteString(fmt.Sprintf("\n…and %d more (see auth_audit_logs).\n", truncated))
	}

	htmlBody = "<h2>Refresh-token reuse detected</h2>" +
		"<p>" + html.EscapeString(fmt.Sprintf("%d event(s) were detected. ", len(events))) +
		"Each is a strong signal of a stolen refresh token; the affected session family was revoked automatically.</p>" +
		"<table border=\"1\" cellpadding=\"6\" cellspacing=\"0\">" +
		"<thead><tr><th>Time (UTC)</th><th>User ID</th><th>Client IP</th><th>Family</th><th>Revoked sessions</th></tr></thead>" +
		"<tbody>" + rows.String() + "</tbody></table>"
	if truncated > 0 {
		htmlBody += "<p>" + html.EscapeString(fmt.Sprintf("…and %d more (see auth_audit_logs).", truncated)) + "</p>"
	}

	return subject, htmlBody, text.String()
}

func fallbackDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}

	return value
}
