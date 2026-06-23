package concurrency_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestConcurrentConfirmSameQuantityReservationDoesNotDuplicateSoldQuantity verifies that confirm
// is idempotent even when 100 goroutines hit the same reservation at once.
func TestConcurrentConfirmSameQuantityReservationDoesNotDuplicateSoldQuantity(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSession(t, client)
	eventID, ticketTypeID := createQuantityInventory(t, client, 1)

	reserveResult := reserveQuantity(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       1,
		IdempotencyKey: fmt.Sprintf("confirm-race-%d", time.Now().UnixNano()),
	})
	if reserveResult.Err != nil {
		t.Fatalf("reserve quantity: %v", reserveResult.Err)
	}
	if reserveResult.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, reserveResult.StatusCode, string(reserveResult.Body))
	}
	reservation := decodeJSONBytes[quantityReservationResponse](t, reserveResult.Body)

	const requestCount = 100
	statusCodes := make(chan int, requestCount)
	errorsCh := make(chan error, requestCount)

	var wg sync.WaitGroup
	wg.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		go func() {
			defer wg.Done()

			/*
				All goroutines confirm the same reservation. The reservation row lock should make
				the first request move stock, while the rest observe the idempotent confirmed state.
			*/
			result := postReservationAction(client, reservation.ReservationID, "confirm")
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
			t.Fatalf("concurrent confirm failed: %v", err)
		}
	}
	for statusCode := range statusCodes {
		if statusCode != http.StatusOK {
			t.Fatalf("expected confirm status %d, got %d", http.StatusOK, statusCode)
		}
	}

	total, sold, reserved := ticketQuantities(t, ticketTypeID)
	if total != 1 || sold != 1 || reserved != 0 {
		t.Fatalf("expected total=1 sold=1 reserved=0, got total=%d sold=%d reserved=%d", total, sold, reserved)
	}
	assertConsistencyOK(t, client)
}

// TestConcurrentCancelSameQuantityReservationDoesNotReleaseStockTwice verifies that cancellation
// releases reserved stock exactly once, even with many concurrent cancel requests.
func TestConcurrentCancelSameQuantityReservationDoesNotReleaseStockTwice(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSession(t, client)
	eventID, ticketTypeID := createQuantityInventory(t, client, 1)

	reserveResult := reserveQuantity(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       1,
		IdempotencyKey: fmt.Sprintf("cancel-race-%d", time.Now().UnixNano()),
	})
	if reserveResult.Err != nil {
		t.Fatalf("reserve quantity: %v", reserveResult.Err)
	}
	if reserveResult.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, reserveResult.StatusCode, string(reserveResult.Body))
	}
	reservation := decodeJSONBytes[quantityReservationResponse](t, reserveResult.Body)

	const requestCount = 100
	statusCodes := make(chan int, requestCount)
	errorsCh := make(chan error, requestCount)

	var wg sync.WaitGroup
	wg.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		go func() {
			defer wg.Done()

			/*
				All goroutines cancel the same reservation. Only the first transition releases
				reserved_quantity; later calls return the already-cancelled reservation.
			*/
			result := postReservationAction(client, reservation.ReservationID, "cancel")
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
			t.Fatalf("concurrent cancel failed: %v", err)
		}
	}
	for statusCode := range statusCodes {
		if statusCode != http.StatusOK {
			t.Fatalf("expected cancel status %d, got %d", http.StatusOK, statusCode)
		}
	}

	total, sold, reserved := ticketQuantities(t, ticketTypeID)
	if total != 1 || sold != 0 || reserved != 0 {
		t.Fatalf("expected total=1 sold=0 reserved=0, got total=%d sold=%d reserved=%d", total, sold, reserved)
	}
	assertConsistencyOK(t, client)
}
