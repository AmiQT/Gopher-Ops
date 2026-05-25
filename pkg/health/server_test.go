package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleHealth_OK(t *testing.T) {
	s := New("0", "test-v1")
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
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.Uptime < 1 {
		t.Errorf("uptime_seconds = %d, want >= 1", got.Uptime)
	}
	if got.Version != "test-v1" {
		t.Errorf("version = %q, want test-v1", got.Version)
	}
}

func TestHandleHealth_DegradedOnFailingProber(t *testing.T) {
	s := New("0", "")
	s.AddProber("fake", func(_ context.Context) SubsystemStatus {
		return SubsystemStatus{OK: false, Message: "unit test failure"}
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when prober fails, got %d", rec.Code)
	}
	var got Status
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Status != "degraded" {
		t.Errorf("status = %q, want degraded", got.Status)
	}
}

func TestHandleHealth_RootPath(t *testing.T) {
	s := New("0", "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("root path returned %d, want 200", rec.Code)
	}
}
