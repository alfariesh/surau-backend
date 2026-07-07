package persistent

import (
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmailDeliveryEventListSelectDefaultsAndOrdersNewestFirst(t *testing.T) {
	t.Parallel()

	query := emailDeliveryEventListSelect(
		sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
		repo.EmailDeliveryEventFilter{},
	)

	sqlText, args, err := query.ToSql()

	require.NoError(t, err)
	assert.Contains(t, sqlText, "FROM email_delivery_events")
	assert.Contains(t, sqlText, "ORDER BY created_at DESC")
	assert.Contains(t, sqlText, "LIMIT 50 OFFSET 0")
	assert.Empty(t, args)
}

func TestEmailDeliveryEventListSelectFiltersAndCapsLimit(t *testing.T) {
	t.Parallel()

	query := emailDeliveryEventListSelect(
		sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
		repo.EmailDeliveryEventFilter{
			Provider:            entity.EmailProviderCloudflare,
			EventType:           entity.EmailDeliveryEventBounceHard,
			Email:               "USER@example.com",
			MessageID:           "message-id",
			CampaignID:          "campaign-id",
			CampaignRecipientID: "recipient-id",
			Limit:               101,
			Offset:              7,
		},
	)

	sqlText, args, err := query.ToSql()

	require.NoError(t, err)
	assert.Contains(t, sqlText, "provider = $1")
	assert.Contains(t, sqlText, "event_type = $2")
	assert.Contains(t, sqlText, "lower(recipient_email) = lower($3)")
	assert.Contains(t, sqlText, "message_id = $4")
	assert.Contains(t, sqlText, "campaign_id = $5")
	assert.Contains(t, sqlText, "campaign_recipient_id = $6")
	assert.Contains(t, sqlText, "LIMIT 100 OFFSET 7")
	assert.Equal(t, []any{
		entity.EmailProviderCloudflare,
		entity.EmailDeliveryEventBounceHard,
		"USER@example.com",
		"message-id",
		"campaign-id",
		"recipient-id",
	}, args)
}
