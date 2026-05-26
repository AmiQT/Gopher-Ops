package ai

import (
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"gopher-ops/pkg/monitor"
)

// FallbackAgent wraps any AIAgent and catches transient failures. When the
// underlying agent fails consecutively it enters "offline mode" and returns
// useful diagnostic data (system health, container states) without an LLM
// round-trip. It exits offline mode automatically after a back-off window.
type FallbackAgent struct {
	inner        AIAgent
	failures     atomic.Int32
	offlineUntil atomic.Int64 // Unix nano; 0 = not offline
}

const (
	maxFailuresBeforeOffline = 3
	offlineDuration          = 5 * time.Minute
)

// NewFallbackAgent wraps agent with automatic offline/recovery logic.
func NewFallbackAgent(agent AIAgent) *FallbackAgent {
	return &FallbackAgent{inner: agent}
}

func (f *FallbackAgent) isOffline() bool {
	until := f.offlineUntil.Load()
	if until == 0 {
		return false
	}
	if time.Now().UnixNano() < until {
		return true
	}
	// Back-off window expired — reset and retry.
	f.offlineUntil.Store(0)
	f.failures.Store(0)
	return false
}

func (f *FallbackAgent) markFailure() {
	n := f.failures.Add(1)
	if n >= maxFailuresBeforeOffline {
		until := time.Now().Add(offlineDuration).UnixNano()
		f.offlineUntil.Store(until)
		slog.Warn("AI entering offline mode", "failures", n, "retry_after", offlineDuration)
	}
}

func (f *FallbackAgent) markSuccess() {
	f.failures.Store(0)
	f.offlineUntil.Store(0)
}

// offlineResponse builds a diagnostic snapshot without calling the LLM.
func offlineResponse(userMsg string) string {
	health, err := monitor.GetSystemHealth()
	if err != nil {
		health = fmt.Sprintf("(system health unavailable: %v)", err)
	}

	diskInfo := monitor.FormatDiskUsage()
	containerStats := monitor.FormatContainerStats()

	return fmt.Sprintf(
		"⚠️ **AI OFFLINE** — Gopher-Ops dalam manual mode.\n\n"+
			"AI tidak dapat dihubungi sekarang (quota, network, atau API error). "+
			"Bot masih boleh run semua actions — guna buttons atau commands macam biasa.\n\n"+
			"**Query kau:** _%s_\n\n"+
			"**Live Diagnostics:**\n%s\n\n"+
			"%s\n\n%s\n\n"+
			"_AI akan cuba reconnect dalam ~5 minit._",
		userMsg, health, containerStats, diskInfo,
	)
}

func (f *FallbackAgent) ProcessRequest(userMsg string) (string, []ActionIntent, error) {
	if f.isOffline() {
		return offlineResponse(userMsg), nil, nil
	}

	resp, intents, err := f.inner.ProcessRequest(userMsg)
	if err != nil {
		f.markFailure()
		slog.Warn("AI ProcessRequest failed, serving fallback", "error", err, "failures", f.failures.Load())
		return offlineResponse(userMsg), nil, nil
	}
	f.markSuccess()
	return resp, intents, nil
}

func (f *FallbackAgent) ResetSession() {
	f.inner.ResetSession()
}

func (f *FallbackAgent) DiagnoseIssue(containerName, evidence string) (string, error) {
	if f.isOffline() {
		return fmt.Sprintf("⚠️ AI offline — tidak dapat buat RCA untuk `%s` sekarang. Cuba lagi dalam ~5 minit.", containerName), nil
	}
	resp, err := f.inner.DiagnoseIssue(containerName, evidence)
	if err != nil {
		f.markFailure()
		return fmt.Sprintf("⚠️ AI error semasa RCA untuk `%s`: %v\n\nCuba lagi sebentar.", containerName, err), nil
	}
	f.markSuccess()
	return resp, nil
}

func (f *FallbackAgent) AuditSecurity(ctxData, imageScanData string) (string, error) {
	if f.isOffline() {
		return "⚠️ AI offline — security audit tidak tersedia sekarang.", nil
	}
	resp, err := f.inner.AuditSecurity(ctxData, imageScanData)
	if err != nil {
		f.markFailure()
		return fmt.Sprintf("⚠️ AI error semasa security audit: %v", err), nil
	}
	f.markSuccess()
	return resp, nil
}

func (f *FallbackAgent) TriageIssue(containerName, triageType, data string) (string, error) {
	if f.isOffline() {
		return fmt.Sprintf("⚠️ AI offline — triage untuk `%s` tidak tersedia sekarang.", containerName), nil
	}
	resp, err := f.inner.TriageIssue(containerName, triageType, data)
	if err != nil {
		f.markFailure()
		return fmt.Sprintf("⚠️ AI error semasa triage `%s`: %v", containerName, err), nil
	}
	f.markSuccess()
	return resp, nil
}

func (f *FallbackAgent) Close() {
	f.inner.Close()
}

// IsOffline reports whether the agent is currently in offline mode.
// Useful for the /status command to surface AI health.
func (f *FallbackAgent) IsOffline() bool {
	return f.isOffline()
}
