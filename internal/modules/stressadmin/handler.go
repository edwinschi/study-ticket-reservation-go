package stressadmin

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"ticket-reservation-go-lab/internal/httpserver"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Post("/v1/admin/stress/seed", h.Seed)
	router.Post("/v1/admin/stress/reset", h.Reset)
	router.Get("/v1/admin/stress/assert-consistency", h.AssertConsistency)
}

func (h *Handler) Seed(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.Seed(r.Context())
	if errors.Is(err, ErrStressAdminDisabled) {
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "Not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "STRESS_SEED_FAILED", "Could not seed stress data")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, response)
}

func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.Reset(r.Context())
	if errors.Is(err, ErrStressAdminDisabled) {
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "Not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "STRESS_RESET_FAILED", "Could not reset stress data")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) AssertConsistency(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.AssertConsistency(r.Context())
	if errors.Is(err, ErrStressAdminDisabled) {
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "Not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "CONSISTENCY_ASSERT_FAILED", "Could not assert database consistency")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, response)
}
