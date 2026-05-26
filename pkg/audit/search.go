package audit

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"strings"
)

// ErrLogMissing is returned when the audit file doesn't exist yet (e.g. fresh
// install before the first Log() call wrote anything).
var ErrLogMissing = errors.New("audit log file not found")

// Recent returns the last `n` entries from the active audit file, newest
// first. Returns at most `n` records — fewer if the log is shorter.
// Older rotated files (audit.log.1, .2, ...) are not searched.
func Recent(n int) ([]Event, error) {
	if n <= 0 {
		return nil, nil
	}
	events, err := readAll()
	if err != nil {
		return nil, err
	}
	if len(events) > n {
		events = events[len(events)-n:]
	}
	reverse(events)
	return events, nil
}

// Search returns the last `limit` entries whose Action, Target, Type, Actor
// or Detail contain `query` (case-insensitive substring). Empty query returns
// the most recent `limit` entries (same as Recent).
func Search(query string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 20
	}
	events, err := readAll()
	if err != nil {
		return nil, err
	}
	if query == "" {
		if len(events) > limit {
			events = events[len(events)-limit:]
		}
		reverse(events)
		return events, nil
	}

	q := strings.ToLower(query)
	matches := make([]Event, 0, limit)
	// Scan newest-first so we can stop early once we hit the limit.
	for i := len(events) - 1; i >= 0 && len(matches) < limit; i-- {
		e := events[i]
		hay := strings.ToLower(e.Action + " " + e.Target + " " + e.Type + " " + e.Actor + " " + e.Detail)
		if strings.Contains(hay, q) {
			matches = append(matches, e)
		}
	}
	return matches, nil
}

// ByActor returns the last `limit` entries where Actor matches `actor`
// (case-insensitive exact match). Useful for "what did this operator do?"
func ByActor(actor string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 20
	}
	events, err := readAll()
	if err != nil {
		return nil, err
	}
	target := strings.ToLower(actor)
	matches := make([]Event, 0, limit)
	for i := len(events) - 1; i >= 0 && len(matches) < limit; i-- {
		if strings.EqualFold(events[i].Actor, target) {
			matches = append(matches, events[i])
		}
	}
	_ = target
	return matches, nil
}

// readAll parses every line of the active audit file into Events. Skips
// malformed lines (logged via slog elsewhere) rather than aborting the
// whole read — a single corrupt line shouldn't blind the operator.
func readAll() ([]Event, error) {
	mu.Lock()
	logPath := path
	mu.Unlock()

	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrLogMissing
		}
		return nil, err
	}
	defer f.Close()

	var events []Event
	scanner := bufio.NewScanner(f)
	// Audit lines can contain long Detail fields (e.g. RCA snippets).
	// Default Scanner buffer is 64KB — bump to 1MB to be safe.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

func reverse(events []Event) {
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
}
