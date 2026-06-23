package stressadmin

type SeedResponse struct {
	EventID      string   `json:"event_id"`
	TicketTypeID string   `json:"ticket_type_id"`
	SeatIDs      []string `json:"seat_ids"`
}

type ResetResponse struct {
	EventsDeleted int64 `json:"events_deleted"`
}

type ConsistencyDetail struct {
	Check   string         `json:"check"`
	Message string         `json:"message"`
	Data    map[string]any `json:"data"`
}

type ConsistencyChecks struct {
	TicketQuantityNotOversold        bool `json:"ticket_quantity_not_oversold"`
	TicketQuantityNotNegative        bool `json:"ticket_quantity_not_negative"`
	NoDuplicateActiveSeats           bool `json:"no_duplicate_active_seats"`
	NoOrphanReservationItems         bool `json:"no_orphan_reservation_items"`
	NoOrphanReservationSeats         bool `json:"no_orphan_reservation_seats"`
	NoStaleExpiredActiveReservations bool `json:"no_stale_expired_active_reservations"`
}

type ConsistencyResponse struct {
	OK      bool                `json:"ok"`
	Checks  ConsistencyChecks   `json:"checks"`
	Details []ConsistencyDetail `json:"details"`
}
