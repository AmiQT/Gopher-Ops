package runner

import (
	"testing"
	"time"

	"gopher-ops/internal/snooze"
)

// These tests pin down the alert-suppression policy. They map directly to
// real production scenarios:
//   - "first alert always fires"        → operator gets paged the first time
//   - "no spam within cooldown"         → flapping containers don't drown the chat
//   - "re-alert after cooldown"         → operator gets reminded if issue persists
//   - "snooze beats cooldown"           → planned maintenance silence works
//   - "expired snooze stops blocking"   → operator forgets to un-snooze, alerts resume

func init() {
	// Make sure no leftover snoozes from other tests bleed in.
	snooze.Path = ""
}

func clearSnoozes(t *testing.T) {
	t.Helper()
	snooze.Path = t.TempDir() + "/snooze.json"
	// Reach into the package to clear; using Clear() in a loop without keys is awkward.
	snooze.Clear("ctr:test")
	snooze.Clear("disk:/")
	snooze.Clear("mem:nginx")
}

func TestShouldAlert_FirstFailureAlwaysFires(t *testing.T) {
	clearSnoozes(t)
	got := shouldAlert("ctr:test", "", time.Time{}, 15*time.Minute)
	if !got {
		t.Error("first failure must always alert; got false")
	}
}

func TestShouldAlert_SuppressedWithinCooldown(t *testing.T) {
	clearSnoozes(t)
	got := shouldAlert("ctr:test", "alert_sent", time.Now().Add(-1*time.Minute), 15*time.Minute)
	if got {
		t.Error("alert within cooldown should be suppressed; got true")
	}
}

func TestShouldAlert_FiresAfterCooldown(t *testing.T) {
	clearSnoozes(t)
	got := shouldAlert("ctr:test", "alert_sent", time.Now().Add(-20*time.Minute), 15*time.Minute)
	if !got {
		t.Error("alert after cooldown should fire; got false")
	}
}

func TestShouldAlert_SnoozeBeatsCooldown(t *testing.T) {
	clearSnoozes(t)
	// Even when cooldown would normally allow the alert, an active snooze
	// must suppress. This is the contract operators rely on when they tap
	// "🤫 1h" during planned maintenance.
	snooze.Set("ctr:test", 1*time.Hour)
	defer snooze.Clear("ctr:test")

	got := shouldAlert("ctr:test", "alert_sent", time.Now().Add(-20*time.Minute), 15*time.Minute)
	if got {
		t.Error("active snooze must suppress alert even past cooldown; got true")
	}
}

func TestShouldAlert_SnoozeBeatsFirstFailure(t *testing.T) {
	clearSnoozes(t)
	// A first-ever failure for a key is normally a guaranteed alert. Snooze
	// must override even this — useful when an operator pre-snoozes ahead
	// of a known noisy event (e.g. before kicking off a load test).
	snooze.Set("ctr:test", 1*time.Hour)
	defer snooze.Clear("ctr:test")

	got := shouldAlert("ctr:test", "", time.Time{}, 15*time.Minute)
	if got {
		t.Error("pre-set snooze must suppress even first failure; got true")
	}
}

func TestShouldAlert_ExpiredSnoozeStopsBlocking(t *testing.T) {
	clearSnoozes(t)
	// A snoozed-then-expired key behaves as if it was never snoozed.
	// Important so operators don't lose alerts permanently if they forget.
	snooze.Set("ctr:test", 10*time.Millisecond)
	time.Sleep(50 * time.Millisecond)

	got := shouldAlert("ctr:test", "", time.Time{}, 15*time.Minute)
	if !got {
		t.Error("expired snooze should no longer block; got false")
	}
}

func TestShouldAlert_DifferentKeysIndependent(t *testing.T) {
	clearSnoozes(t)
	// Snoozing one container must not silence alerts for another. Catches
	// accidental "global mute" bugs.
	snooze.Set("ctr:nginx", 1*time.Hour)
	defer snooze.Clear("ctr:nginx")

	got := shouldAlert("ctr:redis", "", time.Time{}, 15*time.Minute)
	if !got {
		t.Error("snooze on ctr:nginx must not affect ctr:redis; got false")
	}
}
