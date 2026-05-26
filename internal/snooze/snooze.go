// Package snooze tracks per-alert-key suppression windows. Operators tap a
// Snooze button on any alert; we record "this key is muted until T" and the
// monitor skips re-alerting until T elapses.
//
// Lives in its own package because both internal/runner (writes during alert
// emission, reads during shouldAlert) and internal/handlers (writes when an
// operator clicks Snooze) need access without creating an import cycle.
package snooze

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Path is the persistence file. Same convention as state.json — atomic
// tmp+rename writes so a crash mid-write cannot corrupt it.
var Path = "snooze.json"

var (
	mu    sync.RWMutex
	until = map[string]time.Time{}
)

// Set marks `key` as snoozed until now+d. A zero or negative duration is a
// no-op — callers can pass a parsed env value without guarding.
func Set(key string, d time.Duration) {
	if d <= 0 {
		return
	}
	mu.Lock()
	until[key] = time.Now().Add(d)
	mu.Unlock()
	save()
}

// Clear removes a snooze entry. Used when an operator explicitly un-snoozes
// or when stale entries are pruned.
func Clear(key string) {
	mu.Lock()
	delete(until, key)
	mu.Unlock()
	save()
}

// Active reports whether `key` is currently snoozed (deadline in the future).
// Expired entries are evicted lazily on first read after their deadline.
func Active(key string) bool {
	mu.RLock()
	deadline, ok := until[key]
	mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().Before(deadline) {
		return true
	}
	// Lazy cleanup of expired entries.
	mu.Lock()
	delete(until, key)
	mu.Unlock()
	return false
}

// Until returns the snooze deadline for `key`, or zero time if not snoozed.
func Until(key string) time.Time {
	mu.RLock()
	defer mu.RUnlock()
	return until[key]
}

// Load reads the persistence file. Safe to call on startup — missing or
// corrupt files just leave the map empty.
func Load() {
	data, err := os.ReadFile(Path)
	if err != nil {
		return
	}
	var disk map[string]time.Time
	if err := json.Unmarshal(data, &disk); err != nil {
		slog.Warn("snooze.json corrupt — starting fresh", "error", err)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	for k, t := range disk {
		// Skip already-expired entries — no reason to load and re-evict.
		if t.After(now) {
			until[k] = t
		}
	}
}

func save() {
	mu.RLock()
	data, _ := json.Marshal(until)
	mu.RUnlock()

	tmp := Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		slog.Warn("snooze.json write failed", "error", err)
		return
	}
	if err := os.Rename(tmp, filepath.Clean(Path)); err != nil {
		slog.Warn("snooze.json rename failed", "error", err)
	}
}
