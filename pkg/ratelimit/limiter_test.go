package ratelimit

import (
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenBlocks(t *testing.T) {
	// 60/min == 1 per second steady state, with a burst capacity of 60.
	l := New(60)
	const chat int64 = 42

	for i := 0; i < 60; i++ {
		if !l.Allow(chat) {
			t.Fatalf("expected first 60 calls to be allowed, blocked at %d", i)
		}
	}
	if l.Allow(chat) {
		t.Fatal("expected 61st call to be blocked")
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	l := New(60)
	const chat int64 = 7

	// Drain the bucket.
	for i := 0; i < 60; i++ {
		l.Allow(chat)
	}

	// Hand-roll the refill by mutating the bucket's lastRefill clock.
	l.mu.Lock()
	b := l.buckets[chat]
	b.lastRefill = b.lastRefill.Add(-2 * time.Second)
	l.mu.Unlock()

	if !l.Allow(chat) {
		t.Fatal("expected refill to grant at least one token after 2s")
	}
}

func TestLimiterIsolatesChats(t *testing.T) {
	l := New(1)
	if !l.Allow(1) {
		t.Fatal("chat 1 first call should be allowed")
	}
	if !l.Allow(2) {
		t.Fatal("chat 2 first call should be allowed (isolated bucket)")
	}
	if l.Allow(1) {
		t.Fatal("chat 1 second call should be blocked")
	}
}
