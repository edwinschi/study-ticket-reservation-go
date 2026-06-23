package integration_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

type consistencyResponse struct {
	OK     bool `json:"ok"`
	Checks struct {
		TicketQuantityNotOversold        bool `json:"ticket_quantity_not_oversold"`
		TicketQuantityNotNegative        bool `json:"ticket_quantity_not_negative"`
		NoDuplicateActiveSeats           bool `json:"no_duplicate_active_seats"`
		NoOrphanReservationItems         bool `json:"no_orphan_reservation_items"`
		NoOrphanReservationSeats         bool `json:"no_orphan_reservation_seats"`
		NoStaleExpiredActiveReservations bool `json:"no_stale_expired_active_reservations"`
	} `json:"checks"`
	Details []struct {
		Check   string         `json:"check"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	} `json:"details"`
}

func assertConsistencyOK(t *testing.T, client *http.Client) consistencyResponse {
	t.Helper()

	// The local database is shared across integration tests. Running a few expiration batches
	// before asserting avoids false negatives from stale data left by previous test runs.
	for index := 0; index < 3; index++ {
		if processed := expireOnceForTest(t, 10000); processed == 0 {
			break
		}
	}

	response := getJSON(t, client, "/v1/admin/stress/assert-consistency")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.StatusCode)
	}

	payload := decodeBody[consistencyResponse](t, response)
	if !payload.OK {
		t.Fatalf("expected consistency ok=true, got details=%+v", payload.Details)
	}
	if !payload.Checks.TicketQuantityNotOversold ||
		!payload.Checks.TicketQuantityNotNegative ||
		!payload.Checks.NoDuplicateActiveSeats ||
		!payload.Checks.NoOrphanReservationItems ||
		!payload.Checks.NoOrphanReservationSeats ||
		!payload.Checks.NoStaleExpiredActiveReservations {
		t.Fatalf("expected all checks to be true, got %+v", payload.Checks)
	}
	if len(payload.Details) != 0 {
		t.Fatalf("expected empty details, got %+v", payload.Details)
	}
	return payload
}

func TestAssertConsistencyReturnsOK(t *testing.T) {
	client := newHTTPClient(t)
	assertConsistencyOK(t, client)
}

func TestAssertConsistencyAfterReservationLifecycleFlow(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)

	quantityEventID, ticketTypeID := createQuantityInventoryForTest(t, client, 5)
	quantityStatus, quantityBody, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        quantityEventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       2,
		IdempotencyKey: fmt.Sprintf("consistency-quantity-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if quantityStatus != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, quantityStatus, string(quantityBody))
	}
	quantityReservation := decodeJSONBytes[quantityReservationResponse](t, quantityBody)

	confirmStatus, confirmBody, err := postReservationActionHTTP(client, quantityReservation.ReservationID, "confirm")
	if err != nil {
		t.Fatalf("confirm quantity reservation: %v", err)
	}
	if confirmStatus != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, confirmStatus, string(confirmBody))
	}

	seatEventID, seatIDs := createSeatInventoryForTest(t, client, 1)
	seatStatus, seatBody, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        seatEventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("consistency-seat-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve seat: %v", err)
	}
	if seatStatus != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, seatStatus, string(seatBody))
	}
	seatReservation := decodeJSONBytes[seatReservationResponse](t, seatBody)

	cancelStatus, cancelBody, err := postReservationActionHTTP(client, seatReservation.ReservationID, "cancel")
	if err != nil {
		t.Fatalf("cancel seat reservation: %v", err)
	}
	if cancelStatus != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, cancelStatus, string(cancelBody))
	}

	assertConsistencyOK(t, client)
}
