package events

import "time"

type CreateEventRequest struct {
	Name     string    `json:"name"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

type EventResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateTicketTypeRequest struct {
	Name          string `json:"name"`
	TotalQuantity int32  `json:"total_quantity"`
}

type TicketTypeResponse struct {
	ID               string    `json:"id"`
	EventID          string    `json:"event_id"`
	Name             string    `json:"name"`
	TotalQuantity    int32     `json:"total_quantity"`
	SoldQuantity     int32     `json:"sold_quantity"`
	ReservedQuantity int32     `json:"reserved_quantity"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type CreateSeatRequest struct {
	Section    string `json:"section"`
	RowName    string `json:"row_name"`
	SeatNumber string `json:"seat_number"`
}

type CreateSeatsRequest struct {
	Seats []CreateSeatRequest `json:"seats"`
}

type SeatResponse struct {
	ID         string    `json:"id"`
	EventID    string    `json:"event_id"`
	Section    string    `json:"section"`
	RowName    string    `json:"row_name"`
	SeatNumber string    `json:"seat_number"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type TicketTypeInventoryResponse struct {
	ID                string `json:"id"`
	EventID           string `json:"event_id"`
	Name              string `json:"name"`
	TotalQuantity     int32  `json:"total_quantity"`
	SoldQuantity      int32  `json:"sold_quantity"`
	ReservedQuantity  int32  `json:"reserved_quantity"`
	AvailableQuantity int32  `json:"available_quantity"`
}

type SeatInventoryResponse struct {
	ID         string `json:"id"`
	EventID    string `json:"event_id"`
	Section    string `json:"section"`
	RowName    string `json:"row_name"`
	SeatNumber string `json:"seat_number"`
	Status     string `json:"status"`
}

type InventoryResponse struct {
	TicketTypes []TicketTypeInventoryResponse `json:"ticket_types"`
	Seats       []SeatInventoryResponse       `json:"seats"`
}
