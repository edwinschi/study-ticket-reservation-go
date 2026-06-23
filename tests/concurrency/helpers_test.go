package concurrency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/modules/reservations"
	backgroundworker "ticket-reservation-go-lab/internal/worker"
)

type eventResponse struct {
	ID string `json:"id"`
}

type ticketTypeResponse struct {
	ID string `json:"id"`
}

type seatResponse struct {
	ID string `json:"id"`
}

type quantityReservationPayload struct {
	EventID        string `json:"event_id"`
	TicketTypeID   string `json:"ticket_type_id"`
	Quantity       int32  `json:"quantity"`
	IdempotencyKey string `json:"idempotency_key"`
}

type quantityReservationResponse struct {
	ReservationID string `json:"reservation_id"`
}

type seatReservationPayload struct {
	EventID        string   `json:"event_id"`
	SeatIDs        []string `json:"seat_ids"`
	IdempotencyKey string   `json:"idempotency_key"`
}

type seatReservationResponse struct {
	ReservationID string `json:"reservation_id"`
}

type httpResult struct {
	StatusCode int
	Body       []byte
	Err        error
}

func apiBaseURL() string {
	value := os.Getenv("API_BASE_URL")
	if value == "" {
		return "http://localhost:8080"
	}
	return value
}

func newHTTPClient(t *testing.T) *http.Client {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("create cookie jar: %v", err)
	}

	return &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
	}
}

// openTestPool gives tests direct read access to PostgreSQL.
//
// The API remains the system under test for writes, but direct reads make final invariants precise
// and avoid adding debug-only endpoints just for tests.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DATABASE_URL is not set; integration database is unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open postgres pool: %v", err)
	}

	t.Cleanup(pool.Close)
	return pool
}

func postJSON(t *testing.T, client *http.Client, path string, payload any) *http.Response {
	t.Helper()

	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}

	request, err := http.NewRequest(http.MethodPost, apiBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return response
}

func getJSON(t *testing.T, client *http.Client, path string) *http.Response {
	t.Helper()

	request, err := http.NewRequest(http.MethodGet, apiBaseURL()+path, nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return response
}

func decodeBody[T any](t *testing.T, response *http.Response) T {
	t.Helper()
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	return decodeJSONBytes[T](t, body)
}

func decodeJSONBytes[T any](t *testing.T, body []byte) T {
	t.Helper()

	var payload T
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode response body %q: %v", string(body), err)
	}
	return payload
}

func createAnonymousSession(t *testing.T, client *http.Client) {
	t.Helper()

	response := postJSON(t, client, "/v1/sessions/anonymous", map[string]string{})
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, response.StatusCode, string(body))
	}
	_ = decodeBody[struct {
		VisitorSessionID string `json:"visitor_session_id"`
	}](t, response)
}

func createEvent(t *testing.T, client *http.Client) string {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Second)
	response := postJSON(t, client, "/v1/events", map[string]any{
		"name":      fmt.Sprintf("Concurrency Event %d", time.Now().UnixNano()),
		"starts_at": now.Add(24 * time.Hour),
		"ends_at":   now.Add(27 * time.Hour),
	})
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, response.StatusCode, string(body))
	}
	event := decodeBody[eventResponse](t, response)
	return event.ID
}

func createQuantityInventory(t *testing.T, client *http.Client, totalQuantity int32) (string, string) {
	t.Helper()

	eventID := createEvent(t, client)
	response := postJSON(t, client, "/v1/events/"+eventID+"/ticket-types", map[string]any{
		"name":           "Concurrency Inventory",
		"total_quantity": totalQuantity,
	})
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, response.StatusCode, string(body))
	}
	ticketType := decodeBody[ticketTypeResponse](t, response)
	return eventID, ticketType.ID
}

func createSeatInventory(t *testing.T, client *http.Client, seatCount int) (string, []string) {
	t.Helper()

	eventID := createEvent(t, client)
	seats := make([]map[string]string, 0, seatCount)
	for index := 0; index < seatCount; index++ {
		seats = append(seats, map[string]string{
			"section":     "A",
			"row_name":    "1",
			"seat_number": fmt.Sprintf("%03d", index+1),
		})
	}

	response := postJSON(t, client, "/v1/events/"+eventID+"/seats", map[string]any{
		"seats": seats,
	})
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, response.StatusCode, string(body))
	}

	payload := decodeBody[struct {
		Seats []seatResponse `json:"seats"`
	}](t, response)

	seatIDs := make([]string, 0, len(payload.Seats))
	for _, seat := range payload.Seats {
		seatIDs = append(seatIDs, seat.ID)
	}
	return eventID, seatIDs
}

