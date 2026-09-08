package net

import (
	"context"
	"testing"
	"time"
)

func TestAcquireRequestPermitPrepaysFirstRequest(t *testing.T) {
	ctx := WithRequestRateLimit(context.Background(), 1)
	requestCtx, err := AcquireRequestPermit(ctx)
	if err != nil {
		t.Fatalf("AcquireRequestPermit() error = %v", err)
	}

	requestCtx, cancel := context.WithTimeout(requestCtx, 10*time.Millisecond)
	defer cancel()
	if err := WaitRequestRateLimit(requestCtx); err != nil {
		t.Fatalf("first WaitRequestRateLimit() used no prepaid permit: %v", err)
	}
	if err := WaitRequestRateLimit(requestCtx); err == nil {
		t.Fatal("second WaitRequestRateLimit() unexpectedly bypassed the limiter")
	}
}

func TestRequestRateLimitDisabled(t *testing.T) {
	ctx := WithRequestRateLimit(context.Background(), 0)
	requestCtx, err := AcquireRequestPermit(ctx)
	if err != nil {
		t.Fatalf("AcquireRequestPermit() error = %v", err)
	}
	if err := WaitRequestRateLimit(requestCtx); err != nil {
		t.Fatalf("WaitRequestRateLimit() error = %v", err)
	}
}
