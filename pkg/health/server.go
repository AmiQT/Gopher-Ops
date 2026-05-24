// Package health serves a minimal HTTP /health endpoint so external monitors
// (Kubernetes liveness probes, uptime checkers, load balancers) can verify
// the bot is alive without joining the Telegram conversation.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// Status is the JSON payload returned by GET /health.
type Status struct {
	Status        string `json:"status"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// Server is the HTTP health endpoint.
type Server struct {
	port      string
	startTime time.Time
	srv       *http.Server
}

func New(port string) *Server {
	return &Server{
		port:      port,
		startTime: time.Now(),
	}
}

// Start launches the HTTP server in a goroutine and arranges shutdown on ctx
// cancellation. Returns immediately.
func (s *Server) Start(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/", s.handleHealth)

	s.srv = &http.Server{
		Addr:              ":" + s.port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		slog.Info("🩺 Health server listening", "port", s.port)
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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(Status{
		Status:        "ok",
		UptimeSeconds: int64(time.Since(s.startTime).Seconds()),
	})
}
