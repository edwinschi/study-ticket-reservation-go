package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"ticket-reservation-go-lab/internal/config"
	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/httpserver"
	"ticket-reservation-go-lab/internal/modules/auth"
	"ticket-reservation-go-lab/internal/modules/events"
	"ticket-reservation-go-lab/internal/modules/health"
	"ticket-reservation-go-lab/internal/modules/reservations"
	"ticket-reservation-go-lab/internal/modules/sessions"
	"ticket-reservation-go-lab/internal/modules/stressadmin"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}
	logger = config.ConfigureLogger(cfg.LogLevel)

	// signal.NotifyContext creates a root context that is cancelled on SIGINT/SIGTERM.
	// Passing this context down makes startup and shutdown code stop promptly when Docker
	// asks the container to exit.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startupCtx, cancelStartup := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancelStartup()

	postgresPool, err := db.OpenPostgres(startupCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("postgres connection failed", "error", err)
		os.Exit(1)
	}
	defer postgresPool.Close()

	redisClient, err := db.OpenRedis(startupCtx, db.RedisConfig{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	if err != nil {
		logger.Error("redis connection failed", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	queries := db.NewQueries(postgresPool)
	sessionRepository := sessions.NewRepository(queries)
	sessionService := sessions.NewService(
		sessionRepository,
		cfg.VisitorSessionTTL,
		cfg.CookieSecure,
	)
	authRepository := auth.NewRepository(queries)
	authService := auth.NewService(authRepository, sessionService)
	eventRepository := events.NewRepository(queries)
	eventService := events.NewService(eventRepository)
	reservationRepository := reservations.NewRepository(postgresPool, queries)
	reservationService := reservations.NewService(reservationRepository, cfg.ReservationTTL)
	stressAdminRepository := stressadmin.NewRepository(postgresPool, queries)
	stressAdminService := stressadmin.NewService(stressAdminRepository, cfg.AppEnv)

	router := httpserver.NewRouter(logger)
	health.NewHandler(
		postgresPool,
		db.RedisPinger{Client: redisClient},
		cfg.ReadinessTimeout,
	).RegisterRoutes(router)
	sessions.NewHandler(sessionService).RegisterRoutes(router)
	auth.NewHandler(authService, sessionService).RegisterRoutes(router)
	events.NewHandler(eventService).RegisterRoutes(router)
	reservations.NewHandler(reservationService, sessionService).RegisterRoutes(router)
	stressadmin.NewHandler(stressAdminService).RegisterRoutes(router)

	server := httpserver.NewServer(cfg.HTTPAddr, router)

	go func() {
		logger.Info("api server started", "addr", cfg.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("api server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("api server shutting down")

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("api server shutdown failed", "error", err)
		os.Exit(1)
	}
}
