package concurrency_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestQuantityReservationRaceAllowsOnlyAvailableStock proves that the atomic UPDATE, not Go code,
// is the source of truth for quantity availability under heavy concurrency.
func TestQuantityReservationRaceAllowsOnlyAvailableStock(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSession(t, client)
	eventID, ticketTypeID := createQuantityInventory(t, client, 50)

	const requestCount = 500
	statusCodes := make(chan int, requestCount)
	errorsCh := make(chan error, requestCount)

	var wg sync.WaitGroup
	wg.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		index := index
		go func() {
			defer wg.Done()

			/*
				Each goroutine uses a unique idempotency key. This forces PostgreSQL to decide
				availability through the atomic UPDATE instead of collapsing requests as retries.
			*/
			result := reserveQuantity(client, quantityReservationPayload{
				EventID:        eventID,
				TicketTypeID:   ticketTypeID,
				Quantity:       1,
				IdempotencyKey: fmt.Sprintf("quantity-strong-race-%d-%d", time.Now().UnixNano(), index),
			})
			if result.Err != nil {
				errorsCh <- result.Err
				return
			}
			if result.StatusCode == http.StatusInternalServerError {
				errorsCh <- fmt.Errorf("unexpected 500 body=%s", string(result.Body))
				return
			}
			statusCodes <- result.StatusCode
		}()
	}
	wg.Wait()
	close(statusCodes)
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent quantity request failed: %v", err)
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

	if successes != 50 {
		t.Fatalf("expected 50 successes, got %d", successes)
	}
	if conflicts != 450 {
		t.Fatalf("expected 450 conflicts, got %d", conflicts)
	}

	total, sold, reserved := ticketQuantities(t, ticketTypeID)
	if total != 50 || sold != 0 || reserved != 50 {
		t.Fatalf("expected total=50 sold=0 reserved=50, got total=%d sold=%d reserved=%d", total, sold, reserved)
	}
	assertConsistencyOK(t, client)
}

// TestQuantityReservationRaceWithSameIdempotencyKeyCreatesOneReservation proves retry safety.
//
// All goroutines send the same logical operation. The expected result is many successful HTTP
// responses, but only one real reservation row and one unit of reserved stock.
func TestQuantityReservationRaceWithSameIdempotencyKeyCreatesOneReservation(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSession(t, client)
	eventID, ticketTypeID := createQuantityInventory(t, client, 50)

	const requestCount = 100
	idempotencyKey := fmt.Sprintf("quantity-same-key-%d", time.Now().UnixNano())
	statusCodes := make(chan int, requestCount)
	errorsCh := make(chan error, requestCount)

	var wg sync.WaitGroup
	wg.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		go func() {
			defer wg.Done()

			/*
				These goroutines represent client retries for the same logical operation.
				The unique (visitor_session_id, idempotency_key) constraint should collapse them
				into one real reservation while every retry receives the existing reservation.
			*/
			result := reserveQuantity(client, quantityReservationPayload{
				EventID:        eventID,
				TicketTypeID:   ticketTypeID,
				Quantity:       1,
				IdempotencyKey: idempotencyKey,
			})
			if result.Err != nil {
				errorsCh <- result.Err
				return
			}
			if result.StatusCode == http.StatusInternalServerError {
				errorsCh <- fmt.Errorf("unexpected 500 body=%s", string(result.Body))
				return
			}
			statusCodes <- result.StatusCode
		}()
	}
	wg.Wait()
	close(statusCodes)
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("idempotent quantity request failed: %v", err)
		}
	}
	for statusCode := range statusCodes {
		if statusCode != http.StatusCreated {
			t.Fatalf("expected idempotent retries to return 201, got %d", statusCode)
		}
	}

	_, sold, reserved := ticketQuantities(t, ticketTypeID)
	if sold != 0 || reserved != 1 {
		t.Fatalf("expected sold=0 reserved=1, got sold=%d reserved=%d", sold, reserved)
	}
	if realReservations := countQuantityReservations(t, ticketTypeID); realReservations != 1 {
		t.Fatalf("expected one real reservation item, got %d", realReservations)
	}
	assertConsistencyOK(t, client)
}
