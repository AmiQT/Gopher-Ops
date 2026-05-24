// Package audit writes structured JSON-line events to a persistent audit log.
// Every action the bot takes (autopilot restart, AI-triggered tool, user
// callback) should leave an entry so production incidents can be reconstructed
// after the Telegram thread has scrolled.
package audit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Event is a single audit record.
type Event struct {
	Time   time.Time `json:"time"`
	Type   string    `json:"type"`             // boot | shutdown | action | alert | autopilot | ai_tool
	Actor  string    `json:"actor"`            // system | user | ai
	Action string    `json:"action,omitempty"` // RestartContainer, StopContainer, etc.
	Target string    `json:"target,omitempty"` // container id / url / etc.
	Status string    `json:"status"`           // ok | error | info
	Detail string    `json:"detail,omitempty"`
}

var (
	mu       sync.Mutex
	path     = "audit.log"
	maxSize  int64 = 10 * 1024 * 1024 // 10 MB before rotation
	maxFiles       = 5                // keep audit.log + audit.log.1..audit.log.5
)

// SetPath overrides the audit log destination. Call before the first Log().
func SetPath(p string) {
	mu.Lock()
	defer mu.Unlock()
	if p != "" {
		path = p
	}
}

// SetRotation configures size-based rotation. `bytes` is the threshold above
// which the active file is renamed to .1 and a fresh file is started.
// `files` is the number of historical files to retain.
func SetRotation(bytes int64, files int) {
	mu.Lock()
	defer mu.Unlock()
	if bytes > 0 {
		maxSize = bytes
	}
	if files > 0 {
		maxFiles = files
	}
}

// rotateIfNeeded must be called with mu held. It shifts audit.log.N-1 →
// audit.log.N for each kept generation, drops the oldest, and renames the
// current file to .1.
func rotateIfNeeded() {
	info, err := os.Stat(path)
	if err != nil || info.Size() < maxSize {
		return
	}

	// Drop the oldest generation.
	_ = os.Remove(fmt.Sprintf("%s.%d", path, maxFiles))

	// Shift the rest: .N-1 → .N, ..., .1 → .2
	for i := maxFiles - 1; i >= 1; i-- {
		oldName := fmt.Sprintf("%s.%d", path, i)
		newName := fmt.Sprintf("%s.%d", path, i+1)
		_ = os.Rename(oldName, newName)
	}

	// Active file becomes .1
	if err := os.Rename(path, path+".1"); err != nil {
		slog.Warn("audit rotate failed", "error", err)
	}
}

// Log appends an event to the audit file. Failures are logged via slog but
// never panic — auditing must not take down the bot.
func Log(evt Event) {
	mu.Lock()
	defer mu.Unlock()

	if evt.Time.IsZero() {
		evt.Time = time.Now().UTC()
	}

	data, err := json.Marshal(evt)
	if err != nil {
		slog.Warn("audit marshal failed", "error", err)
		return
	}

	rotateIfNeeded()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		slog.Warn("audit open failed", "error", err, "path", path)
		return
	}
	defer f.Close()

	if _, err := f.Write(append(data, '\n')); err != nil {
		slog.Warn("audit write failed", "error", err)
	}
}