func reserveQuantity(client *http.Client, payload quantityReservationPayload) httpResult {
	return postRawJSON(client, "/v1/reservations/quantity", payload)
}

func reserveSeats(client *http.Client, payload seatReservationPayload) httpResult {
	return postRawJSON(client, "/v1/reservations/seats", payload)
}

func postReservationAction(client *http.Client, reservationID string, action string) httpResult {
	return postRawJSON(client, "/v1/reservations/"+reservationID+"/"+action, map[string]string{})
}

// postRawJSON returns errors through httpResult instead of failing the test immediately.
//
// Goroutines cannot safely call t.Fatalf after the parent test has moved on. Collecting results
// through channels lets the parent goroutine decide how to fail the test.
func postRawJSON(client *http.Client, path string, payload any) httpResult {
	body, err := json.Marshal(payload)
	if err != nil {
		return httpResult{Err: err}
	}

	request, err := http.NewRequest(http.MethodPost, apiBaseURL()+path, bytes.NewReader(body))
	if err != nil {
		return httpResult{Err: err}
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return httpResult{Err: err}
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return httpResult{Err: err}
	}
	return httpResult{StatusCode: response.StatusCode, Body: responseBody}
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
		WHERE id = $1::uuid
		`,
		ticketTypeID,
	).Scan(&totalQuantity, &soldQuantity, &reservedQuantity)
	if err != nil {
		t.Fatalf("query ticket quantities: %v", err)
	}
	return totalQuantity, soldQuantity, reservedQuantity
}

func countQuantityReservations(t *testing.T, ticketTypeID string) int64 {
	t.Helper()

	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var count int64
	err := pool.QueryRow(
		ctx,
		`
		SELECT COUNT(*)
		FROM reservation_items
		WHERE ticket_type_id = $1::uuid
		`,
		ticketTypeID,
	).Scan(&count)
	if err != nil {
		t.Fatalf("count quantity reservations: %v", err)
	}
	return count
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

// markReservationsExpired moves test-created reservations just far enough into the past for the
// expiration worker to pick them up. It stays within the consistency endpoint's 60-second tolerance
// so tests do not create false-positive stale-expiration failures while they are running.
func markReservationsExpired(t *testing.T, reservationIDs []string) {
	t.Helper()

	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ten seconds is enough for the worker query, but below the 60s assert-consistency tolerance.
	expiredAt := time.Now().UTC().Add(-10 * time.Second)
	for _, reservationID := range reservationIDs {
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

// newExpirationWorker builds the real worker stack in-process.
//
// This keeps race tests fast and deterministic: they can run one batch immediately instead of
// waiting for the Docker worker's five-second loop.
func newExpirationWorker(t *testing.T, batchSize int32) *backgroundworker.ExpirationWorker {
	t.Helper()

	pool := openTestPool(t)
	queries := db.NewQueries(pool)
	repository := reservations.NewRepository(pool, queries)
	service := reservations.NewService(repository, 15*time.Minute)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return backgroundworker.NewExpirationWorker(logger, service, time.Hour, batchSize)
}

func runExpirationBatch(t *testing.T, batchSize int32) int {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	processed, err := newExpirationWorker(t, batchSize).ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("process expiration batch: %v", err)
	}
	return processed
}

// assertConsistencyOK is the final invariant check used by every concurrency test.
//
// Passing individual status-code assertions is not enough; this endpoint verifies that no hidden
// database invariant was violated under load.
func assertConsistencyOK(t *testing.T, client *http.Client) {
	t.Helper()

	type consistencyPayload struct {
		OK      bool `json:"ok"`
		Details []struct {
			Check   string         `json:"check"`
			Message string         `json:"message"`
			Data    map[string]any `json:"data"`
		} `json:"details"`
	}

	var lastDetails []struct {
		Check   string         `json:"check"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	}

	for attempt := 0; attempt < 10; attempt++ {
		/*
			The Docker worker may be processing the same expired rows at the same time as the
			test helper. Because production code uses SKIP LOCKED, this helper can temporarily
			skip rows locked by the real worker. A short retry loop avoids treating that safe
			coordination as a consistency failure.
		*/
		_ = runExpirationBatch(t, 10000)

		response := getJSON(t, client, "/v1/admin/stress/assert-consistency")
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, response.StatusCode, string(body))
		}

		payload := decodeBody[consistencyPayload](t, response)
		if payload.OK {
			return
		}
		lastDetails = payload.Details
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("expected database consistency, got details=%+v", lastDetails)
}
