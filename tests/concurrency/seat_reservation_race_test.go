package concurrency_test

import (
	"fmt"
	"math/rand"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestSeatReservationRaceDoesNotDuplicateActiveSeats stresses random seat contention.
//
// The important assertion is not that every request succeeds; it is that no physical seat ends
// with two active rows after many goroutines race through the API.
func TestSeatReservationRaceDoesNotDuplicateActiveSeats(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSession(t, client)
	eventID, seatIDs := createSeatInventory(t, client, 10)

	const requestCount = 300
	choices := make([]string, requestCount)
	for index := 0; index < len(seatIDs); index++ {
		choices[index] = seatIDs[index]
	}
	random := rand.New(rand.NewSource(20240623))
	for index := len(seatIDs); index < requestCount; index++ {
		choices[index] = seatIDs[random.Intn(len(seatIDs))]
	}

	statusCodes := make(chan int, requestCount)
	errorsCh := make(chan error, requestCount)

	var wg sync.WaitGroup
	wg.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		index := index
		go func() {
			defer wg.Done()

			/*
				Every request uses a different idempotency key and competes for one seat.
				The ordered row locks plus the partial unique index must allow only one
				active reservation per physical seat.
			*/
			result := reserveSeats(client, seatReservationPayload{
				EventID:        eventID,
				SeatIDs:        []string{choices[index]},
				IdempotencyKey: fmt.Sprintf("seat-strong-race-%d-%d", time.Now().UnixNano(), index),
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
			t.Fatalf("concurrent seat request failed: %v", err)
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

	if successes > len(seatIDs) {
		t.Fatalf("expected at most %d successes, got %d", len(seatIDs), successes)
	}
	if successes+conflicts != requestCount {
		t.Fatalf("expected %d completed requests, got successes=%d conflicts=%d", requestCount, successes, conflicts)
	}

	activeTotal := int64(0)
	for seatID, count := range activeSeatReservationCounts(t, seatIDs) {
		if count > 1 {
			t.Fatalf("seat %s has %d active reservations", seatID, count)
		}
		activeTotal += count
	}
	if activeTotal != int64(successes) {
		t.Fatalf("expected active reservations=%d, got %d", successes, activeTotal)
	}
	assertConsistencyOK(t, client)
}

// TestSameSeatReservationRaceAllowsOnlyOneWinner targets the smallest possible seat race.
//
// If the partial unique index or transaction logic regresses, this test is likely to expose it
// because all goroutines compete for exactly the same seat.
func TestSameSeatReservationRaceAllowsOnlyOneWinner(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSession(t, client)
	eventID, seatIDs := createSeatInventory(t, client, 1)

	const requestCount = 200
	statusCodes := make(chan int, requestCount)
	errorsCh := make(chan error, requestCount)

	var wg sync.WaitGroup
	wg.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		index := index
		go func() {
			defer wg.Done()

			/*
				This is the most direct conflict case: all goroutines target the same seat.
				Exactly one can create an active hold; all others should receive 409 Conflict.
			*/
			result := reserveSeats(client, seatReservationPayload{
				EventID:        eventID,
				SeatIDs:        []string{seatIDs[0]},
				IdempotencyKey: fmt.Sprintf("same-seat-race-%d-%d", time.Now().UnixNano(), index),
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
			t.Fatalf("same-seat request failed: %v", err)
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
	if successes != 1 || conflicts != 199 {
		t.Fatalf("expected 1 success and 199 conflicts, got successes=%d conflicts=%d", successes, conflicts)
	}
	if count := activeSeatReservationCounts(t, seatIDs)[seatIDs[0]]; count != 1 {
		t.Fatalf("expected one active reservation for the seat, got %d", count)
	}
	assertConsistencyOK(t, client)
}
