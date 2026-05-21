package monitor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── URLMetricStore ────────────────────────────────────────────────────────────

func TestURLMetricStore_Push_CapacityRespected(t *testing.T) {
	store := &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 3}
	url := "http://example.com"

	for i := 0; i < 5; i++ {
		store.Push(URLMetricPoint{URL: url, Latency: time.Duration(i) * time.Millisecond, IsUp: true})
	}

	if len(store.Points[url]) != 3 {
		t.Errorf("expected 3 points (MaxSize), got %d", len(store.Points[url]))
	}
}

func TestURLMetricStore_IsDegraded_BelowThreshold(t *testing.T) {
	store := &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	url := "http://fast.com"

	for i := 0; i < 5; i++ {
		store.Push(URLMetricPoint{URL: url, Latency: 50 * time.Millisecond, IsUp: true, StatusCode: 200})
	}

	if store.IsDegraded(url, 200*time.Millisecond, 5) {
		t.Error("expected not degraded — avg 50ms is below 200ms threshold")
	}
}

func TestURLMetricStore_IsDegraded_AboveThreshold(t *testing.T) {
	store := &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	url := "http://slow.com"

	for i := 0; i < 5; i++ {
		store.Push(URLMetricPoint{URL: url, Latency: 500 * time.Millisecond, IsUp: true, StatusCode: 200})
	}

	if !store.IsDegraded(url, 200*time.Millisecond, 5) {
		t.Error("expected degraded — avg 500ms exceeds 200ms threshold")
	}
}

func TestURLMetricStore_IsDegraded_NotEnoughSamples(t *testing.T) {
	store := &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	url := "http://new.com"

	store.Push(URLMetricPoint{URL: url, Latency: 999 * time.Millisecond, IsUp: true})

	if store.IsDegraded(url, 100*time.Millisecond, 5) {
		t.Error("should not flag degraded when fewer samples than required count")
	}
}

func TestURLMetricStore_IsDegraded_IgnoresDownPoints(t *testing.T) {
	store := &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	url := "http://mixed.com"

	// 3 down points (latency irrelevant), 2 fast up points
	for i := 0; i < 3; i++ {
		store.Push(URLMetricPoint{URL: url, Latency: 999 * time.Millisecond, IsUp: false})
	}
	for i := 0; i < 2; i++ {
		store.Push(URLMetricPoint{URL: url, Latency: 10 * time.Millisecond, IsUp: true})
	}

	// Only 2 valid (up) samples — below the required count of 5
	if store.IsDegraded(url, 50*time.Millisecond, 5) {
		t.Error("should not flag degraded — not enough valid (up) samples")
	}
}

func TestURLMetricStore_UpstreamSummary_Empty(t *testing.T) {
	store := &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	if store.UpstreamSummary() != "" {
		t.Error("expected empty summary for empty store")
	}
}

func TestURLMetricStore_UpstreamSummary_ContainsURL(t *testing.T) {
	store := &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	url := "http://api.example.com"
	store.Push(URLMetricPoint{URL: url, Latency: 120 * time.Millisecond, IsUp: true, StatusCode: 200})

	summary := store.UpstreamSummary()
	if !strings.Contains(summary, url) {
		t.Errorf("expected summary to contain URL %q, got: %s", url, summary)
	}
	if !strings.Contains(summary, "UP") {
		t.Error("expected summary to contain status UP")
	}
}

// ── CheckHTTP ────────────────────────────────────────────────────────────────

func TestCheckHTTP_Up(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Use a fresh store to avoid cross-test pollution
	orig := GlobalURLMetricStore
	GlobalURLMetricStore = &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	defer func() { GlobalURLMetricStore = orig }()

	status := CheckHTTP(srv.URL)
	if !status.IsUp {
		t.Errorf("expected IsUp=true for HTTP 200, got false")
	}
	if status.StatusCode != 200 {
		t.Errorf("expected StatusCode=200, got %d", status.StatusCode)
	}

	// Verify latency was recorded
	if len(GlobalURLMetricStore.Points[srv.URL]) == 0 {
		t.Error("expected latency point to be recorded in GlobalURLMetricStore")
	}
}

func TestCheckHTTP_Down(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	orig := GlobalURLMetricStore
	GlobalURLMetricStore = &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	defer func() { GlobalURLMetricStore = orig }()

	status := CheckHTTP(srv.URL)
	if status.IsUp {
		t.Error("expected IsUp=false for HTTP 500")
	}
}

func TestCheckHTTP_Unreachable(t *testing.T) {
	orig := GlobalURLMetricStore
	GlobalURLMetricStore = &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 10}
	defer func() { GlobalURLMetricStore = orig }()

	status := CheckHTTP("http://127.0.0.1:19999") // nothing listening here
	if status.IsUp {
		t.Error("expected IsUp=false for unreachable host")
	}
	if status.StatusCode != 0 {
		t.Errorf("expected StatusCode=0 for connection error, got %d", status.StatusCode)
	}
}

