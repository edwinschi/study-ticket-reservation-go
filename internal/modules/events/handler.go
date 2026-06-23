package events

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

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
	router.Post("/v1/events", h.CreateEvent)
	router.Get("/v1/events/{event_id}", h.GetEvent)
	router.Post("/v1/events/{event_id}/ticket-types", h.CreateTicketType)
	router.Post("/v1/events/{event_id}/seats", h.CreateSeats)
	router.Get("/v1/events/{event_id}/inventory", h.GetInventory)
}

func (h *Handler) CreateEvent(w http.ResponseWriter, r *http.Request) {
	var payload CreateEventRequest
	if !decodeJSON(w, r, &payload) {
		return
	}

	event, err := h.service.CreateEvent(r.Context(), payload)
	if errors.Is(err, ErrInvalidEventInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_EVENT", "Invalid event payload")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "EVENT_CREATE_FAILED", "Could not create event")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, event)
}

func (h *Handler) GetEvent(w http.ResponseWriter, r *http.Request) {
	eventID, ok := eventIDFromRequest(w, r)
	if !ok {
		return
	}

	event, err := h.service.GetEvent(r.Context(), eventID)
	if errors.Is(err, ErrEventNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "EVENT_NOT_FOUND", "Event not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "EVENT_LOOKUP_FAILED", "Could not read event")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, event)
}

func (h *Handler) CreateTicketType(w http.ResponseWriter, r *http.Request) {
	eventID, ok := eventIDFromRequest(w, r)
	if !ok {
		return
	}

	var payload CreateTicketTypeRequest
	if !decodeJSON(w, r, &payload) {
		return
	}

	ticketType, err := h.service.CreateTicketType(r.Context(), eventID, payload)
	if errors.Is(err, ErrEventNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "EVENT_NOT_FOUND", "Event not found")
		return
	}
	if errors.Is(err, ErrInvalidTicketTypeInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_TICKET_TYPE", "Invalid ticket type payload")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "TICKET_TYPE_CREATE_FAILED", "Could not create ticket type")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, ticketType)
}

func (h *Handler) CreateSeats(w http.ResponseWriter, r *http.Request) {
	eventID, ok := eventIDFromRequest(w, r)
	if !ok {
		return
	}

	var payload CreateSeatsRequest
	if !decodeJSON(w, r, &payload) {
		return
	}

	seats, err := h.service.CreateSeats(r.Context(), eventID, payload)
	if errors.Is(err, ErrEventNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "EVENT_NOT_FOUND", "Event not found")
		return
	}
	if errors.Is(err, ErrInvalidSeatInput) {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_SEATS", "Invalid seats payload")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "SEATS_CREATE_FAILED", "Could not create seats")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, map[string][]SeatResponse{"seats": seats})
}

func (h *Handler) GetInventory(w http.ResponseWriter, r *http.Request) {
	eventID, ok := eventIDFromRequest(w, r)
	if !ok {
		return
	}

	inventory, err := h.service.GetInventory(r.Context(), eventID)
	if errors.Is(err, ErrEventNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "EVENT_NOT_FOUND", "Event not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "INVENTORY_LOOKUP_FAILED", "Could not read inventory")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, inventory)
}

func eventIDFromRequest(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	eventID, err := db.ParseUUID(chi.URLParam(r, "event_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_EVENT_ID", "event_id must be a valid UUID")
		return pgtype.UUID{}, false
	}
	return eventID, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, payload any) bool {
	if err := json.NewDecoder(r.Body).Decode(payload); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON")
		return false
	}
	return true
}
