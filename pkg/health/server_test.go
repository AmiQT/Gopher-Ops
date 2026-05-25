package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestHandleHealth_OK asserts /health responds 200 with JSON containing the
// expected status field and a non-negative uptime.
func TestHandleHealth_OK(t *testing.T) {
	s := New("0")
	// Backdate startTime so uptime is at least 1s.
	s.startTime = time.Now().Add(-2 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var got Status
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("response not valid JSON: %v\nbody: %s", err, rec.Body.String())
	}
	if got.Status != "ok" {
		t.Errorf("status field = %q, want %q", got.Status, "ok")
	}
	if got.UptimeSeconds < 1 {
		t.Errorf("uptime_seconds = %d, want >= 1", got.UptimeSeconds)
	}
}

// TestHandleHealth_RootPath ensures the catch-all "/" handler also returns
// healthy output. This lets simple HTTP probes (curl base URL) work without
// needing the exact /health path.
func TestHandleHealth_RootPath(t *testing.T) {
	s := New("0")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("root path returned %d, want 200", rec.Code)
	}
}
