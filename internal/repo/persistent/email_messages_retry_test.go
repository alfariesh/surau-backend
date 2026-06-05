package persistent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClaimDueTransactionalEmailMessagesSQL(t *testing.T) {
	t.Parallel()

	sqlText := claimDueTransactionalEmailMessagesSQL()

	assert.Contains(t, sqlText, "FROM email_messages")
	assert.Contains(t, sqlText, "WHERE category = $1")
	assert.Contains(t, sqlText, "AND status = $2")
	assert.Contains(t, sqlText, "AND scheduled_at IS NOT NULL")
	assert.Contains(t, sqlText, "AND scheduled_at <= $3")
	assert.Contains(t, sqlText, "ORDER BY scheduled_at ASC, created_at ASC")
	assert.Contains(t, sqlText, "LIMIT $4")
	assert.Contains(t, sqlText, "FOR UPDATE SKIP LOCKED")
	assert.Contains(t, sqlText, "UPDATE email_messages AS m")
	assert.Contains(t, sqlText, "SET scheduled_at = $5")
	assert.Contains(t, sqlText, "RETURNING")
}

func TestEmailMessageClaimLimitDefaultsAndCaps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{name: "default", limit: 0, want: 50},
		{name: "negative", limit: -1, want: 50},
		{name: "given", limit: 20, want: 20},
		{name: "capped", limit: 101, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, emailMessageClaimLimit(tt.limit))
		})
	}
}
