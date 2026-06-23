package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

type Handler struct {
	postgres         Pinger
	redis            Pinger
	readinessTimeout time.Duration
}

func NewHandler(postgres Pinger, redis Pinger, readinessTimeout time.Duration) *Handler {
	return &Handler{
		postgres:         postgres,
		redis:            redis,
		readinessTimeout: readinessTimeout,
	}
}

func (h *Handler) RegisterRoutes(router chi.Router) {
	router.Get("/health", h.Health)
	router.Get("/ready", h.Ready)
}

func (h *Handler) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	// Readiness uses the request context as its parent. If the client disconnects or the server
	// shuts down, PostgreSQL and Redis pings are cancelled instead of running in the background.
	ctx, cancel := context.WithTimeout(r.Context(), h.readinessTimeout)
	defer cancel()

	if err := h.postgres.Ping(ctx); err != nil {
		writeError(
			w,
			http.StatusServiceUnavailable,
			"POSTGRES_UNAVAILABLE",
			"PostgreSQL is unavailable",
		)
		return
	}

	if err := h.redis.Ping(ctx); err != nil {
		writeError(
			w,
			http.StatusServiceUnavailable,
			"REDIS_UNAVAILABLE",
			"Redis is unavailable",
		)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "ready",
		"postgres": "ok",
		"redis":    "ok",
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, code string, message string) {
	writeJSON(w, statusCode, map[string]map[string]string{
		"error": {
			"code":    code,
			"message": message,
		},
	})
}
