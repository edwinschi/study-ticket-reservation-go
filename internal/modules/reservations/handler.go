package reservations

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/httpserver"
	"ticket-reservation-go-lab/internal/modules/sessions"
	"ticket-reservation-go-lab/internal/sqlc"
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
	router.Post("/v1/reservations/quantity", h.ReserveQuantity)
	router.Post("/v1/reservations/seats", h.ReserveSeats)
	router.Get("/v1/reservations/{reservation_id}", h.GetReservation)
	router.Post("/v1/reservations/{reservation_id}/cancel", h.CancelReservation)
	router.Post("/v1/reservations/{reservation_id}/confirm", h.ConfirmReservation)
}

// ReserveQuantity translates HTTP concerns into service input.
//
// The handler deliberately does not read or calculate stock. Keeping inventory logic in the
// service/repository layer makes it much harder to accidentally bypass the PostgreSQL atomic
// UPDATE that protects quantity reservations.
func (h *Handler) ReserveQuantity(w http.ResponseWriter, r *http.Request) {
	currentSession, err := h.sessionService.GetCurrentSession(r.Context(), r)
	if errors.Is(err, sessions.ErrSessionRequired) {
		httpserver.WriteError(w, http.StatusUnauthorized, "SESSION_REQUIRED", "A valid visitor session is required")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "SESSION_LOOKUP_FAILED", "Could not read session")
		return
	}

	var payload QuantityReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON")
		return
	}

	response, err := h.service.ReserveQuantity(r.Context(), currentSession, payload)
	if errors.Is(err, ErrInvalidQuantityReservation) {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_QUANTITY_RESERVATION", "Invalid quantity reservation payload")
		return
	}
	if errors.Is(err, ErrInsufficientStock) {
		httpserver.WriteError(w, http.StatusConflict, "INSUFFICIENT_STOCK", "Not enough stock available")
		return
	}
	if errors.Is(err, ErrIdempotencyKeyConflict) {
		httpserver.WriteError(w, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", "Idempotency key was already used")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "RESERVATION_CREATE_FAILED", "Could not create reservation")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, response)
}

// GetReservation returns only reservations visible to the current visitor session.
func (h *Handler) GetReservation(w http.ResponseWriter, r *http.Request) {
	currentSession, reservationID, ok := h.currentSessionAndReservationID(w, r)
	if !ok {
		return
	}

	response, err := h.service.GetReservation(r.Context(), currentSession, reservationID)
	if errors.Is(err, ErrReservationNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "RESERVATION_NOT_FOUND", "Reservation not found")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "RESERVATION_LOOKUP_FAILED", "Could not read reservation")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

// CancelReservation maps expected lifecycle conflicts to 409 instead of logging them as server
// failures. A confirmed reservation, for example, is not cancellable but that is a business
// conflict, not an infrastructure error.
func (h *Handler) CancelReservation(w http.ResponseWriter, r *http.Request) {
	currentSession, reservationID, ok := h.currentSessionAndReservationID(w, r)
	if !ok {
		return
	}

	response, err := h.service.CancelReservation(r.Context(), currentSession, reservationID)
	if errors.Is(err, ErrReservationNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "RESERVATION_NOT_FOUND", "Reservation not found")
		return
	}
	if errors.Is(err, ErrReservationCannotCancel) {
		httpserver.WriteError(w, http.StatusConflict, "RESERVATION_CANNOT_BE_CANCELLED", "Reservation cannot be cancelled")
		return
	}
	if errors.Is(err, ErrReservationInvariantBroken) {
		httpserver.WriteError(w, http.StatusConflict, "RESERVATION_INCONSISTENT", "Reservation cannot be transitioned safely")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "RESERVATION_CANCEL_FAILED", "Could not cancel reservation")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

// ConfirmReservation simulates purchase completion. Payment is intentionally outside this study
// stage; the endpoint focuses on the inventory state transition.
func (h *Handler) ConfirmReservation(w http.ResponseWriter, r *http.Request) {
	currentSession, reservationID, ok := h.currentSessionAndReservationID(w, r)
	if !ok {
		return
	}

	response, err := h.service.ConfirmReservation(r.Context(), currentSession, reservationID)
	if errors.Is(err, ErrReservationNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "RESERVATION_NOT_FOUND", "Reservation not found")
		return
	}
	if errors.Is(err, ErrReservationCannotConfirm) {
		httpserver.WriteError(w, http.StatusConflict, "RESERVATION_CANNOT_BE_CONFIRMED", "Reservation cannot be confirmed")
		return
	}
	if errors.Is(err, ErrReservationInvariantBroken) {
		httpserver.WriteError(w, http.StatusConflict, "RESERVATION_INCONSISTENT", "Reservation cannot be transitioned safely")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "RESERVATION_CONFIRM_FAILED", "Could not confirm reservation")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

// currentSessionAndReservationID centralizes the common authorization preconditions for
// reservation lifecycle endpoints.
func (h *Handler) currentSessionAndReservationID(
	w http.ResponseWriter,
	r *http.Request,
) (sqlc.VisitorSession, pgtype.UUID, bool) {
	currentSession, err := h.sessionService.GetCurrentSession(r.Context(), r)
	if errors.Is(err, sessions.ErrSessionRequired) {
		httpserver.WriteError(w, http.StatusUnauthorized, "SESSION_REQUIRED", "A valid visitor session is required")
		return sqlc.VisitorSession{}, pgtype.UUID{}, false
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "SESSION_LOOKUP_FAILED", "Could not read session")
		return sqlc.VisitorSession{}, pgtype.UUID{}, false
	}

	reservationID, err := db.ParseUUID(chi.URLParam(r, "reservation_id"))
	if err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_RESERVATION_ID", "reservation_id must be a valid UUID")
		return sqlc.VisitorSession{}, pgtype.UUID{}, false
	}

	return currentSession, reservationID, true
}

