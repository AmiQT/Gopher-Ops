// Package audit writes structured JSON-line events to a persistent audit log.
// Every action the bot takes (autopilot restart, AI-triggered tool, user
// callback) should leave an entry so production incidents can be reconstructed
// after the Telegram thread has scrolled.
//
// Each entry includes a SHA-256 chain hash: Hash = SHA256(prevHash + JSON(event)).
// This makes the log tamper-evident — modifying or deleting any entry breaks
// the chain, detectable by Verify().
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	Hash   string    `json:"hash,omitempty"` // SHA-256(prevHash + JSON without Hash field)
}

// chainHash is the hash of the last written entry; maintained in memory.
// On restart we re-read the last line so the chain is continuous.
var chainHash string

var (
	mu       sync.Mutex
	path           = "audit.log"
	maxSize  int64 = 10 * 1024 * 1024 // 10 MB before rotation
	maxFiles       = 5                // keep audit.log + audit.log.1..audit.log.5
)

// SetPath overrides the audit log destination. Call before the first Log().
// It also seeds chainHash from the last line of the existing file so the
// chain is continuous across bot restarts.
func SetPath(p string) {
	mu.Lock()
	defer mu.Unlock()
	if p != "" {
		path = p
	}
	seedChainHash()
}

// seedChainHash reads the last line of the audit file and extracts its hash
// to initialise chainHash. Must be called with mu held.
func seedChainHash() {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	last := lines[len(lines)-1]
	var evt Event
	if err := json.Unmarshal([]byte(last), &evt); err == nil && evt.Hash != "" {
		chainHash = evt.Hash
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

// computeHash produces SHA-256(prevHash + jsonPayload).
func computeHash(prevHash string, payload []byte) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// Log appends an event to the audit file. Each entry is hashed against the
// previous entry's hash to form a tamper-evident chain. Failures are logged
// via slog but never panic — auditing must not take down the bot.
func Log(evt Event) {
	mu.Lock()
	defer mu.Unlock()

	if evt.Time.IsZero() {
		evt.Time = time.Now().UTC()
	}

	// Marshal without the Hash field first, then compute and inject hash.
	evt.Hash = ""
	payload, err := json.Marshal(evt)
	if err != nil {
		slog.Warn("audit marshal failed", "error", err)
		return
	}
	evt.Hash = computeHash(chainHash, payload)

	data, err := json.Marshal(evt)
	if err != nil {
		slog.Warn("audit marshal (with hash) failed", "error", err)
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
		return
	}
	chainHash = evt.Hash
}

// VerifyResult is the outcome of a chain integrity check.
type VerifyResult struct {
	Entries  int
	Broken   int // index of first broken link (0-based), -1 if intact
	ErrorMsg string
}

// Verify reads the audit log at the configured path and checks that every
// entry's hash matches SHA-256(prevHash + JSON-without-hash-field). Returns
// a VerifyResult describing the chain state.
func Verify() VerifyResult {
	mu.Lock()
	logPath := path
	mu.Unlock()

	data, err := os.ReadFile(logPath)
	if err != nil {
		return VerifyResult{Broken: -1, ErrorMsg: err.Error()}
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	prev := ""
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var evt Event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			return VerifyResult{Entries: i, Broken: i, ErrorMsg: fmt.Sprintf("line %d: %v", i, err)}
		}

		stored := evt.Hash
		evt.Hash = ""
		payload, _ := json.Marshal(evt)
		expected := computeHash(prev, payload)

		if stored != expected {
			return VerifyResult{
				Entries:  i,
				Broken:   i,
				ErrorMsg: fmt.Sprintf("line %d: hash mismatch (expected %s, got %s)", i, expected[:8], stored[:8]),
			}
		}
		prev = stored
	}
	return VerifyResult{Entries: len(lines), Broken: -1}
}
