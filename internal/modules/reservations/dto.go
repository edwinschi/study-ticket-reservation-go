package reservations

import "time"

type QuantityReservationRequest struct {
	EventID        string `json:"event_id"`
	TicketTypeID   string `json:"ticket_type_id"`
	Quantity       int32  `json:"quantity"`
	IdempotencyKey string `json:"idempotency_key"`
}

type QuantityReservationItemResponse struct {
	TicketTypeID string `json:"ticket_type_id"`
	Quantity     int32  `json:"quantity"`
}

type QuantityReservationResponse struct {
	ReservationID   string                            `json:"reservation_id"`
	Status          string                            `json:"status"`
	ReservationType string                            `json:"reservation_type"`
	ExpiresAt       time.Time                         `json:"expires_at"`
	Items           []QuantityReservationItemResponse `json:"items"`
}

type SeatReservationRequest struct {
	EventID        string   `json:"event_id"`
	SeatIDs        []string `json:"seat_ids"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type SeatReservationItemResponse struct {
	SeatID string `json:"seat_id"`
}

type SeatReservationResponse struct {
	ReservationID   string                        `json:"reservation_id"`
	Status          string                        `json:"status"`
	ReservationType string                        `json:"reservation_type"`
	ExpiresAt       time.Time                     `json:"expires_at"`
	Seats           []SeatReservationItemResponse `json:"seats"`
}

type ReservationItemDetailResponse struct {
	TicketTypeID string `json:"ticket_type_id"`
	Quantity     int32  `json:"quantity"`
	Status       string `json:"status"`
}

type ReservationSeatDetailResponse struct {
	SeatID string `json:"seat_id"`
	Status string `json:"status"`
}

type ReservationResponse struct {
	ReservationID   string                          `json:"reservation_id"`
	Status          string                          `json:"status"`
	ReservationType string                          `json:"reservation_type"`
	ExpiresAt       time.Time                       `json:"expires_at"`
	Items           []ReservationItemDetailResponse `json:"items,omitempty"`
	Seats           []ReservationSeatDetailResponse `json:"seats,omitempty"`
}
