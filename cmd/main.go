// Command gopher-ops is the Telegram-driven SRE ChatOps bot.
//
// main.go is intentionally thin: it loads config, boots dependencies via
// internal/bot, supervises the background monitor via internal/runner, and
// dispatches Telegram updates to handlers in internal/handlers. All real
// logic lives in those packages so each is independently testable.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"gopher-ops/internal/bot"
	"gopher-ops/internal/config"
	"gopher-ops/internal/handlers"
	"gopher-ops/internal/runner"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/ratelimit"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

// run is the real entrypoint. We isolate it so deferred cleanups (signal
// handler release, AI agent close) actually fire before process exit — a
// bare `os.Exit(1)` from main() skips deferred calls (gocritic exitAfterDefer).
func run() error {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
	slog.Info("🤖 Booting up Gopher-Ops SRE Telegram Bot...", "version", bot.Version)

	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found — credentials must be set via environment")
	}
	// Must run after godotenv so .env values apply.
	ai.LoadEnvConfig()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}
	slog.Info("Authorized operators configured", "count", len(cfg.AuthorizedChatIDs))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps, err := bot.Boot(ctx, cfg)
	if err != nil {
		return fmt.Errorf("boot: %w", err)
	}
	defer deps.Agent.Close()

	limiter := ratelimit.New(cfg.RateLimitPerMin)

	go runner.SuperviseMonitor(ctx, deps)

	runUpdateLoop(ctx, deps, limiter)
	return nil
}

// runUpdateLoop is the Telegram receive loop: dispatch callbacks, commands,
// inline syntax, then fall back to free-form AI processing.
func runUpdateLoop(ctx context.Context, d *bot.Deps, limiter *ratelimit.Limiter) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := d.Bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			slog.Info("🛑 Shutdown signal received — stopping bot")
			audit.Log(audit.Event{Type: "shutdown", Actor: "system", Status: "ok"})
			d.Bot.StopReceivingUpdates()
			return
		case update, ok := <-updates:
			if !ok {
				return
			}
			handleUpdate(d, limiter, update)
		}
	}
}

func handleUpdate(d *bot.Deps, limiter *ratelimit.Limiter, update tgbotapi.Update) {
	if update.CallbackQuery != nil {
		chatID := update.CallbackQuery.Message.Chat.ID
		if !d.Cfg.AuthorizedSet[chatID] {
			d.Bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "❌ Kau siapa siot?"))
			return
		}
		if !limiter.Allow(chatID) {
			d.Bot.Request(tgbotapi.NewCallback(update.CallbackQuery.ID, "⏳ Slow down — rate limited."))
			return
		}
		handlers.Callback(d, update.CallbackQuery)
		return
	}

	if update.Message == nil {
		return
	}

	chatID := update.Message.Chat.ID
	if !d.Cfg.AuthorizedSet[chatID] {
		d.SafeSend(tgbotapi.NewMessage(chatID, "❌ Warganegara asing dilarang masuk. Kau sapa takde access level nak borak dengan aku bro. GG."))
		audit.Log(audit.Event{
			Type: "alert", Actor: "system", Action: "unauthorized_access",
			Target: strconv.FormatInt(chatID, 10), Status: "info",
		})
		return
	}

	if !limiter.Allow(chatID) {
		d.SafeSend(tgbotapi.NewMessage(chatID, "⏳ Whoa, slow down bro! Limit kau dah cecah. Try semula dalam seminit."))
		return
	}

	if update.Message.IsCommand() {
		reply := handlers.Command(d, update.Message)
		msg := tgbotapi.NewMessage(chatID, reply)
		msg.ParseMode = "Markdown"
		d.SafeSend(msg)
		return
	}

	if text, parseMode, handled := handlers.Inline(d, update.Message.Text); handled {
		msg := tgbotapi.NewMessage(chatID, text)
		if parseMode != "" {
			msg.ParseMode = parseMode
		}
		d.SafeSend(msg)
		return
	}

	handlers.AIProcess(d, update.Message)
}
