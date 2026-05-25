package notify

import (
	"context"
	"testing"
	"time"
)

// TestNewQueue_DefaultBufSize asserts an invalid or zero buffer falls back to
// the documented 256 default rather than creating an unbuffered channel that
// would block every Broadcast caller.
func TestNewQueue_DefaultBufSize(t *testing.T) {
	q := NewQueue(nil, []int64{1}, 0)
	if cap(q.msgs) != 256 {
		t.Errorf("default cap = %d, want 256", cap(q.msgs))
	}

	q2 := NewQueue(nil, []int64{1}, -5)
	if cap(q2.msgs) != 256 {
		t.Errorf("negative cap fallback = %d, want 256", cap(q2.msgs))
	}
}

// TestBroadcast_FillsQueueUpToCap shows messages enter the buffer one per
// chat ID, up to capacity, then subsequent broadcasts are dropped.
func TestBroadcast_FillsQueueUpToCap(t *testing.T) {
	// One chat, buf=2 → 2 messages fit, the third should drop without blocking.
	q := NewQueue(nil, []int64{42}, 2)
	q.Broadcast(Broadcast{Text: "first"})
	q.Broadcast(Broadcast{Text: "second"})

	// Capacity reached. This call must NOT block — it should drop.
	done := make(chan struct{})
	go func() {
		q.Broadcast(Broadcast{Text: "third — should drop"})
		close(done)
	}()
	select {
	case <-done:
		// good: returned immediately
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Broadcast blocked when queue was full — should have dropped")
	}

	// Queue still holds exactly 2 messages.
	if len(q.msgs) != 2 {
		t.Errorf("queue len = %d, want 2", len(q.msgs))
	}
}

// TestBroadcast_FanoutPerChatID asserts a single Broadcast emits one message
// per authorized chat ID. The async worker would send each, but we test the
// enqueue side here without needing a real bot.
func TestBroadcast_FanoutPerChatID(t *testing.T) {
	q := NewQueue(nil, []int64{1, 2, 3}, 10)
	q.Broadcast(Broadcast{Text: "alert"})
	if got := len(q.msgs); got != 3 {
		t.Errorf("queue len after 1 broadcast to 3 chats = %d, want 3", got)
	}
}

// TestRun_StopsOnContextCancel verifies the worker drains until ctx is done
// and then returns cleanly — no leaked goroutine. Bot is nil; if the test
// accidentally tries to Send, it would panic, so we verify the cancel path
// fires BEFORE any send is attempted by passing an already-cancelled ctx.
func TestRun_StopsOnContextCancel(t *testing.T) {
	q := NewQueue(nil, []int64{1}, 10)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	done := make(chan struct{})
	go func() {
		q.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
