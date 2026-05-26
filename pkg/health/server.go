// Package health serves HTTP endpoints for liveness probes and metrics scraping.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"time"

	"gopher-ops/pkg/metrics"
)

// SubsystemStatus captures the health of one dependency.
type SubsystemStatus struct {
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// Status is the JSON payload returned by GET /health.
type Status struct {
	Status     string                     `json:"status"`
	Uptime     int64                      `json:"uptime_seconds"`
	Version    string                     `json:"version,omitempty"`
	Subsystems map[string]SubsystemStatus `json:"subsystems,omitempty"`
}

// SubsystemProber is a function that checks one subsystem and returns
// whether it is healthy and an optional message.
type SubsystemProber func(ctx context.Context) SubsystemStatus

// Server is the HTTP health + metrics endpoint.
type Server struct {
	port        string
	startTime   time.Time
	version     string
	probers     map[string]SubsystemProber
	srv         *http.Server
	enablePprof bool
}

func New(port, version string) *Server {
	return &Server{
		port:      port,
		startTime: time.Now(),
		version:   version,
		probers:   make(map[string]SubsystemProber),
	}
}

// EnablePprof exposes Go's net/http/pprof handlers under /debug/pprof/. Off by
// default — only enable when actively profiling, since the endpoint leaks
// internal state and can be expensive to scrape.
func (s *Server) EnablePprof() { s.enablePprof = true }

// AddProber registers a named subsystem health check.
func (s *Server) AddProber(name string, fn SubsystemProber) {
	s.probers[name] = fn
}

// Start launches the HTTP server in a goroutine and arranges shutdown on ctx
// cancellation. Returns immediately.
func (s *Server) Start(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/", s.handleHealth)

	if s.enablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		slog.Warn("⚠️  pprof enabled at /debug/pprof/ — do not expose publicly")
	}

	s.srv = &http.Server{
		Addr:              ":" + s.port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("🩺 Health server listening", "port", s.port, "metrics", "/metrics")
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("health server failed", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	probeCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	subsystems := make(map[string]SubsystemStatus, len(s.probers))
	overall := "ok"
	for name, probe := range s.probers {
		result := probe(probeCtx)
		subsystems[name] = result
		if !result.OK {
			overall = "degraded"
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if overall != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(Status{
		Status:     overall,
		Uptime:     int64(time.Since(s.startTime).Seconds()),
		Version:    s.version,
		Subsystems: subsystems,
	})
}
