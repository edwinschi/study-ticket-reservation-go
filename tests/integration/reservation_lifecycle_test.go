package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/modules/reservations"
	backgroundworker "ticket-reservation-go-lab/internal/worker"
)

type reservationLifecycleResponse struct {
	ReservationID   string    `json:"reservation_id"`
	Status          string    `json:"status"`
	ReservationType string    `json:"reservation_type"`
	ExpiresAt       time.Time `json:"expires_at"`
	Items           []struct {
		TicketTypeID string `json:"ticket_type_id"`
		Quantity     int32  `json:"quantity"`
		Status       string `json:"status"`
	} `json:"items"`
	Seats []struct {
		SeatID string `json:"seat_id"`
		Status string `json:"status"`
	} `json:"seats"`
}

func decodeJSONBytes[T any](t *testing.T, body []byte) T {
	t.Helper()

	var payload T
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response body %q: %v", string(body), err)
	}
	return payload
}

func getReservationHTTP(client *http.Client, reservationID string) (int, []byte, error) {
	request, err := http.NewRequest(
		http.MethodGet,
		apiBaseURL()+"/v1/reservations/"+reservationID,
		nil,
	)
	if err != nil {
		return 0, nil, err
	}

	response, err := client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, body, nil
}

func postReservationActionHTTP(
	client *http.Client,
	reservationID string,
	action string,
) (int, []byte, error) {
	request, err := http.NewRequest(
		http.MethodPost,
		apiBaseURL()+"/v1/reservations/"+reservationID+"/"+action,
		bytes.NewReader([]byte("{}")),
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

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, body, nil
}

func markReservationExpiredForTest(t *testing.T, reservationID string) {
	t.Helper()

	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	expiredAt := time.Now().UTC().Add(-time.Minute)
	if _, err := pool.Exec(
		ctx,
		`
		UPDATE reservations
		SET expires_at = $2
		WHERE id = $1::uuid
		`,
		reservationID,
		expiredAt,
	); err != nil {
		t.Fatalf("mark reservation expired: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`
		UPDATE reservation_seats
		SET expires_at = $2
		WHERE reservation_id = $1::uuid
		`,
		reservationID,
		expiredAt,
	); err != nil {
		t.Fatalf("mark reservation seats expired: %v", err)
	}
}

func reservationStatus(t *testing.T, reservationID string) string {
	t.Helper()

	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var status string
	err := pool.QueryRow(
		ctx,
		`
		SELECT status
		FROM reservations
		WHERE id = $1::uuid
		`,
		reservationID,
	).Scan(&status)
	if err != nil {
		t.Fatalf("query reservation status: %v", err)
	}
	return status
}

func countReservationsWithStatus(t *testing.T, reservationIDs []string, status string) int {
	t.Helper()

	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	count := 0
	for _, reservationID := range reservationIDs {
		var matches bool
		err := pool.QueryRow(
			ctx,
			`
			SELECT status = $2
			FROM reservations
			WHERE id = $1::uuid
			`,
			reservationID,
			status,
		).Scan(&matches)
		if err != nil {
			t.Fatalf("query reservation %s status: %v", reservationID, err)
		}
		if matches {
			count++
		}
	}
	return count
}

func newExpirationWorkerForTest(t *testing.T, batchSize int32) *backgroundworker.ExpirationWorker {
	t.Helper()

	pool := openTestPool(t)
	queries := db.NewQueries(pool)
	repository := reservations.NewRepository(pool, queries)
	service := reservations.NewService(repository, 15*time.Minute)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return backgroundworker.NewExpirationWorker(logger, service, time.Hour, batchSize)
}

func expireOnceForTest(t *testing.T, batchSize int32) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	processed, err := newExpirationWorkerForTest(t, batchSize).ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("process expiration batch: %v", err)
	}
	return processed
}

func TestGetReservationReturnsCurrentSessionReservation(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 5)

	statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       1,
		IdempotencyKey: fmt.Sprintf("get-reservation-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}
	reservation := decodeJSONBytes[quantityReservationResponse](t, body)

	getStatus, getBody, err := getReservationHTTP(client, reservation.ReservationID)
	if err != nil {
		t.Fatalf("get reservation: %v", err)
	}
	if getStatus != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, getStatus, string(getBody))
	}
	found := decodeJSONBytes[reservationLifecycleResponse](t, getBody)
	if found.ReservationID != reservation.ReservationID || found.Status != "reserved" {
		t.Fatalf("unexpected reservation response: %+v", found)
	}
}

func TestCancelQuantityReservationReleasesStock(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 5)

	statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       2,
		IdempotencyKey: fmt.Sprintf("cancel-quantity-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}
	reservation := decodeJSONBytes[quantityReservationResponse](t, body)

	cancelStatus, cancelBody, err := postReservationActionHTTP(client, reservation.ReservationID, "cancel")
	if err != nil {
		t.Fatalf("cancel reservation: %v", err)
	}
	if cancelStatus != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, cancelStatus, string(cancelBody))
	}
	cancelled := decodeJSONBytes[reservationLifecycleResponse](t, cancelBody)
	if cancelled.Status != "cancelled" || len(cancelled.Items) != 1 || cancelled.Items[0].Status != "cancelled" {
		t.Fatalf("unexpected cancelled response: %+v", cancelled)
	}

	// Repeating cancel must not release stock again.
	secondStatus, secondBody, err := postReservationActionHTTP(client, reservation.ReservationID, "cancel")
	if err != nil {
		t.Fatalf("cancel reservation again: %v", err)
	}
	if secondStatus != http.StatusOK {
		t.Fatalf("expected idempotent status %d, got %d body=%s", http.StatusOK, secondStatus, string(secondBody))
	}

	_, sold, reserved := ticketQuantities(t, ticketTypeID)
	if sold != 0 || reserved != 0 {
		t.Fatalf("expected sold=0 reserved=0, got sold=%d reserved=%d", sold, reserved)
	}
}

func TestConfirmQuantityReservationMovesReservedToSold(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 5)

	statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       2,
		IdempotencyKey: fmt.Sprintf("confirm-quantity-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}
	reservation := decodeJSONBytes[quantityReservationResponse](t, body)

	confirmStatus, confirmBody, err := postReservationActionHTTP(client, reservation.ReservationID, "confirm")
	if err != nil {
		t.Fatalf("confirm reservation: %v", err)
	}
	if confirmStatus != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, confirmStatus, string(confirmBody))
	}
	confirmed := decodeJSONBytes[reservationLifecycleResponse](t, confirmBody)
	if confirmed.Status != "confirmed" || len(confirmed.Items) != 1 || confirmed.Items[0].Status != "confirmed" {
		t.Fatalf("unexpected confirmed response: %+v", confirmed)
	}

	// Repeating confirm must not increment sold_quantity again.
	secondStatus, secondBody, err := postReservationActionHTTP(client, reservation.ReservationID, "confirm")
	if err != nil {
		t.Fatalf("confirm reservation again: %v", err)
	}
	if secondStatus != http.StatusOK {
		t.Fatalf("expected idempotent status %d, got %d body=%s", http.StatusOK, secondStatus, string(secondBody))
	}

	_, sold, reserved := ticketQuantities(t, ticketTypeID)
	if sold != 2 || reserved != 0 {
		t.Fatalf("expected sold=2 reserved=0, got sold=%d reserved=%d", sold, reserved)
	}
}

func TestExpireQuantityReservationReleasesStock(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 5)

	statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       2,
		IdempotencyKey: fmt.Sprintf("expire-quantity-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}
	reservation := decodeJSONBytes[quantityReservationResponse](t, body)
	markReservationExpiredForTest(t, reservation.ReservationID)

	_ = expireOnceForTest(t, 100)

	if status := reservationStatus(t, reservation.ReservationID); status != "expired" {
		t.Fatalf("expected reservation status expired, got %q", status)
	}
	_, sold, reserved := ticketQuantities(t, ticketTypeID)
	if sold != 0 || reserved != 0 {
		t.Fatalf("expected sold=0 reserved=0, got sold=%d reserved=%d", sold, reserved)
	}
}

func TestCancelSeatReservationReleasesSeat(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 1)

	statusCode, body, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("cancel-seat-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve seat: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}
	reservation := decodeJSONBytes[seatReservationResponse](t, body)

	cancelStatus, cancelBody, err := postReservationActionHTTP(client, reservation.ReservationID, "cancel")
	if err != nil {
		t.Fatalf("cancel seat reservation: %v", err)
	}
	if cancelStatus != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, cancelStatus, string(cancelBody))
	}

	counts := activeSeatReservationCounts(t, seatIDs)
	if counts[seatIDs[0]] != 0 {
		t.Fatalf("expected released seat, got active count %d", counts[seatIDs[0]])
	}

	secondStatus, secondBody, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("reserve-after-cancel-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve released seat: %v", err)
	}
	if secondStatus != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, secondStatus, string(secondBody))
	}
}

func TestConfirmSeatReservationKeepsSeatUnavailable(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 1)

	statusCode, body, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("confirm-seat-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve seat: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}
	reservation := decodeJSONBytes[seatReservationResponse](t, body)

	confirmStatus, confirmBody, err := postReservationActionHTTP(client, reservation.ReservationID, "confirm")
	if err != nil {
		t.Fatalf("confirm seat reservation: %v", err)
	}
	if confirmStatus != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, confirmStatus, string(confirmBody))
	}

	counts := activeSeatReservationCounts(t, seatIDs)
	if counts[seatIDs[0]] != 1 {
		t.Fatalf("expected confirmed seat to stay active, got active count %d", counts[seatIDs[0]])
	}

	secondStatus, secondBody, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("reserve-after-confirm-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve confirmed seat: %v", err)
	}
	if secondStatus != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, secondStatus, string(secondBody))
	}
}

func TestExpireSeatReservationReleasesSeat(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, seatIDs := createSeatInventoryForTest(t, client, 1)

	statusCode, body, err := reserveSeatsHTTP(client, seatReservationPayload{
		EventID:        eventID,
		SeatIDs:        []string{seatIDs[0]},
		IdempotencyKey: fmt.Sprintf("expire-seat-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve seat: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}
	reservation := decodeJSONBytes[seatReservationResponse](t, body)
	markReservationExpiredForTest(t, reservation.ReservationID)

	_ = expireOnceForTest(t, 100)

	if status := reservationStatus(t, reservation.ReservationID); status != "expired" {
		t.Fatalf("expected reservation status expired, got %q", status)
	}
	counts := activeSeatReservationCounts(t, seatIDs)
	if counts[seatIDs[0]] != 0 {
		t.Fatalf("expected expired seat to be released, got active count %d", counts[seatIDs[0]])
	}
}

func TestTwoExpirationWorkersDoNotProcessSameReservationIncorrectly(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 10)

	reservationIDs := make([]string, 0, 5)
	for index := 0; index < 5; index++ {
		statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
			EventID:        eventID,
			TicketTypeID:   ticketTypeID,
			Quantity:       1,
			IdempotencyKey: fmt.Sprintf("two-workers-%d-%d", time.Now().UnixNano(), index),
		})
		if err != nil {
			t.Fatalf("reserve quantity %d: %v", index, err)
		}
		if statusCode != http.StatusCreated {
			t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
		}
		reservation := decodeJSONBytes[quantityReservationResponse](t, body)
		reservationIDs = append(reservationIDs, reservation.ReservationID)
		markReservationExpiredForTest(t, reservation.ReservationID)
	}

	workerA := newExpirationWorkerForTest(t, 100)
	workerB := newExpirationWorkerForTest(t, 100)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	for _, expirationWorker := range []*backgroundworker.ExpirationWorker{workerA, workerB} {
		expirationWorker := expirationWorker
		go func() {
			defer wg.Done()
			if _, err := expirationWorker.ProcessOnce(ctx); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("expiration worker failed: %v", err)
		}
	}

	if expiredCount := countReservationsWithStatus(t, reservationIDs, "expired"); expiredCount != len(reservationIDs) {
		t.Fatalf("expected %d expired reservations, got %d", len(reservationIDs), expiredCount)
	}
	_, sold, reserved := ticketQuantities(t, ticketTypeID)
	if sold != 0 || reserved != 0 {
		t.Fatalf("expected sold=0 reserved=0, got sold=%d reserved=%d", sold, reserved)
	}
}

func TestConcurrentConfirmQuantityReservationDoesNotDuplicateSoldQuantity(t *testing.T) {
	client := newHTTPClient(t)
	createAnonymousSessionForTest(t, client)
	eventID, ticketTypeID := createQuantityInventoryForTest(t, client, 1)

	statusCode, body, err := reserveQuantityHTTP(client, quantityReservationPayload{
		EventID:        eventID,
		TicketTypeID:   ticketTypeID,
		Quantity:       1,
		IdempotencyKey: fmt.Sprintf("confirm-race-%d", time.Now().UnixNano()),
	})
	if err != nil {
		t.Fatalf("reserve quantity: %v", err)
	}
	if statusCode != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, statusCode, string(body))
	}
	reservation := decodeJSONBytes[quantityReservationResponse](t, body)

	const requestCount = 100
	statusCodes := make(chan int, requestCount)
	errorsCh := make(chan error, requestCount)

	var wg sync.WaitGroup
	wg.Add(requestCount)
	for index := 0; index < requestCount; index++ {
		go func() {
			defer wg.Done()
			statusCode, responseBody, err := postReservationActionHTTP(client, reservation.ReservationID, "confirm")
			if err != nil {
				errorsCh <- err
				return
			}
			if statusCode == http.StatusInternalServerError {
				errorsCh <- fmt.Errorf("unexpected 500 body=%s", string(responseBody))
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
			t.Fatalf("concurrent confirm failed: %v", err)
		}
	}
	for statusCode := range statusCodes {
		if statusCode != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, statusCode)
		}
	}

	total, sold, reserved := ticketQuantities(t, ticketTypeID)
	if total != 1 || sold != 1 || reserved != 0 {
		t.Fatalf("expected total=1 sold=1 reserved=0, got total=%d sold=%d reserved=%d", total, sold, reserved)
	}
}
