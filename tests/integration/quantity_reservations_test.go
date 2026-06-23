package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"
)

type quantityReservationPayload struct {
	EventID        string `json:"event_id"`
	TicketTypeID   string `json:"ticket_type_id"`
	Quantity       int32  `json:"quantity"`
	IdempotencyKey string `json:"idempotency_key"`
}

type quantityReservationResponse struct {
	ReservationID   string    `json:"reservation_id"`
	Status          string    `json:"status"`
	ReservationType string    `json:"reservation_type"`
	ExpiresAt       time.Time `json:"expires_at"`
	Items           []struct {
		TicketTypeID string `json:"ticket_type_id"`
		Quantity     int32  `json:"quantity"`
	} `json:"items"`
}

func createQuantityInventoryForTest(
	t *testing.T,
	client *http.Client,
	totalQuantity int32,
) (string, string) {
	t.Helper()

	event := createEventForTest(t, client)
	ticketResponse := postJSON(t, client, "/v1/events/"+event.ID+"/ticket-types", map[string]any{
		"name":           "Race Inventory",
		"total_quantity": totalQuantity,
	})
	if ticketResponse.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, ticketResponse.StatusCode)
	}
	ticketType := decodeBody[ticketTypePayload](t, ticketResponse)
	return event.ID, ticketType.ID
}

func ticketQuantities(t *testing.T, ticketTypeID string) (int32, int32, int32) {
	t.Helper()

	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var totalQuantity int32
	var soldQuantity int32
	var reservedQuantity int32
	err := pool.QueryRow(
		ctx,
		`
		SELECT total_quantity, sold_quantity, reserved_quantity
		FROM ticket_types
		WHERE id = $1
		`,
		ticketTypeID,
	).Scan(&totalQuantity, &soldQuantity, &reservedQuantity)
	if err != nil {
		t.Fatalf("query ticket quantities: %v", err)
	}
	return totalQuantity, soldQuantity, reservedQuantity
}

func reserveQuantityHTTP(
	client *http.Client,
	payload quantityReservationPayload,
) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}

	request, err := http.NewRequest(
		http.MethodPost,
		apiBaseURL()+"/v1/reservations/quantity",
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, responseBody, nil
}

func TestReserveQuantitySuccess(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 10)

	statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       2,
		IdempotencyKey: fmt.Sprintf("quantity-success-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}

	var payload quantityReservationResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode reservation response: %v", err)
	}
	if payload.ReservationID == "" || payload.Status != "reserved" || payload.ReservationType != "quantity" {
		t.Fatalf("unexpected reservation response: %+v", payload)
	}
	if len(payload.Items) != 1 || payload.Items[0].Quantity != 2 {
		t.Fatalf("unexpected reservation items: %+v", payload.Items)
	}

	_, sold, reserved := ticketQuantities(t, ticketTypeID)
	if sold != 0 || reserved != 2 {
		t.Fatalf("expected sold=0 reserved=2, got sold=%d reserved=%d", sold, reserved)
	}
}

func TestReserveQuantityWithoutSessionReturnsUnauthorized(t *testing.T) {
	client := newHTTPClient(t)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 10)

	statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       1,
		IdempotencyKey: fmt.Sprintf("quantity-no-session-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if statusCode != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusUnauthorized, statusCode, string(body))
	}
}

func TestReserveQuantityOverAvailableReturnsConflict(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 1)

	statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       2,
		IdempotencyKey: fmt.Sprintf("quantity-conflict-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if statusCode != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, statusCode, string(body))
	}

	_, sold, reserved := ticketQuantities(t, ticketTypeID)
	if sold != 0 || reserved != 0 {
		t.Fatalf("expected sold=0 reserved=0, got sold=%d reserved=%d", sold, reserved)
	}
}

func TestReserveQuantityIdempotencyDoesNotDuplicateStock(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 10)
	payload := quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       2,
		IdempotencyKey: fmt.Sprintf("quantity-idempotent-%d", time.Now().UnixNano()),
	}

	firstStatus, firstBody, err := reserveQuantityHTTP(client, payload)
	if err != nil {
		t.Fatalf("reserve quantity first call: %v", err)
	}
	secondStatus, secondBody, err := reserveQuantityHTTP(client, payload)
	if err != nil {
		t.Fatalf("reserve quantity second call: %v", err)
	}

	if firstStatus != http.StatusCreated || secondStatus != http.StatusCreated {
		t.Fatalf("expected both calls to return 201, got %d and %d", firstStatus, secondStatus)
	}

	var first quantityReservationResponse
	var second quantityReservationResponse
	if err := json.Unmarshal(firstBody, &first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if err := json.Unmarshal(secondBody, &second); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if first.ReservationID != second.ReservationID {
		t.Fatalf("expected same reservation id, got %q and %q", first.ReservationID, second.ReservationID)
	}

	_, sold, reserved := ticketQuantities(t, ticketTypeID)
	if sold != 0 || reserved != 2 {
		t.Fatalf("expected sold=0 reserved=2, got sold=%d reserved=%d", sold, reserved)
	}
}

func TestReserveQuantityConcurrentRace(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 100)

	const requestCount = 500
	statusCodes := make(chan int, requestCount)
	errorsCh := make(chan error, requestCount)

	var wg sync.WaitGroup
	wg.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		index := index
		go func() {
			defer wg.Done()
			statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
				EventID:        eventID,
				TicketTypeID:   ticketTypeID,
				Quantity:       1,
				IdempotencyKey: fmt.Sprintf("quantity-race-%d-%d", time.Now().UnixNano(), index),
			})
			if err != nil {
				errorsCh <- err
				return
			}
			if statusCode == http.StatusInternalServerError {
				errorsCh <- fmt.Errorf("unexpected 500 body=%s", string(body))
				return
			}
			statusCodes <- statusCode
		}()
	}
	wg.Wait()
	close(statusCodes)
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent request failed: %v", err)
		}
	}

	successes := 0
	conflicts := 0
	for statusCode := range statusCodes {
		switch statusCode {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected status code: %d", statusCode)
		}
	}

	if successes != 100 {
		t.Fatalf("expected 100 successes, got %d", successes)
	}
	if conflicts != 400 {
		t.Fatalf("expected 400 conflicts, got %d", conflicts)
	}

	total, sold, reserved := ticketQuantities(t, ticketTypeID)
	if total != 100 || reserved != 100 {
		t.Fatalf("expected total=100 reserved=100, got total=%d reserved=%d", total, reserved)
	}
	if sold+reserved > total {
		t.Fatalf("oversold inventory: sold=%d reserved=%d total=%d", sold, reserved, total)
	}
}
