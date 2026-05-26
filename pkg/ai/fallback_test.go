package ai

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// stubAgent is a minimal AIAgent that returns configurable errors/responses.
type stubAgent struct {
	processErr  error
	diagnoseErr error
	callCount   int
}

func (s *stubAgent) ProcessRequest(msg string) (string, []ActionIntent, error) {
	s.callCount++
	if s.processErr != nil {
		return "", nil, s.processErr
	}
	return "ok:" + msg, nil, nil
}
func (s *stubAgent) ResetSession()                                 {}
func (s *stubAgent) DiagnoseIssue(name, ev string) (string, error) { return "", s.diagnoseErr }
func (s *stubAgent) AuditSecurity(ctx, img string) (string, error) { return "audit", nil }
func (s *stubAgent) TriageIssue(n, t, d string) (string, error)    { return "triage", nil }
func (s *stubAgent) Close()                                        {}
func (s *stubAgent) IsOffline() bool                               { return false }

func TestFallbackAgent_PassthroughOnSuccess(t *testing.T) {
	stub := &stubAgent{}
	fa := NewFallbackAgent(stub)

	resp, _, err := fa.ProcessRequest("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "ok:hello" {
		t.Errorf("got %q, want ok:hello", resp)
	}
}

func TestFallbackAgent_EntersOfflineAfterMaxFailures(t *testing.T) {
	stub := &stubAgent{processErr: errors.New("upstream down")}
	fa := NewFallbackAgent(stub)

	// Fire maxFailuresBeforeOffline calls — the last one should flip offline.
	for i := 0; i < maxFailuresBeforeOffline; i++ {
		resp, _, err := fa.ProcessRequest("q")
		if err != nil {
			t.Fatalf("FallbackAgent should absorb errors, got: %v", err)
		}
		if !strings.Contains(resp, "AI OFFLINE") && !strings.Contains(resp, "AI error") {
			t.Errorf("expected offline/error response, got: %q", resp)
		}
	}

	if !fa.IsOffline() {
		t.Error("expected agent to be offline after consecutive failures")
	}
}

func TestFallbackAgent_ServesOfflineResponseWithoutCallingInner(t *testing.T) {
	stub := &stubAgent{processErr: errors.New("down")}
	fa := NewFallbackAgent(stub)

	// Drive it offline.
	for i := 0; i < maxFailuresBeforeOffline; i++ {
		fa.ProcessRequest("q") //nolint
	}

	callsBefore := stub.callCount
	resp, _, _ := fa.ProcessRequest("query while offline")
	if stub.callCount != callsBefore {
		t.Error("inner agent should NOT be called while in offline mode")
	}
	if !strings.Contains(resp, "AI OFFLINE") {
		t.Errorf("offline response missing expected text, got: %q", resp)
	}
}

func TestFallbackAgent_RecoveryAfterBackoff(t *testing.T) {
	stub := &stubAgent{processErr: errors.New("down")}
	fa := NewFallbackAgent(stub)

	// Force offline immediately by injecting a past offlineUntil.
	fa.failures.Store(int32(maxFailuresBeforeOffline))
	fa.offlineUntil.Store(time.Now().Add(-1 * time.Second).UnixNano()) // expired

	// Now fix the inner agent.
	stub.processErr = nil
	resp, _, err := fa.ProcessRequest("post-recovery")
	if err != nil {
		t.Fatalf("unexpected error after recovery: %v", err)
	}
	if resp != "ok:post-recovery" {
		t.Errorf("expected passthrough after recovery, got %q", resp)
	}
	if fa.IsOffline() {
		t.Error("should be online after successful call")
	}
}
