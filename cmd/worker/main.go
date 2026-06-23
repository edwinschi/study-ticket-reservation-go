package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"ticket-reservation-go-lab/internal/config"
	"ticket-reservation-go-lab/internal/db"
	"ticket-reservation-go-lab/internal/modules/reservations"
	backgroundworker "ticket-reservation-go-lab/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration error", "error", err)
		os.Exit(1)
	}
	logger = config.ConfigureLogger(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startupCtx, cancelStartup := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancelStartup()

	postgresPool, err := db.OpenPostgres(startupCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("worker postgres connection failed", "error", err)
		os.Exit(1)
	}
	defer postgresPool.Close()

	redisClient, err := db.OpenRedis(startupCtx, db.RedisConfig{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	if err != nil {
		logger.Error("worker redis connection failed", "error", err)
		os.Exit(1)
	}
	defer redisClient.Close()

	queries := db.NewQueries(postgresPool)
	reservationRepository := reservations.NewRepository(postgresPool, queries)
	reservationService := reservations.NewService(reservationRepository, cfg.ReservationTTL)
	expirationWorker := backgroundworker.NewExpirationWorker(
		logger,
		reservationService,
		cfg.ExpirationWorkerInterval,
		cfg.ExpirationWorkerBatchSize,
	)

	logger.Info(
		"expiration worker started",
		"interval", cfg.ExpirationWorkerInterval.String(),
		"batch_size", cfg.ExpirationWorkerBatchSize,
	)
	if err := expirationWorker.Run(ctx); err != nil {
		logger.Error("expiration worker failed", "error", err)
		os.Exit(1)
	}
	logger.Info("worker shutting down")
}