// ReserveSeats keeps HTTP parsing separate from seat-locking logic.
//
// The actual concurrency protection lives in the service transaction with SELECT FOR UPDATE and
// the partial unique index. This handler only maps validation, not-found and conflict errors to
// stable HTTP responses.
func (h *Handler) ReserveSeats(w http.ResponseWriter, r *http.Request) {
	currentSession, err := h.sessionService.GetCurrentSession(r.Context(), r)
	if errors.Is(err, sessions.ErrSessionRequired) {
		httpserver.WriteError(w, http.StatusUnauthorized, "SESSION_REQUIRED", "A valid visitor session is required")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "SESSION_LOOKUP_FAILED", "Could not read session")
		return
	}

	var payload SeatReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_JSON", "Request body must be valid JSON")
		return
	}

	response, err := h.service.ReserveSeats(r.Context(), currentSession, payload)
	if errors.Is(err, ErrInvalidSeatReservation) {
		httpserver.WriteError(w, http.StatusBadRequest, "INVALID_SEAT_RESERVATION", "Invalid seat reservation payload")
		return
	}
	if errors.Is(err, ErrSeatsNotFound) {
		httpserver.WriteError(w, http.StatusNotFound, "SEATS_NOT_FOUND", "One or more seats do not exist for this event")
		return
	}
	if errors.Is(err, ErrSeatUnavailable) {
		httpserver.WriteError(w, http.StatusConflict, "SEAT_UNAVAILABLE", "One or more seats are not available")
		return
	}
	if errors.Is(err, ErrIdempotencyKeyConflict) {
		httpserver.WriteError(w, http.StatusConflict, "IDEMPOTENCY_KEY_CONFLICT", "Idempotency key was already used")
		return
	}
	if err != nil {
		httpserver.WriteError(w, http.StatusInternalServerError, "SEAT_RESERVATION_CREATE_FAILED", "Could not create seat reservation")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, response)
}
