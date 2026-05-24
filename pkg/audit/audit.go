// Package audit writes structured JSON-line events to a persistent audit log.
// Every action the bot takes (autopilot restart, AI-triggered tool, user
// callback) should leave an entry so production incidents can be reconstructed
// after the Telegram thread has scrolled.
package audit

import (
	"encoding/json"
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
	mu   sync.Mutex
	path = "audit.log"
)

// SetPath overrides the audit log destination. Call before the first Log().
func SetPath(p string) {
	mu.Lock()
	defer mu.Unlock()
	if p != "" {
		path = p
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
