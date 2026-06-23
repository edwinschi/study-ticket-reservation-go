package worker

import (
	"context"
	"log/slog"
	"time"
)

type ExpirationService interface {
	ExpireExpiredReservations(ctx context.Context, batchSize int32) (int, error)
}

type ExpirationWorker struct {
	logger    *slog.Logger
	service   ExpirationService
	interval  time.Duration
	batchSize int32
}

func NewExpirationWorker(
	logger *slog.Logger,
	service ExpirationService,
	interval time.Duration,
	batchSize int32,
) *ExpirationWorker {
	return &ExpirationWorker{
		logger:    logger,
		service:   service,
		interval:  interval,
		batchSize: batchSize,
	}
}

// ProcessOnce runs one expiration batch.
//
// Tests call this method directly so they can verify worker behavior without sleeping for the
// normal loop interval.
func (w *ExpirationWorker) ProcessOnce(ctx context.Context) (int, error) {
	return w.service.ExpireExpiredReservations(ctx, w.batchSize)
}

// Run executes expiration batches until the context is cancelled.
//
// The context is connected to process signals in cmd/worker, so Docker shutdown stops the loop
// cleanly instead of interrupting a transaction midway.
func (w *ExpirationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		/*
			The worker is intentionally small: it delegates the database transaction to the
			reservation service. That keeps HTTP handlers and background jobs using the same
			stock-release rules instead of maintaining two subtly different implementations.
		*/
		processed, err := w.ProcessOnce(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			w.logger.Error("expiration batch failed", "error", err)
		} else if processed > 0 {
			w.logger.Info("expired reservations processed", "count", processed)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
