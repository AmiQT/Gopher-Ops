package audit

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Entry represents a single auditable action taken by the agent
type Entry struct {
	Timestamp string `json:"timestamp"`
	Trigger   string `json:"trigger"` // "autopilot" or "manual"
	Action    string `json:"action"`
	Target    string `json:"target"`
	Result    string `json:"result"`
}

const logFile = "audit.jsonl"

var mu sync.Mutex

// Log appends an audit entry to audit.jsonl
func Log(trigger, action, target, result string) {
	mu.Lock()
	defer mu.Unlock()

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
