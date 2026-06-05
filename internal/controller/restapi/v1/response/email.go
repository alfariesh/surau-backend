package response

import "github.com/evrone/go-clean-template/internal/entity"

// EmailTemplateList wraps paginated email templates.
type EmailTemplateList struct {
	Items []entity.EmailTemplate `json:"items"`
	Total int                    `json:"total"`
} // @name v1.EmailTemplateList

// EmailTemplateVersionList wraps localized template versions.
type EmailTemplateVersionList struct {
	Items []entity.EmailTemplateVersion `json:"items"`
	Total int                           `json:"total"`
} // @name v1.EmailTemplateVersionList

// EmailMessageList wraps delivery log entries.
type EmailMessageList struct {
	Items []entity.EmailMessageLog `json:"items"`
	Total int                      `json:"total"`
} // @name v1.EmailMessageList

// EmailSuppressionList wraps suppression entries.
type EmailSuppressionList struct {
	Items []entity.EmailSuppression `json:"items"`
	Total int                       `json:"total"`
} // @name v1.EmailSuppressionList

// EmailDeliveryEventList wraps delivery audit events.
type EmailDeliveryEventList struct {
	Items []entity.EmailDeliveryEvent `json:"items"`
	Total int                         `json:"total"`
} // @name v1.EmailDeliveryEventList

// EmailCampaignList wraps marketing campaigns.
type EmailCampaignList struct {
	Items []entity.EmailCampaign `json:"items"`
	Total int                    `json:"total"`
} // @name v1.EmailCampaignList

// EmailAudienceRecipientList wraps campaign audience preview recipients.
type EmailAudienceRecipientList struct {
	Items []entity.EmailAudienceRecipient `json:"items"`
	Total int                             `json:"total"`
} // @name v1.EmailAudienceRecipientList
