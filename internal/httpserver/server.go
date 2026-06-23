package httpserver

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

func NewRouter(logger *slog.Logger) chi.Router {
	router := chi.NewRouter()

	router.Use(Recoverer(logger))
	router.Use(RequestID)
	router.Use(RequestLogger(logger))

	return router
}

func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
