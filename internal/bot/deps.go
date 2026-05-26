// Package bot wires together the bot's runtime dependencies and exposes the
// thin handlers that cmd/main.go composes into the Telegram update loop.
//
// The point of this package is dependency injection: every handler takes a
// *Deps rather than reaching into globals or os.Getenv, which makes them
// trivially testable and keeps cmd/main.go down to a thin entrypoint.
package bot

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopher-ops/internal/config"
	"gopher-ops/pkg/ai"
	"gopher-ops/pkg/metrics"
	"gopher-ops/pkg/notify"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Deps bundles the long-lived runtime objects every handler needs. It is
// built once during boot and threaded through to handlers, the monitor
// runner, and anywhere else that talks to the outside world.
type Deps struct {
	Cfg         *config.Config
	Bot         *tgbotapi.BotAPI
	Agent       ai.AIAgent
	Broadcaster *notify.Queue
	Version     string
	// ResetCh signals the monitor goroutine to flush in-memory trackers
	// when an operator runs /reset.
	ResetCh chan struct{}
}

// SafeSend wraps bot.Send so a failed delivery is logged + counted instead of
// silently vanishing. Returns the Message and error so callers that need the
// message ID can still use it.
func (d *Deps) SafeSend(msg tgbotapi.Chattable) (tgbotapi.Message, error) {
	sent, err := d.Bot.Send(msg)
	if err != nil {
		slog.Warn("telegram send failed", "error", err)
		metrics.TelegramMsgTotal.WithLabelValues("error").Inc()
	} else {
		metrics.TelegramMsgTotal.WithLabelValues("ok").Inc()
	}
	return sent, err
}

// markdownEscaper neutralizes Telegram Markdown specials in untrusted strings
// (container names, log payloads) so a crafted log line cannot break rendering
// or smuggle formatting into operator alerts.
var markdownEscaper = strings.NewReplacer(
	"_", "\\_",
	"*", "\\*",
	"`", "\\`",
	"[", "\\[",
)

// SafeMD escapes Telegram-Markdown specials in untrusted text.
func SafeMD(s string) string { return markdownEscaper.Replace(s) }

// AtomicWriteFile writes via tmp + rename so a crash mid-write cannot leave a
// half-written file that would corrupt the next boot.
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Clean(path))
}
