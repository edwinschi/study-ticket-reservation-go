package sessions

import "time"

type AnonymousSessionResponse struct {
	VisitorSessionID string `json:"visitor_session_id"`
}

type SessionResponse struct {
	ID         string    `json:"id"`
	UserID     *string   `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}
