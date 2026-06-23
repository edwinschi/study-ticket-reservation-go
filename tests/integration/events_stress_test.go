package integration_test

import (
	"net/http"
	"testing"
	"time"
)

type eventPayload struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

type ticketTypePayload struct {
	ID                string `json:"id"`
	EventID           string `json:"event_id"`
	Name              string `json:"name"`
	TotalQuantity     int32  `json:"total_quantity"`
	SoldQuantity      int32  `json:"sold_quantity"`
	ReservedQuantity  int32  `json:"reserved_quantity"`
	AvailableQuantity int32  `json:"available_quantity"`
}

type seatPayload struct {
	ID         string `json:"id"`
	EventID    string `json:"event_id"`
	Section    string `json:"section"`
	RowName    string `json:"row_name"`
	SeatNumber string `json:"seat_number"`
	Status     string `json:"status"`
}

func createEventForTest(t *testing.T, client *http.Client) eventPayload {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Second)
	response := postJSON(t, client, "/v1/events", map[string]any{
		"name":      "Integration Event",
		"starts_at": now.Add(24 * time.Hour),
		"ends_at":   now.Add(27 * time.Hour),
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, response.StatusCode)
	}

	event := decodeBody[eventPayload](t, response)
	if event.ID == "" {
		t.Fatal("expected event id")
	}
	return event
}

func TestCreateAndReadEvent(t *testing.T) {
	client := newHTTPClient(t)
	event := createEventForTest(t, client)

	response := getJSON(t, client, "/v1/events/"+event.ID)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.StatusCode)
	}

	found := decodeBody[eventPayload](t, response)
	if found.ID != event.ID {
		t.Fatalf("expected event id %q, got %q", event.ID, found.ID)
	}
}

func TestCreateTicketTypeSeatsAndInventory(t *testing.T) {
	client := newHTTPClient(t)
	event := createEventForTest(t, client)

	ticketResponse := postJSON(t, client, "/v1/events/"+event.ID+"/ticket-types", map[string]any{
		"name":           "General Admission",
		"total_quantity": 25,
	})
	if ticketResponse.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, ticketResponse.StatusCode)
	}
	ticketType := decodeBody[ticketTypePayload](t, ticketResponse)
	if ticketType.TotalQuantity != 25 {
		t.Fatalf("expected total quantity 25, got %d", ticketType.TotalQuantity)
	}

	seatsResponse := postJSON(t, client, "/v1/events/"+event.ID+"/seats", map[string]any{
		"seats": []map[string]string{
			{"section": "A", "row_name": "1", "seat_number": "1"},
			{"section": "A", "row_name": "1", "seat_number": "2"},
		},
	})
	if seatsResponse.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, seatsResponse.StatusCode)
	}
	seatsPayload := decodeBody[struct {
		Seats []seatPayload `json:"seats"`
	}](t, seatsResponse)
	if len(seatsPayload.Seats) != 2 {
		t.Fatalf("expected 2 seats, got %d", len(seatsPayload.Seats))
	}

	inventoryResponse := getJSON(t, client, "/v1/events/"+event.ID+"/inventory")
	if inventoryResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, inventoryResponse.StatusCode)
	}
	inventory := decodeBody[struct {
		TicketTypes []ticketTypePayload `json:"ticket_types"`
		Seats       []seatPayload       `json:"seats"`
	}](t, inventoryResponse)

	if len(inventory.TicketTypes) != 1 {
		t.Fatalf("expected 1 ticket type, got %d", len(inventory.TicketTypes))
	}
	if inventory.TicketTypes[0].AvailableQuantity != 25 {
		t.Fatalf("expected available quantity 25, got %d", inventory.TicketTypes[0].AvailableQuantity)
	}
	if len(inventory.Seats) != 2 {
		t.Fatalf("expected 2 seats, got %d", len(inventory.Seats))
	}
	for _, seat := range inventory.Seats {
		if seat.Status != "available" {
			t.Fatalf("expected available seat, got %q", seat.Status)
		}
	}
}

func TestStressSeedAndReset(t *testing.T) {
	client := newHTTPClient(t)

	resetBefore := postJSON(t, client, "/v1/admin/stress/reset", map[string]string{})
	if resetBefore.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resetBefore.StatusCode)
	}
	_ = decodeBody[struct {
		EventsDeleted int64 `json:"events_deleted"`
	}](t, resetBefore)

	seedResponse := postJSON(t, client, "/v1/admin/stress/seed", map[string]string{})
	if seedResponse.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, seedResponse.StatusCode)
	}
	seed := decodeBody[struct {
		EventID      string   `json:"event_id"`
		TicketTypeID string   `json:"ticket_type_id"`
		SeatIDs      []string `json:"seat_ids"`
	}](t, seedResponse)
	if seed.EventID == "" || seed.TicketTypeID == "" {
		t.Fatal("expected event_id and ticket_type_id")
	}
	if len(seed.SeatIDs) != 100 {
		t.Fatalf("expected 100 seats, got %d", len(seed.SeatIDs))
	}

	inventoryResponse := getJSON(t, client, "/v1/events/"+seed.EventID+"/inventory")
	if inventoryResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, inventoryResponse.StatusCode)
	}
	inventory := decodeBody[struct {
		TicketTypes []ticketTypePayload `json:"ticket_types"`
		Seats       []seatPayload       `json:"seats"`
	}](t, inventoryResponse)
	if len(inventory.TicketTypes) != 1 || inventory.TicketTypes[0].AvailableQuantity != 1000 {
		t.Fatalf("expected one 1000-unit ticket type, got %+v", inventory.TicketTypes)
	}
	if len(inventory.Seats) != 100 {
		t.Fatalf("expected 100 seats, got %d", len(inventory.Seats))
	}

	resetResponse := postJSON(t, client, "/v1/admin/stress/reset", map[string]string{})
	if resetResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, resetResponse.StatusCode)
	}
	reset := decodeBody[struct {
		EventsDeleted int64 `json:"events_deleted"`
	}](t, resetResponse)
	if reset.EventsDeleted < 1 {
		t.Fatalf("expected at least one deleted stress event, got %d", reset.EventsDeleted)
	}

	deletedEventResponse := getJSON(t, client, "/v1/events/"+seed.EventID)
	defer deletedEventResponse.Body.Close()
	if deletedEventResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("expected status %d after reset, got %d", http.StatusNotFound, deletedEventResponse.StatusCode)
	}
}
