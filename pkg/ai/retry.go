package ai

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// withRetry executes fn up to aiRetryAttempts times with exponential backoff
// (base, base*2, base*4, …). It aborts immediately if the context is
// cancelled. Transient upstream errors get a second chance; permanent ones
// still surface to the caller after the final attempt.
//
// Knobs (aiRetryAttempts, aiRetryBaseDelay) come from LoadEnvConfig() —
// reading them here rather than at package init ensures .env values applied
// by godotenv.Load() in main() take effect.
func withRetry(ctx context.Context, fn func() error) error {
	var lastErr error
	for i := 0; i < aiRetryAttempts; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}

		if i == aiRetryAttempts-1 {
			break
		}

		delay := aiRetryBaseDelay * (1 << i)
		slog.Warn("AI call failed — retrying",
			"attempt", i+1, "max", aiRetryAttempts,
			"delay_ms", delay.Milliseconds(), "error", err.Error())

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}
