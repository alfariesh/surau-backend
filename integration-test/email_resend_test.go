package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// TestAdminEmailResendDeadLetter proves the F1-C dead-letter path end to end:
// a terminally failed transactional email is requeued via the admin endpoint
// and the background dispatcher actually delivers it (log sender in the
// integration stack), flipping the row to sent.
func TestAdminEmailResendDeadLetter(t *testing.T) {
	token := adminJWT(t)

	messageID := seedDeadLetterEmail(t)

	resp := doJSON(t, http.MethodPost, baseURL()+"/v1/admin/emails/messages/"+messageID+"/resend", nil, token)

	var requeued struct {
		ID       string `json:"id"`
		Status   string `json:"status"`
		Attempts int    `json:"attempts"`
		Error    string `json:"error"`
	}

	decodeAndClose(t, resp, &requeued)

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("resend expected 202, got %d", resp.StatusCode)
	}
	if requeued.ID != messageID || requeued.Status != "queued" || requeued.Attempts != 0 {
		t.Fatalf("unexpected requeued message: %+v", requeued)
	}
	// The previous failure must survive the requeue for forensics.
	if requeued.Error == "" {
		t.Fatal("expected previous delivery error to be kept on the requeued row")
	}

	// A message that is no longer dead-lettered cannot be resent again.
	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/admin/emails/messages/"+messageID+"/resend", nil, token)
	resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("second resend expected 409, got %d", resp.StatusCode)
	}

	waitForEmailMessageStatus(t, messageID, "sent", 90*time.Second)
}

func TestAdminEmailResendUnknownMessage(t *testing.T) {
	token := adminJWT(t)

	resp := doJSON(
		t,
		http.MethodPost,
		baseURL()+"/v1/admin/emails/messages/00000000-0000-4000-8000-000000000000/resend",
		nil,
		token,
	)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("resend unknown id expected 404, got %d", resp.StatusCode)
	}

	resp = doJSON(t, http.MethodPost, baseURL()+"/v1/admin/emails/messages/not-a-uuid/resend", nil, token)
	resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("resend malformed id expected 404, got %d", resp.StatusCode)
	}
}

// seedDeadLetterEmail inserts a terminally failed transactional email row
// (attempts exhausted, scheduled_at NULL) and returns its id.
func seedDeadLetterEmail(t *testing.T) string {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	recipient := fmt.Sprintf("deadletter_%d@test.local", time.Now().UnixNano())

	var id string

	err := pool.QueryRow(ctx, `
INSERT INTO email_messages (
    id, category, template_key, recipient_email, lang, subject,
    html, text, critical, status, attempts, error, scheduled_at
) VALUES (
    gen_random_uuid(), 'transactional', 'auth_verification', $1, 'en', 'Integration dead letter',
    '<p>integration resend</p>', 'integration resend', true, 'failed', 6, 'integration synthetic failure', NULL
)
RETURNING id`, recipient).Scan(&id)
	if err != nil {
		t.Fatalf("seed dead-letter email: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cleanupCancel()

		cleanupPool := integrationDB(t)
		defer cleanupPool.Close()

		_, _ = cleanupPool.Exec(cleanupCtx, `DELETE FROM email_messages WHERE id = $1`, id)
	})

	return id
}

func waitForEmailMessageStatus(t *testing.T, id, want string, timeout time.Duration) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()

	deadline := time.Now().Add(timeout)

	for {
		ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)

		var status string

		err := pool.QueryRow(ctx, `SELECT status FROM email_messages WHERE id = $1`, id).Scan(&status)

		cancel()

		if err != nil {
			t.Fatalf("poll email message status: %v", err)
		}

		if status == want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("email message %s never reached status %q (last %q)", id, want, status)
		}

		time.Sleep(2 * time.Second)
	}
}
