package request

import "time"

// CreateServiceIdentity registers one immutable machine principal.
type CreateServiceIdentity struct {
	PrincipalName string   `json:"principal_name" validate:"required,min=3,max=63" example:"collab-server"`
	Description   string   `json:"description" validate:"max=500" example:"Realtime draft bridge"`
	Scopes        []string `json:"scopes" validate:"required,min=1,dive,required" example:"collab:draft:write"`
} // @name request.CreateServiceIdentity

// UpdateServiceIdentity replaces the mutable description and complete scope set.
type UpdateServiceIdentity struct {
	Description string   `json:"description" validate:"max=500"`
	Scopes      []string `json:"scopes" validate:"required,min=1,dive,required"`
} // @name request.UpdateServiceIdentity

// IssueServiceToken optionally overrides the default 30-day expiry.
type IssueServiceToken struct {
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
} // @name request.IssueServiceToken
