package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Entry represents a single auditable action taken by the agent
type Entry struct {
	Timestamp string `json:"timestamp"`
	Trigger   string `json:"trigger"` // "autopilot", "manual", "escalation"
	Action    string `json:"action"`
	Target    string `json:"target"`
	Result    string `json:"result"`
}

const (
	logFile    = "audit.jsonl"
	backupFile = "audit.jsonl.1"
	maxBytes   = 5 * 1024 * 1024 // 5 MB — rotate beyond this
)

var mu sync.Mutex

// Log appends an audit entry. Rotates the log file if it exceeds maxBytes.
func Log(trigger, action, target, result string) {
	mu.Lock()
	defer mu.Unlock()

	rotate()

	entry := Entry{
		Timestamp: time.Now().Format(time.RFC3339),
		Trigger:   trigger,
		Action:    action,
		Target:    target,
		Result:    result,
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(string(data) + "\n")
}

// rotate renames the log file to the backup path when it exceeds maxBytes.
// Must be called with mu held.
func rotate() {
	info, err := os.Stat(logFile)
	if err != nil || info.Size() < maxBytes {
		return
	}
	os.Rename(logFile, backupFile)
}

// ReadLast returns the last n entries from the audit log (most recent last).
func ReadLast(n int) []Entry {
	mu.Lock()
	defer mu.Unlock()

	f, err := os.Open(logFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	var all []Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e Entry
		if json.Unmarshal(scanner.Bytes(), &e) == nil {
			all = append(all, e)
		}
	}

	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}
