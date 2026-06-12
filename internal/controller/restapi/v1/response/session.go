package response

import (
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
)

// SessionInfo describes one active device/session for the "manage devices" view.
// Sensitive fields (token hash, token version, replacement chain) are omitted.
type SessionInfo struct {
	ID         string    `json:"id" example:"550e8400-e29b-41d4-a716-446655440000"`
	UserAgent  string    `json:"user_agent" example:"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)"`
	ClientIP   string    `json:"client_ip" example:"203.0.113.42"`
	CreatedAt  time.Time `json:"created_at" example:"2026-01-01T00:00:00Z"`
	LastUsedAt time.Time `json:"last_used_at" example:"2026-01-02T08:30:00Z"`
	ExpiresAt  time.Time `json:"expires_at" example:"2026-02-01T00:00:00Z"`
	IsCurrent  bool      `json:"is_current" example:"true"`
} // @name v1.SessionInfo

// SessionList is the response for GET /auth/sessions.
type SessionList struct {
	Items []SessionInfo `json:"items"`
	Total int           `json:"total" example:"2"`
} // @name v1.SessionList

// SessionRevoked is the response for DELETE /auth/sessions/:id.
type SessionRevoked struct {
	SessionRevoked bool `json:"session_revoked" example:"true"`
} // @name v1.SessionRevoked

// NewSessionList builds the device list, flagging the session whose family
// matches the caller's current access token.
func NewSessionList(sessions []entity.AuthSession, currentFamilyID string) SessionList {
	items := make([]SessionInfo, 0, len(sessions))
	for i := range sessions {
		session := &sessions[i]
		items = append(items, SessionInfo{
			ID:         session.ID,
			UserAgent:  session.UserAgent,
			ClientIP:   session.ClientIP,
			CreatedAt:  session.CreatedAt,
			LastUsedAt: session.LastUsedAt,
			ExpiresAt:  session.ExpiresAt,
			IsCurrent:  currentFamilyID != "" && session.FamilyID == currentFamilyID,
		})
	}

	return SessionList{Items: items, Total: len(items)}
}
