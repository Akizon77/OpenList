package net

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"golang.org/x/time/rate"
)

type requestRateLimiterKey struct{}
type prepaidRequestKey struct{}

var ErrRequestRateLimitWait = errors.New("request rate limit wait failed")

type prepaidRequest struct {
	used atomic.Bool
}

// WithRequestRateLimit attaches a shared request limiter to ctx.
func WithRequestRateLimit(ctx context.Context, requestsPerSecond float64) context.Context {
	if requestsPerSecond <= 0 {
		return ctx
	}
	limiter := rate.NewLimiter(rate.Limit(requestsPerSecond), 1)
	return context.WithValue(ctx, requestRateLimiterKey{}, limiter)
}

// AcquireRequestPermit waits for one request slot and lets the first nested
// HTTP request consume that already-acquired slot.
func AcquireRequestPermit(ctx context.Context) (context.Context, error) {
	limiter, _ := ctx.Value(requestRateLimiterKey{}).(*rate.Limiter)
	if limiter == nil {
		return ctx, nil
	}
	if err := limiter.Wait(ctx); err != nil {
		return ctx, fmt.Errorf("%w: %w", ErrRequestRateLimitWait, err)
	}
	return context.WithValue(ctx, prepaidRequestKey{}, &prepaidRequest{}), nil
}

// WaitRequestRateLimit waits for one request slot unless the caller is using
// the first nested request covered by AcquireRequestPermit.
func WaitRequestRateLimit(ctx context.Context) error {
	if permit, _ := ctx.Value(prepaidRequestKey{}).(*prepaidRequest); permit != nil && permit.used.CompareAndSwap(false, true) {
		return nil
	}
	limiter, _ := ctx.Value(requestRateLimiterKey{}).(*rate.Limiter)
	if limiter == nil {
		return nil
	}
	if err := limiter.Wait(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrRequestRateLimitWait, err)
	}
	return nil
}