// ── DetectImageChanges ────────────────────────────────────────────────────────

func TestDetectImageChanges_NoChanges(t *testing.T) {
	prev := ImageSnapshot{"abc12345": "nginx:1.21", "def67890": "redis:7.0"}
	curr := ImageSnapshot{"abc12345": "nginx:1.21", "def67890": "redis:7.0"}
	changes := DetectImageChanges(prev, curr)
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %v", changes)
	}
}

func TestDetectImageChanges_OneChanged(t *testing.T) {
	prev := ImageSnapshot{"abc12345": "nginx:1.21"}
	curr := ImageSnapshot{"abc12345": "nginx:1.25"}
	changes := DetectImageChanges(prev, curr)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if !strings.Contains(changes[0], "nginx:1.21") || !strings.Contains(changes[0], "nginx:1.25") {
		t.Errorf("change message missing old/new image: %s", changes[0])
	}
}

func TestDetectImageChanges_NewContainerIgnored(t *testing.T) {
	prev := ImageSnapshot{"abc12345": "nginx:1.21"}
	curr := ImageSnapshot{"abc12345": "nginx:1.21", "newcontainer": "postgres:15"}
	changes := DetectImageChanges(prev, curr)
	if len(changes) != 0 {
		t.Errorf("new containers should not appear as changes, got %v", changes)
	}
}

// ── FormatCrashContext ────────────────────────────────────────────────────────

func TestFormatCrashContext_OOM(t *testing.T) {
	cc := CrashContext{ExitCode: 137, OOMKilled: true, RestartCount: 2}
	out := FormatCrashContext(cc)
	if !strings.Contains(out, "OOMKilled") {
		t.Error("expected OOMKilled in output")
	}
	if !strings.Contains(out, "137") {
		t.Error("expected exit code 137 in output")
	}
	if !strings.Contains(out, "2") {
		t.Error("expected restart count 2 in output")
	}
}

func TestFormatCrashContext_Segfault(t *testing.T) {
	cc := CrashContext{ExitCode: 139, OOMKilled: false, RestartCount: 0}
	out := FormatCrashContext(cc)
	if !strings.Contains(out, "Segmentation fault") {
		t.Errorf("expected segfault description, got: %s", out)
	}
}

func TestFormatCrashContext_AppError(t *testing.T) {
	cc := CrashContext{ExitCode: 1, OOMKilled: false, RestartCount: 0}
	out := FormatCrashContext(cc)
	if !strings.Contains(out, "Application error") {
		t.Errorf("expected app error description, got: %s", out)
	}
}

func TestFormatCrashContext_CleanExit(t *testing.T) {
	cc := CrashContext{ExitCode: 0, OOMKilled: false, RestartCount: 0}
	out := FormatCrashContext(cc)
	if !strings.Contains(out, "Clean exit") {
		t.Errorf("expected clean exit description, got: %s", out)
	}
}

// ── PreCrashMetricsSummary ────────────────────────────────────────────────────

func TestPreCrashMetricsSummary_Empty(t *testing.T) {
	orig := GlobalMetricStore
	GlobalMetricStore = &MetricStore{Points: make([]MetricPoint, 0), MaxSize: 60}
	defer func() { GlobalMetricStore = orig }()

	out := PreCrashMetricsSummary(5)
	if out != "" {
		t.Errorf("expected empty string for empty store, got: %q", out)
	}
}

func TestPreCrashMetricsSummary_ReturnsLastN(t *testing.T) {
	orig := GlobalMetricStore
	GlobalMetricStore = &MetricStore{Points: make([]MetricPoint, 0), MaxSize: 60}
	defer func() { GlobalMetricStore = orig }()

	for i := 0; i < 10; i++ {
		GlobalMetricStore.PushMetrics(float64(i)*10, float64(i)*5)
	}

	out := PreCrashMetricsSummary(3)
	if !strings.Contains(out, "last 3 minutes") {
		t.Errorf("expected '3 minutes' in output, got: %s", out)
	}
	// Should contain CPU values for the last 3 points (70%, 80%, 90%)
	for _, expected := range []string{"70.0%", "80.0%", "90.0%"} {
		if !strings.Contains(out, expected) {
			t.Errorf("expected %s in pre-crash metrics, got:\n%s", expected, out)
		}
	}
}

func TestPreCrashMetricsSummary_FewerPointsThanRequested(t *testing.T) {
	orig := GlobalMetricStore
	GlobalMetricStore = &MetricStore{Points: make([]MetricPoint, 0), MaxSize: 60}
	defer func() { GlobalMetricStore = orig }()

	GlobalMetricStore.PushMetrics(50.0, 60.0)

	out := PreCrashMetricsSummary(5) // ask for 5 but only 1 available
	if out == "" {
		t.Error("expected non-empty output even with fewer points than requested")
	}
}

