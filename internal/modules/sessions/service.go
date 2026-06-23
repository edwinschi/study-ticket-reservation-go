package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/sqlc"
)

const CookieName = "visitor_session"

var ErrSessionRequired = errors.New("visitor session is required")

type CreatedSession struct {
	Session sqlc.VisitorSession
	Token   string
}

type Service struct {
	repository   *Repository
	ttl          time.Duration
	cookieSecure bool
}

func NewService(repository *Repository, ttl time.Duration, cookieSecure bool) *Service {
	return &Service{
		repository:   repository,
		ttl:          ttl,
		cookieSecure: cookieSecure,
	}
}

func (s *Service) CreateAnonymousSession(ctx context.Context) (CreatedSession, error) {
	/*
		The browser receives a random opaque token, but PostgreSQL stores only its SHA-256 hash.
		If the database leaks, raw session cookies are not immediately exposed.
	*/
	token, err := GenerateToken()
	if err != nil {
		return CreatedSession{}, err
	}

	now := time.Now().UTC()
	session, err := s.repository.Create(
		ctx,
		HashToken(token),
		pgtype.Timestamptz{Time: now, Valid: true},
		pgtype.Timestamptz{Time: now.Add(s.ttl), Valid: true},
	)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("create visitor session: %w", err)
	}

	return CreatedSession{
		Session: session,
		Token:   token,
	}, nil
}

func (s *Service) AttachUser(
	ctx context.Context,
	session sqlc.VisitorSession,
	userID pgtype.UUID,
) (sqlc.VisitorSession, error) {
	return s.repository.AttachUser(ctx, session.ID, userID)
}

func (s *Service) GetCurrentSession(ctx context.Context, r *http.Request) (sqlc.VisitorSession, error) {
	/*
		The cookie is only an identifier for a persisted PostgreSQL session. Later reservation
		endpoints should call this helper and use the returned visitor_session.id as ownership.
	*/
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return sqlc.VisitorSession{}, ErrSessionRequired
	}

	session, err := s.repository.GetByTokenHash(ctx, HashToken(cookie.Value))
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return sqlc.VisitorSession{}, ErrSessionRequired
	}
	if err != nil {
		return sqlc.VisitorSession{}, fmt.Errorf("get visitor session: %w", err)
	}

	if db.TimestamptzToTime(session.ExpiresAt).Before(time.Now().UTC()) {
		return sqlc.VisitorSession{}, ErrSessionRequired
	}

	// Touching the session is not part of stock consistency. It is intentionally a small,
	// independent update so later reservation transactions can stay short.
	touched, err := s.repository.Touch(ctx, session.ID)
	if err != nil {
		return sqlc.VisitorSession{}, fmt.Errorf("touch visitor session: %w", err)
	}

	return touched, nil
}

func (s *Service) BuildCookie(token string) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.ttl.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure,
	}
}

func (s *Service) ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure,
	}
}

func GenerateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func ToSessionResponse(session sqlc.VisitorSession) SessionResponse {
	return SessionResponse{
		ID:         db.UUIDToString(session.ID),
		UserID:     db.OptionalUUIDToString(session.UserID),
		CreatedAt:  db.TimestamptzToTime(session.CreatedAt),
		UpdatedAt:  db.TimestamptzToTime(session.UpdatedAt),
		LastSeenAt: db.TimestamptzToTime(session.LastSeenAt),
		ExpiresAt:  db.TimestamptzToTime(session.ExpiresAt),
	}
}
