package entity

import (
	"slices"
	"time"
)

const (
	ServiceScopeCollabDraftWrite      = "collab:draft:write"
	ServiceScopeRAGEvalRead           = "rag-eval:read"
	ServiceScopeEnrichmentRead        = "enrichment:read"
	ServiceScopePromptRegistryManage  = "prompt-registry:manage"
	ServiceScopeInferenceBudgetManage = "inference-budget:manage"

	ServicePrincipalUnattributed = "unattributed"

	ServiceAuthOutcomeStarted           = "started"
	ServiceAuthOutcomeAllowed           = "allowed"
	ServiceAuthOutcomeMissing           = "missing"
	ServiceAuthOutcomeMalformed         = "malformed"
	ServiceAuthOutcomeInvalid           = "invalid"
	ServiceAuthOutcomeExpired           = "expired"
	ServiceAuthOutcomeTokenRevoked      = "token_revoked"
	ServiceAuthOutcomePrincipalRevoked  = "principal_revoked"
	ServiceAuthOutcomeInsufficientScope = "insufficient_scope"
	ServiceAuthOutcomeUnavailable       = "unavailable"
)

// AllServiceScopes returns the frozen A-2 machine-capability registry. New
// values are additive API/security decisions and must update the DB CHECK plus
// contract test.
func AllServiceScopes() []string {
	return []string{
		ServiceScopeCollabDraftWrite,
		ServiceScopeRAGEvalRead,
		ServiceScopeEnrichmentRead,
		ServiceScopePromptRegistryManage,
		ServiceScopeInferenceBudgetManage,
	}
}

// IsValidServiceScope reports whether scope is in the frozen registry.
func IsValidServiceScope(scope string) bool {
	return slices.Contains(AllServiceScopes(), scope)
}

// ServicePrincipal is one stable, named machine identity. Scopes belong to
// the principal; credentials are independently rotatable children.
type ServicePrincipal struct {
	ID            string         `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	PrincipalName string         `json:"principal_name" example:"collab-server"`
	Description   string         `json:"description" example:"Realtime draft bridge"`
	Scopes        []string       `json:"scopes" example:"collab:draft:write"`
	Tokens        []ServiceToken `json:"tokens,omitempty"`
	CreatedBy     *string        `json:"created_by,omitempty"`
	RevokedAt     *time.Time     `json:"revoked_at,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
} // @name entity.ServicePrincipal

// ServiceToken is safe token metadata. SecretHash and the raw credential are
// deliberately absent from every JSON-facing type.
type ServiceToken struct {
	ID          string     `json:"id" example:"550e8400-e29b-41d4-a716-446655440001"`
	PrincipalID string     `json:"principal_id"`
	TokenKind   string     `json:"token_kind" example:"structured"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedBy   *string    `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
} // @name entity.ServiceToken

// ServiceTokenRecord is the persistence/authentication form. SecretHash must
// never be serialized, logged, or returned by a controller.
type ServiceTokenRecord struct {
	ServiceToken
	SecretHash []byte `json:"-"`
}

// ServiceCredential is the joined persistence record used by authentication.
type ServiceCredential struct {
	Token         ServiceTokenRecord
	PrincipalID   string
	PrincipalName string
	Scopes        []string
	RevokedAt     *time.Time
}

// IssuedServiceToken contains the raw secret exactly once, at issuance.
type IssuedServiceToken struct {
	ServiceToken
	Token string `json:"token" example:"surau_st_550e8400-e29b-41d4-a716-446655440001.secret"`
} // @name entity.IssuedServiceToken

// ServiceTokenIssueResult returns the new credential plus the principal whose
// ETag advanced atomically with issuance.
type ServiceTokenIssueResult struct {
	Principal ServicePrincipal   `json:"principal"`
	Token     IssuedServiceToken `json:"credential"`
} // @name entity.ServiceTokenIssueResult

// ServiceAuthentication is the trusted request-local machine identity. A
// denied request may still carry a verified principal when the credential is
// expired/revoked; fake credentials remain unattributed.
type ServiceAuthentication struct {
	PrincipalID   string
	PrincipalName string
	TokenID       string
	Scopes        []string
	ExpiresAt     time.Time
	Outcome       string
}

// HasScope reports whether this authenticated principal owns scope.
func (a *ServiceAuthentication) HasScope(scope string) bool {
	return slices.Contains(a.Scopes, scope)
}

// ServiceRequestAudit is the durable A-2 request evidence. RouteTemplate is
// used instead of the raw URL so IDs/query strings do not become audit PII.
type ServiceRequestAudit struct {
	ID             string
	PrincipalID    *string
	PrincipalName  string
	TokenID        *string
	RequiredScope  *string
	Method         string
	RouteTemplate  string
	RequestID      *string
	TraceID        *string
	ClientIP       *string
	AuthOutcome    string
	ResponseStatus *int
	StartedAt      time.Time
	FinishedAt     *time.Time
}

// ServiceIdentityEvent is an append-only lifecycle audit event.
type ServiceIdentityEvent struct {
	ID            string
	PrincipalID   *string
	PrincipalName string
	TokenID       *string
	ActorUserID   *string
	Action        string
	Metadata      map[string]any
	CreatedAt     time.Time
}

// ServiceAuditCleanupResult reports bounded high-volume request-audit cleanup.
type ServiceAuditCleanupResult struct {
	RequestLogs int64
}
