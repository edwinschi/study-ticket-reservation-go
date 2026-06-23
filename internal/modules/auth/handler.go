package auth

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"ticket-reservation-go-lab/internal/httpserver"
	"ticket-reservation-go-lab/internal/modules/sessions"
)

type Handler struct {
	service        *Service
	sessionService *sessions.Service
}

func NewHandler(service *Service, sessionService *sessions.Service) *Handler {
	return &Handler{
		service:        service,
		sessionService: sessionService,
	}
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Post("/v1/auth/register", h.Register)
	router.Post("/v1/auth/login", h.Login)
	router.Post("/v1/auth/logout", h.Logout)
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var payload RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON")
		return
	}

	user, err := h.service.Register(r.Context(), payload.Email, payload.Password)
	if errors.Is(err, ErrEmailAlreadyRegistered) {
		httpserver.WriteError(w, http.StatusConflict, "EMAIL_ALREADY_REGISTERED", "Email is already registered")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_REGISTER_REQUEST", "Email and password are required")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, ToUserResponse(user))
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var payload LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON")
		return
	}

	result, err := h.service.Login(r.Context(), r, payload.Email, payload.Password)
	if errors.Is(err, ErrInvalidCredentials) {
		httpserver.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid email or password")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "LOGIN_FAILED", "Could not login")
		return
	}

	if result.SessionToken != "" {
		http.SetCookie(w, h.sessionService.BuildCookie(result.SessionToken))
	}
	httpserver.WriteJSON(w, http.StatusOK, ToUserResponse(result.User))
}

func (h *Handler) Logout(w http.ResponseWriter, _ *http.Request) {
	// Logout only removes the browser cookie. Existing reservations remain in PostgreSQL and
	// their lifecycle will be handled by reservation endpoints/workers in later stages.
	http.SetCookie(w, h.sessionService.ClearCookie())
	httpserver.WriteJSON(w, http.StatusOK, LogoutResponse{Status: "logged_out"})
}
