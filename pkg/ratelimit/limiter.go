// Package ratelimit provides a per-chat token-bucket limiter to cap how
// quickly any single operator can issue commands. Without this, a compromised
// Telegram account could weaponize the AI to burn through an API quota in
// minutes.
package ratelimit

import (
	"sync"
	"time"
)

type bucket struct {
	tokens     float64
	lastRefill time.Time
}

// Limiter holds one token bucket per chat ID.
type Limiter struct {
	mu           sync.Mutex
	buckets      map[int64]*bucket
	maxTokens    float64
	refillPerSec float64
}

// New creates a limiter that permits up to `permitsPerMinute` over any
// rolling minute, with a burst capacity equal to that allowance.
func New(permitsPerMinute float64) *Limiter {
	if permitsPerMinute <= 0 {
		permitsPerMinute = 30
	}
	return &Limiter{
		buckets:      make(map[int64]*bucket),
		maxTokens:    permitsPerMinute,
		refillPerSec: permitsPerMinute / 60.0,
	}
}

// Allow consumes one token for `chatID`. Returns false if the bucket is empty.
func (l *Limiter) Allow(chatID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[chatID]
	if !ok {
		l.buckets[chatID] = &bucket{tokens: l.maxTokens - 1, lastRefill: now}
		return true
	}

	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * l.refillPerSec
	if b.tokens > l.maxTokens {
		b.tokens = l.maxTokens
	}
	b.lastRefill = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
