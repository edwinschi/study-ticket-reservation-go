package sessions

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/httpserver"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Post("/v1/sessions/anonymous", h.CreateAnonymousSession)
	router.Get("/v1/me/session", h.GetCurrentSession)
}

func (h *Handler) CreateAnonymousSession(w http.ResponseWriter, r *http.Request) {
	created, err := h.service.CreateAnonymousSession(r.Context())
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "SESSION_CREATE_FAILED", "Could not create session")
		return
	}

	http.SetCookie(w, h.service.BuildCookie(created.Token))
	httpserver.WriteJSON(w, http.StatusCreated, AnonymousSessionResponse{
		VisitorSessionID: db.UUIDToString(created.Session.ID),
	})
}

func (h *Handler) GetCurrentSession(w http.ResponseWriter, r *http.Request) {
	session, err := h.service.GetCurrentSession(r.Context(), r)
	if errors.Is(err, ErrSessionRequired) {
		httpserver.WriteError(w, http.StatusUnauthorized, "SESSION_REQUIRED", "A valid visitor session is required")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "SESSION_LOOKUP_FAILED", "Could not read session")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, ToSessionResponse(session))
}
