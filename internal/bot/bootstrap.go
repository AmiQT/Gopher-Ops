package bot

import (
	"context"
	"fmt"
	"log/slog"

	"gopher-ops/internal/config"
	"gopher-ops/internal/snooze"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/health"
	"gopher-ops/pkg/metrics"
	"gopher-ops/pkg/monitor"
	"gopher-ops/pkg/notify"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Boot constructs every long-lived dependency the bot needs to run. It is the
// single place where side-effecting init (Telegram auth, AI client open,
// Prometheus registration, health server, broadcaster) happens — the rest of
// the app receives a fully-built *Deps and stays pure.
func Boot(ctx context.Context, cfg *config.Config) (*Deps, error) {
	// Audit pipeline first — every later boot step gets logged.
	audit.SetPath(cfg.AuditLogPath)
	audit.SetRotation(cfg.AuditLogMaxBytes, cfg.AuditLogMaxFiles)
	audit.Log(audit.Event{Type: "boot", Actor: "system", Status: "ok"})

	// Restore any snoozes that were active before the last shutdown.
	snooze.Load()

	if cfg.MetricsHistory > 0 {
		monitor.SetMetricsHistorySize(cfg.MetricsHistory)
	}

	rawAgent, err := ai.NewAgent(ai.Config{
		Provider:  cfg.LLMProvider,
		APIKey:    cfg.LLMAPIKey(),
		BaseURL:   cfg.LLMBaseURL,
		ModelName: cfg.LLMModel,
	})
	if err != nil {
		return nil, fmt.Errorf("ai agent: %w", err)
	}
	// Wrap with FallbackAgent so transient AI failures don't break the bot.
	agent := ai.NewFallbackAgent(rawAgent)

	tg, err := tgbotapi.NewBotAPI(cfg.TelegramToken)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	tg.Debug = false
	slog.Info("✅ Authorized on Telegram", "username", tg.Self.UserName)

	metrics.Register()
	if cfg.HealthPort != "" {
		srv := health.New(cfg.HealthPort, Version)
		if cfg.PprofEnable {
			srv.EnablePprof()
		}
		srv.AddProber("docker", func(_ context.Context) health.SubsystemStatus {
			if _, err := monitor.GetContainerStates(); err != nil {
				return health.SubsystemStatus{OK: false, Message: err.Error()}
			}
			return health.SubsystemStatus{OK: true}
		})
		srv.AddProber("telegram", func(_ context.Context) health.SubsystemStatus {
			if tg.Self.UserName == "" {
				return health.SubsystemStatus{OK: false, Message: "bot not initialised"}
			}
			return health.SubsystemStatus{OK: true}
		})
		srv.Start(ctx)
	}

	broadcaster := notify.NewQueue(tg, cfg.AuthorizedChatIDs, cfg.TelegramQueue)
	go broadcaster.Run(ctx)

	return &Deps{
		Cfg:         cfg,
		Bot:         tg,
		Agent:       agent,
		Broadcaster: broadcaster,
		Version:     Version,
		ResetCh:     make(chan struct{}, 1),
	}, nil
}
