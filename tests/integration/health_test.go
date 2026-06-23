package integration_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ticket-reservation-go-lab/internal/modules/health"

	"github.com/go-chi/chi/v5"
)

type fakePinger struct {
	err error
}

func (p fakePinger) Ping(_ context.Context) error {
	return p.err
}

func newHealthTestServer(postgresErr error, redisErr error) http.Handler {
	router := chi.NewRouter()
	handler := health.NewHandler(
		fakePinger{err: postgresErr},
		fakePinger{err: redisErr},
		time.Second,
	)
	handler.RegisterRoutes(router)
	return router
}

func TestHealthReturnsOK(t *testing.T) {
	server := newHealthTestServer(nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
}

func TestReadyReturnsOKWhenDependenciesAreReachable(t *testing.T) {
	server := newHealthTestServer(nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
}

func TestReadyReturnsUnavailableWhenPostgresFails(t *testing.T) {
	server := newHealthTestServer(errors.New("postgres down"), nil)

	request := httptest.NewRequest(http.MethodGet, "/ready", nil)
	response := httptest.NewRecorder()

	server.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, response.Code)
	}
}
