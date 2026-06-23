package concurrency_test

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	backgroundworker "ticket-reservation-go-lab/internal/worker"
)

// TestTwoExpirationWorkersProcessExpiredReservationsOnceLogically proves that SKIP LOCKED lets
// multiple workers cooperate without releasing the same reserved stock twice.
func TestTwoExpirationWorkersProcessExpiredReservationsOnceLogically(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSession(t, client)
	eventID, ticketTypeID := createQuantityInventory(t, client, 20)

	reservationIDs := make([]string, 0, 10)
	for index := 0; index < 10; index++ {
		result := reserveQuantity(client, quantityReservationPayload{
			EventID:        eventID,
			TicketTypeID:   ticketTypeID,
			Quantity:       1,
			IdempotencyKey: fmt.Sprintf("worker-race-%d-%d", time.Now().UnixNano(), index),
		})
		if result.Err != nil {
			t.Fatalf("reserve quantity %d: %v", index, result.Err)
		}
		if result.StatusCode != http.StatusCreated {
			t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, result.StatusCode, string(result.Body))
		}
		reservation := decodeJSONBytes[quantityReservationResponse](t, result.Body)
		reservationIDs = append(reservationIDs, reservation.ReservationID)
	}
	markReservationsExpired(t, reservationIDs)

	workerA := newExpirationWorker(t, 100)
	workerB := newExpirationWorker(t, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	processedCounts := make(chan int, 2)
	errorsCh := make(chan error, 2)

	var wg sync.WaitGroup
	wg.Add(2)
	for _, expirationWorker := range []*backgroundworker.ExpirationWorker{workerA, workerB} {
		expirationWorker := expirationWorker
		go func() {
			defer wg.Done()

			/*
				Both workers scan the same expired-reservation queue. FOR UPDATE SKIP LOCKED
				should make them split work or let one worker process all rows, but never apply
				the stock release twice for the same reservation.
			*/
			processed, err := expirationWorker.ProcessOnce(ctx)
			if err != nil {
				errorsCh <- err
				return
			}
			processedCounts <- processed
		}()
	}
	wg.Wait()
	close(processedCounts)
	close(errorsCh)

	for err := range errorsCh {
		if err != nil {
			t.Fatalf("expiration worker failed: %v", err)
		}
	}

	processedTotal := 0
	for processed := range processedCounts {
		processedTotal += processed
	}
	if processedTotal > len(reservationIDs) {
		t.Fatalf("workers reported processing %d reservations, but only %d were created", processedTotal, len(reservationIDs))
	}

	if expiredCount := countReservationsWithStatus(t, reservationIDs, "expired"); expiredCount != len(reservationIDs) {
		t.Fatalf("expected %d expired reservations, got %d", len(reservationIDs), expiredCount)
	}

	total, sold, reserved := ticketQuantities(t, ticketTypeID)
	if total != 20 || sold != 0 || reserved != 0 {
		t.Fatalf("expected total=20 sold=0 reserved=0, got total=%d sold=%d reserved=%d", total, sold, reserved)
	}
	assertConsistencyOK(t, client)
}
