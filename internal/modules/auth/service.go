package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/crypto/bcrypt"

	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/modules/sessions"
	"ticket-reservation-go-lab/internal/sqlc"
)

var (
	ErrEmailAlreadyRegistered = errors.New("email already registered")
	ErrInvalidCredentials     = errors.New("invalid credentials")
	ErrCurrentUserRequired    = errors.New("current user is required")
)

type LoginResult struct {
	User         sqlc.User
	SessionToken string
}

type Service struct {
	repository     *Repository
	sessionService *sessions.Service
}

func NewService(repository *Repository, sessionService *sessions.Service) *Service {
	return &Service{
		repository:     repository,
		sessionService: sessionService,
	}
}

func (s *Service) Register(ctx context.Context, email string, password string) (sqlc.User, error) {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	if normalizedEmail == "" || len(password) < 8 {
		return sqlc.User{}, fmt.Errorf("invalid register request")
	}

	// Passwords are never stored in plain text. bcrypt includes a salt and work factor in the
	// encoded hash, which is sufficient for this study project.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return sqlc.User{}, fmt.Errorf("hash password: %w", err)
	}

	user, err := s.repository.CreateUser(ctx, normalizedEmail, string(passwordHash))
	if isUniqueViolation(err) {
		return sqlc.User{}, ErrEmailAlreadyRegistered
	}
	if err != nil {
		return sqlc.User{}, fmt.Errorf("create user: %w", err)
	}

	return user, nil
}

func (s *Service) Login(ctx context.Context, r *http.Request, email string, password string) (LoginResult, error) {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	user, err := s.repository.GetUserByEmail(ctx, normalizedEmail)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return LoginResult{}, ErrInvalidCredentials
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("get user by email: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return LoginResult{}, ErrInvalidCredentials
	}

	currentSession, err := s.sessionService.GetCurrentSession(ctx, r)
	if errors.Is(err, sessions.ErrSessionRequired) {
		// Login without an anonymous cookie creates a new visitor session. This keeps the study
		// auth model simple: the same HTTP-only cookie represents anonymous and logged-in states.
		created, createErr := s.sessionService.CreateAnonymousSession(ctx)
		if createErr != nil {
			return LoginResult{}, createErr
		}
		currentSession = created.Session
		_, err = s.sessionService.AttachUser(ctx, currentSession, user.ID)
		if err != nil {
			return LoginResult{}, fmt.Errorf("attach user to new session: %w", err)
		}
		return LoginResult{
			User:         user,
			SessionToken: created.Token,
		}, nil
	}
	if err != nil {
		return LoginResult{}, err
	}

	if _, err := s.sessionService.AttachUser(ctx, currentSession, user.ID); err != nil {
		return LoginResult{}, fmt.Errorf("attach user to visitor session: %w", err)
	}

	token := ""
	if cookie, cookieErr := r.Cookie(sessions.CookieName); cookieErr == nil {
		token = cookie.Value
	}

	return LoginResult{
		User:         user,
		SessionToken: token,
	}, nil
}

func (s *Service) GetOptionalUser(ctx context.Context, session sqlc.VisitorSession) (*sqlc.User, error) {
	if !session.UserID.Valid {
		return nil, nil
	}

	user, err := s.repository.GetUserByID(ctx, session.UserID)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Service) GetCurrentUser(ctx context.Context, session sqlc.VisitorSession) (sqlc.User, error) {
	user, err := s.GetOptionalUser(ctx, session)
	if err != nil {
		return sqlc.User{}, err
	}
	if user == nil {
		return sqlc.User{}, ErrCurrentUserRequired
	}
	return *user, nil
}

func ToUserResponse(user sqlc.User) UserResponse {
	return UserResponse{
		ID:        db.UUIDToString(user.ID),
		Email:     user.Email,
		CreatedAt: db.TimestamptzToTime(user.CreatedAt),
		UpdatedAt: db.TimestamptzToTime(user.UpdatedAt),
	}
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
