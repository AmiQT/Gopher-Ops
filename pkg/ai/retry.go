package ai

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"time"
)

// aiRetryAttempts is the total number of attempts (including the first).
var aiRetryAttempts = func() int {
	if v := os.Getenv("AI_RETRY_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 3
}()

// aiRetryBaseDelay is the initial backoff delay; doubles after each attempt.
var aiRetryBaseDelay = func() time.Duration {
	if v := os.Getenv("AI_RETRY_BASE_DELAY_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Millisecond
		}
	}
	return 1 * time.Second
}()

// withRetry executes fn up to aiRetryAttempts times with exponential backoff
// (base, base*2, base*4, …). It aborts immediately if the context is
// cancelled. Transient upstream errors get a second chance; permanent ones
// still surface to the caller after the final attempt.
func withRetry(ctx context.Context, fn func() error) error {
	var lastErr error
	for i := 0; i < aiRetryAttempts; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		// Don't burn retries if the caller already gave up.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}

		// Last attempt — return the error without sleeping.
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
