package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"sync"
	"testing"
	"time"
)

type seatReservationPayload struct {
	EventID        string   `json:"event_id"`
	SeatIDs        []string `json:"seat_ids"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type seatReservationResponse struct {
	ReservationID   string    `json:"reservation_id"`
	Status          string    `json:"status"`
	ReservationType string    `json:"reservation_type"`
	ExpiresAt       time.Time `json:"expires_at"`
	Seats           []struct {
		SeatID string `json:"seat_id"`
	} `json:"seats"`
}

func createSeatInventoryForTest(t *testing.T, client *http.Client, seatCount int) (string, []string) {
	t.Helper()

	event := createEventForTest(t, client)
	seats := make([]map[string]string, 0, seatCount)
	for index := 0; index < seatCount; index++ {
		seats = append(seats, map[string]string{
			"section":     "A",
			"row_name":    "1",
			"seat_number": fmt.Sprintf("%03d", index+1),
		})
	}

	response := postJSON(t, client, "/v1/events/"+event.ID+"/seats", map[string]any{
		"seats": seats,
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d", http.StatusCreated, response.StatusCode)
	}

	payload := decodeBody[struct {
		Seats []seatPayload `json:"seats"`
	}](t, response)
	if len(payload.Seats) != seatCount {
		t.Fatalf("expected %d seats, got %d", seatCount, len(payload.Seats))
	}

	seatIDs := make([]string, 0, len(payload.Seats))
	for _, seat := range payload.Seats {
		seatIDs = append(seatIDs, seat.ID)
	}
	return event.ID, seatIDs
}

func reserveSeatsHTTP(client *http.Client, payload seatReservationPayload) (int, []byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}

	request, err := http.NewRequest(
		http.MethodPost,
		apiBaseURL()+"/v1/reservations/seats",
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

func activeSeatReservationCounts(t *testing.T, seatIDs []string) map[string]int64 {
	t.Helper()

	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	counts := make(map[string]int64, len(seatIDs))
	for _, seatID := range seatIDs {
		var count int64
		err := pool.QueryRow(
			ctx,
			`
			SELECT COUNT(*)
			FROM reservation_seats
			WHERE seat_id = $1::uuid
			  AND status IN ('reserved', 'confirmed')
			`,
			seatID,
		).Scan(&count)
		if err != nil {
			t.Fatalf("query active reservations for seat %s: %v", seatID, err)
		}
		counts[seatID] = count
	}
	return counts
}

func TestReserveSingleSeatSuccess(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 1)

	statusCode, body, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("seat-success-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve seat: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}

	var payload seatReservationResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode seat reservation response: %v", err)
	}
	if payload.ReservationID == "" || payload.Status != "reserved" || payload.ReservationType != "seats" {
		t.Fatalf("unexpected reservation response: %+v", payload)
	}
	if len(payload.Seats) != 1 || payload.Seats[0].SeatID != seatIDs[0] {
		t.Fatalf("unexpected reserved seats: %+v", payload.Seats)
	}
}

func TestReserveMultipleSeatsSuccess(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 3)

	statusCode, body, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0], seatIDs[1], seatIDs[2]},
		IdempotencyKey: fmt.Sprintf("seat-multi-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve seats: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}

	var payload seatReservationResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode seat reservation response: %v", err)
	}
	if len(payload.Seats) != 3 {
		t.Fatalf("expected 3 reserved seats, got %d", len(payload.Seats))
	}
}

func TestReserveAlreadyReservedSeatReturnsConflict(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 1)

	firstStatus, firstBody, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("seat-first-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve first seat: %v", err)
	}
	if firstStatus != http.StatusCreated {
		t.Fatalf("expected first status %d, got %d body=%s", http.StatusCreated, firstStatus, string(firstBody))
	}

	secondStatus, secondBody, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("seat-second-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve second seat: %v", err)
	}
	if secondStatus != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, secondStatus, string(secondBody))
	}
}

func TestReserveSeatsIdempotencyReturnsExistingReservation(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 1)
	payload := seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("seat-idempotent-%d", time.Now().UnixNano()),
	}

	firstStatus, firstBody, err := reserveSeatsHTTP(client, payload)
	if err != nil {
		t.Fatalf("reserve first seat call: %v", err)
	}
	secondStatus, secondBody, err := reserveSeatsHTTP(client, payload)
	if err != nil {
		t.Fatalf("reserve second seat call: %v", err)
	}
	if firstStatus != http.StatusCreated || secondStatus != http.StatusCreated {
		t.Fatalf("expected both calls to return 201, got %d and %d", firstStatus, secondStatus)
	}

	var first seatReservationResponse
	var second seatReservationResponse
	if err := json.Unmarshal(firstBody, &first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if err := json.Unmarshal(secondBody, &second); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if first.ReservationID != second.ReservationID {
		t.Fatalf("expected same reservation id, got %q and %q", first.ReservationID, second.ReservationID)
	}

	counts := activeSeatReservationCounts(t, seatIDs)
	if counts[seatIDs[0]] != 1 {
		t.Fatalf("expected one active seat reservation, got %d", counts[seatIDs[0]])
	}
}

func TestReserveSeatsConcurrentRace(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 20)

	const requestCount = 500
	choices := make([]string, requestCount)
	for index := 0; index < len(seatIDs); index++ {
		choices[index] = seatIDs[index]
	}
	random := rand.New(rand.NewSource(42))
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
			statusCode, body, err := reserveSeatsHTTP(client, seatReservationPayload{
				EventID:        eventID,
				SeatIDs:        []string{choices[index]},
				IdempotencyKey: fmt.Sprintf("seat-race-%d-%d", time.Now().UnixNano(), index),
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

	counts := activeSeatReservationCounts(t, seatIDs)
	activeTotal := int64(0)
	for seatID, count := range counts {
		if count > 1 {
			t.Fatalf("seat %s has %d active reservations", seatID, count)
		}
		activeTotal += count
	}
	if activeTotal != int64(successes) {
		t.Fatalf("expected active reservations=%d, got %d", successes, activeTotal)
	}
}

func TestReserveSeatsOrderedLockAvoidsRecurringDeadlock(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 2)

	payloadA := seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0], seatIDs[1]},
		IdempotencyKey: fmt.Sprintf("seat-deadlock-a-%d", time.Now().UnixNano()),
	}
	payloadB := seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[1], seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("seat-deadlock-b-%d", time.Now().UnixNano()),
	}

	statusCodes := make(chan int, 2)
	errorsCh := make(chan error, 2)

	var wg sync.WaitGroup
	wg.Add(2)
	for _, payload := range []seatReservationPayload{payloadA, payloadB} {
		payload := payload
		go func() {
			defer wg.Done()
			statusCode, body, err := reserveSeatsHTTP(client, payload)
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
			t.Fatalf("ordered-lock request failed: %v", err)
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
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one success and one conflict, got successes=%d conflicts=%d", successes, conflicts)
	}
}
