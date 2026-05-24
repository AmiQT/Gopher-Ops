// Package notify wraps the Telegram bot in a non-blocking, multi-operator
// broadcaster. The monitor goroutine never blocks on Telegram API latency:
// it enqueues a message and moves on while a dedicated worker drains the
// queue. Every alert is fanned out to every authorized chat ID.
package notify

import (
	"context"
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Broadcast is a one-to-many notification payload. Chat IDs are resolved by
// the Queue at fan-out time so callers don't need to know who's on-call.
type Broadcast struct {
	Text        string
	ParseMode   string
	ReplyMarkup *tgbotapi.InlineKeyboardMarkup
}

// Queue is an async sender that drains messages on a single worker goroutine.
type Queue struct {
	bot     *tgbotapi.BotAPI
	chatIDs []int64
	msgs    chan tgbotapi.Chattable
}

// NewQueue constructs a Queue. bufSize bounds the in-memory backlog; when full
// further broadcasts are dropped (and logged) instead of blocking the caller.
func NewQueue(bot *tgbotapi.BotAPI, chatIDs []int64, bufSize int) *Queue {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &Queue{
		bot:     bot,
		chatIDs: chatIDs,
		msgs:    make(chan tgbotapi.Chattable, bufSize),
	}
}

// Run drains the queue until ctx is cancelled. Call once in a goroutine.
func (q *Queue) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-q.msgs:
			if _, err := q.bot.Send(m); err != nil {
				slog.Warn("telegram send failed", "error", err)
			}
		}
	}
}

// Broadcast fans out a message to every authorized chat ID. Non-blocking:
// drops the message with a warning if the queue is at capacity.
func (q *Queue) Broadcast(b Broadcast) {
	for _, chatID := range q.chatIDs {
		msg := tgbotapi.NewMessage(chatID, b.Text)
		if b.ParseMode != "" {
			msg.ParseMode = b.ParseMode
		}
		if b.ReplyMarkup != nil {
			msg.ReplyMarkup = *b.ReplyMarkup
		}
		select {
		case q.msgs <- msg:
		default:
			slog.Warn("telegram queue full — dropping broadcast", "chat_id", chatID)
		}
	}
}
