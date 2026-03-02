package gmail

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

// apiLimiter wraps a rate.Limiter for Gmail API calls with retry on rate limit errors.
type apiLimiter struct {
	limiter    *rate.Limiter
	maxRetries int
}

// newAPILimiter creates a limiter that allows ~200 requests/sec (12,000/min),
// safely under Gmail's 15,000 queries/min/user quota.
func newAPILimiter() *apiLimiter {
	return &apiLimiter{
		limiter:    rate.NewLimiter(rate.Limit(200), 10), // 200/s with burst of 10
		maxRetries: 5,
	}
}

// do executes fn with rate limiting and retries on rate limit errors.
func (a *apiLimiter) do(ctx context.Context, fn func() error) error {
	for attempt := 0; attempt <= a.maxRetries; attempt++ {
		if err := a.limiter.Wait(ctx); err != nil {
			return fmt.Errorf("rate limiter: %w", err)
		}

		err := fn()
		if err == nil {
			return nil
		}

		if !isRateLimitError(err) {
			return err
		}

		// Exponential backoff: 1s, 2s, 4s, 8s, 16s
		backoff := time.Duration(math.Pow(2, float64(attempt))) * time.Second
		fmt.Printf("\nRate limited, retrying in %v (attempt %d/%d)...\n", backoff, attempt+1, a.maxRetries)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("exceeded max retries due to rate limiting")
}

// isRateLimitError checks if the error is a Gmail rate limit error.
func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "rateLimitExceeded") ||
		strings.Contains(s, "Rate Limit Exceeded") ||
		strings.Contains(s, "RATE_LIMIT_EXCEEDED") ||
		strings.Contains(s, "Quota exceeded") ||
		strings.Contains(s, "userRateLimitExceeded")
}