// ── FormatCascadeContext ──────────────────────────────────────────────────────

func TestFormatCascadeContext_NoDeps(t *testing.T) {
	out := FormatCascadeContext([]string{"api-server"}, DependencyMap{})
	if out != "" {
		t.Errorf("expected empty string for empty dependency map, got: %q", out)
	}
}

func TestFormatCascadeContext_NoCrashes(t *testing.T) {
	deps := DependencyMap{"api-server": {"postgres"}}
	out := FormatCascadeContext([]string{}, deps)
	if out != "" {
		t.Errorf("expected empty string when no crashes, got: %q", out)
	}
}

func TestFormatCascadeContext_DetectsCascade(t *testing.T) {
	deps := DependencyMap{
		"api-server": {"postgres"},
		"worker":     {"redis"},
	}
	// postgres crashed — api-server depends on it
	out := FormatCascadeContext([]string{"postgres"}, deps)
	if !strings.Contains(out, "api-server") {
		t.Errorf("expected api-server in cascade context, got: %s", out)
	}
	if !strings.Contains(out, "postgres") {
		t.Errorf("expected postgres in cascade context, got: %s", out)
	}
	// worker should not appear — redis didn't crash
	if strings.Contains(out, "worker") {
		t.Errorf("worker should not appear when redis did not crash, got: %s", out)
	}
}

func TestFormatCascadeContext_MultipleAffected(t *testing.T) {
	deps := DependencyMap{
		"api-server": {"postgres"},
		"dashboard":  {"postgres"},
	}
	out := FormatCascadeContext([]string{"postgres"}, deps)
	if !strings.Contains(out, "api-server") || !strings.Contains(out, "dashboard") {
		t.Errorf("expected both dependents in output, got: %s", out)
	}
}

// ── GetDiskUsage ──────────────────────────────────────────────────────────────

func TestGetDiskUsage_ContainsRoot(t *testing.T) {
	out := GetDiskUsage()
	if !strings.Contains(out, "/") {
		t.Error("expected root partition in disk usage output")
	}
	if !strings.Contains(out, "%") {
		t.Error("expected percentage in disk usage output")
	}
}

// ── MetricStore ───────────────────────────────────────────────────────────────

func TestMetricStore_CheckSustainedLoad_True(t *testing.T) {
	store := &MetricStore{Points: make([]MetricPoint, 0), MaxSize: 10}
	for i := 0; i < 5; i++ {
		store.PushMetrics(90.0, 50.0)
	}
	if !store.CheckSustainedLoad(80.0, 5) {
		t.Error("expected sustained load to be true — avg CPU 90% > threshold 80%")
	}
}

func TestMetricStore_CheckSustainedLoad_False(t *testing.T) {
	store := &MetricStore{Points: make([]MetricPoint, 0), MaxSize: 10}
	for i := 0; i < 5; i++ {
		store.PushMetrics(30.0, 50.0)
	}
	if store.CheckSustainedLoad(80.0, 5) {
		t.Error("expected sustained load to be false — avg CPU 30% < threshold 80%")
	}
}

func TestMetricStore_CheckSustainedLoad_NotEnoughData(t *testing.T) {
	store := &MetricStore{Points: make([]MetricPoint, 0), MaxSize: 10}
	store.PushMetrics(99.0, 99.0) // only 1 point, need 5
	if store.CheckSustainedLoad(80.0, 5) {
		t.Error("should return false when not enough data points")
	}
}

func TestMetricStore_MaxSizeEnforced(t *testing.T) {
	store := &MetricStore{Points: make([]MetricPoint, 0), MaxSize: 3}
	for i := 0; i < 10; i++ {
		store.PushMetrics(float64(i), float64(i))
	}
	if len(store.Points) != 3 {
		t.Errorf("expected 3 points (MaxSize), got %d", len(store.Points))
	}
}

// ── GetContainerName fallback ─────────────────────────────────────────────────

func TestGetContainerName_InvalidID_ReturnsSelf(t *testing.T) {
	id := "totally_invalid_docker_id_xyz"
	got := GetContainerName(id)
	if got != id {
		t.Errorf("expected %q returned unchanged, got %q", id, got)
	}
}

// ── URLMetricStore thread safety ──────────────────────────────────────────────

func TestURLMetricStore_ConcurrentPush(t *testing.T) {
	store := &URLMetricStore{Points: make(map[string][]URLMetricPoint), MaxSize: 100}
	done := make(chan struct{})

	for i := 0; i < 50; i++ {
		go func(n int) {
			store.Push(URLMetricPoint{
				URL:     fmt.Sprintf("http://url-%d.com", n%5),
				Latency: time.Duration(n) * time.Millisecond,
				IsUp:    true,
			})
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < 50; i++ {
		<-done
	}
	// If we got here without a race condition panic, the test passes.
	// Run with: go test -race ./pkg/monitor/...
}
