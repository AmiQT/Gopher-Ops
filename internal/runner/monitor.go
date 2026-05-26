// Package runner hosts long-running background goroutines: the metrics
// collector + alert pipeline. cmd/main.go launches Monitor() and supervises
// it; everything else (event detection, alert formatting, state persistence)
// lives here.
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"gopher-ops/internal/bot"
	"gopher-ops/internal/handlers"
	"gopher-ops/internal/snooze"
	"gopher-ops/pkg/actions"
	"gopher-ops/pkg/audit"
	"gopher-ops/pkg/metrics"
	"gopher-ops/pkg/monitor"
	"gopher-ops/pkg/notify"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const rcaCacheTTL = 1 * time.Hour

// SuperviseMonitor runs Monitor() with panic recovery and auto-restart. It
// blocks until ctx is cancelled. If the inner monitor crashes, it is
// restarted after 5 seconds — the bot keeps polling Telegram either way.
func SuperviseMonitor(ctx context.Context, d *bot.Deps) {
	for {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("🔥 Background monitor panicked — restarting", "panic", r)
				}
			}()
			Monitor(ctx, d)
		}()

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
			slog.Warn("Background monitor restarting after crash...")
		}
	}
}

// Monitor is the core 1-minute tick loop: collect stats, detect events
// (container down, crash-loop, mem pressure, disk fill, log patterns,
// sustained CPU), emit alerts via the broadcaster, persist state.
func Monitor(ctx context.Context, d *bot.Deps) {
	cfg := d.Cfg
	slog.Info("🩺 Background monitor started",
		"cpu_threshold", cfg.CPUThreshold,
		"cpu_duration_min", cfg.CPUDurationMin,
		"alert_cooldown", cfg.AlertCooldown)

	previousStates, err := monitor.GetContainerStates()
	if err != nil {
		slog.Warn("Monitor init error — starting with empty state", "error", err)
		previousStates = make(map[string]monitor.ContainerStatus)
	}

	s := newState()
	s.load()

	rcaCache := make(map[string]cachedRCA)

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Background monitor stopping")
			return
		case <-d.ResetCh:
			slog.Info("🧹 Monitor state reset via /reset command")
			s.reset()
			for k := range rcaCache {
				delete(rcaCache, k)
			}
			audit.Log(audit.Event{Type: "action", Actor: "system", Action: "monitor_reset", Status: "ok"})
			continue
		case <-ticker.C:
		}

		collectStats(ctx)

		currentStates, err := monitor.GetContainerStates()
		if err != nil {
			slog.Warn("Monitor loop error", "error", err)
			continue
		}

		// Evict RCA cache entries that are stale or refer to dead containers.
		for k, v := range rcaCache {
			if time.Since(v.timestamp) > rcaCacheTTL {
				delete(rcaCache, k)
				continue
			}
			if _, alive := currentStates[k]; !alive {
				delete(rcaCache, k)
			}
		}

		for id, current := range currentStates {
			prev, exists := previousStates[id]
			safeName := bot.SafeMD(current.Name)

			if exists && prev.State == "running" && current.State != "running" {
				handleContainerDown(d, s, rcaCache, id, current.Name, safeName, current.State)
				handleCrashLoop(d, s, id, current.Name, safeName)
			}

			if current.State == "running" {
				if s.restarts[id] > 0 || s.alerts[id] != "" {
					slog.Info("✨ Container healthy again — resetting trackers", "container", current.Name)
					s.restarts[id] = 0
					s.alerts[id] = ""
					s.crashLoop[id] = nil
					s.save()
				}
			}
		}

		probeHTTP(d)
		alertDisk(d, s)
		alertMemPressure(d, s)
		alertSustainedCPU(d)
		scanLogs(ctx, d, s)

		previousStates = currentStates
	}
}

// cachedRCA caches an AI diagnosis for 10 minutes so a flapping container
// doesn't re-burn Gemini quota on every autopilot restart attempt.
type cachedRCA struct {
	result    string
	timestamp time.Time
}

// snoozeRow returns the standard 3-button snooze row to append below any
// alert keyboard. Callback data format: "Snooze:<key>:<minutes>".
// The key is what's looked up in shouldAlert — keep it consistent with the
// key passed into shouldAlert at the alert call site.
func snoozeRow(alertKey string) []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🤫 1h", "Snooze:"+alertKey+":60"),
		tgbotapi.NewInlineKeyboardButtonData("🤫 4h", "Snooze:"+alertKey+":240"),
		tgbotapi.NewInlineKeyboardButtonData("🤫 24h", "Snooze:"+alertKey+":1440"),
	)
}

// shouldAlert returns true when a fresh alert for a key is allowed.
//   - operator-set snooze active for `key`: always suppress (highest priority)
//   - first failure (status == ""): always alert
//   - same failure within cooldown: suppress
//   - same failure after cooldown elapsed: re-alert
func shouldAlert(key, status string, lastSent time.Time, cooldown time.Duration) bool {
	if snooze.Active(key) {
		return false
	}
	if status == "" {
		return true
	}
	if lastSent.IsZero() {
		return true
	}
	return time.Since(lastSent) > cooldown
}

// collectStats fans out a single per-tick stats collection. Bounded to 30s.
// Per-container values are pushed to Prometheus inline.
func collectStats(ctx context.Context) {
	collectCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := monitor.CollectContainerStats(collectCtx); err != nil {
		slog.Warn("container stats collection failed", "error", err)
		return
	}
	for _, s := range monitor.GetContainerStatSnapshot() {
		metrics.ContainerCPUPct.WithLabelValues(s.Name).Set(s.CPUPct)
		metrics.ContainerRAMUsedMB.WithLabelValues(s.Name).Set(s.MemUsedMB)
		metrics.ContainerRAMPct.WithLabelValues(s.Name).Set(s.MemPct)
		metrics.ContainerNetRxMB.WithLabelValues(s.Name).Set(s.NetRxMB)
		metrics.ContainerNetTxMB.WithLabelValues(s.Name).Set(s.NetTxMB)
		metrics.ContainerBlkReadMB.WithLabelValues(s.Name).Set(s.BlkReadMB)
		metrics.ContainerBlkWriteMB.WithLabelValues(s.Name).Set(s.BlkWriteMB)
		metrics.ContainerPIDs.WithLabelValues(s.Name).Set(float64(s.PIDs))
		up := 0.0
		if s.State == "running" {
			up = 1.0
		}
		metrics.ContainerUp.WithLabelValues(s.Name).Set(up)
	}
	if last := monitor.GlobalMetricStore.LatestPoint(); !last.Timestamp.IsZero() {
		metrics.HostCPUPct.Set(last.CPU)
		metrics.HostRAMPct.Set(last.RAM)
	}
	if usages, err := monitor.GetDiskUsage(); err == nil {
		for _, u := range usages {
			metrics.DiskUsedPct.WithLabelValues(u.Mount, u.Filesystem).Set(u.UsedPct)
			metrics.DiskUsedGB.WithLabelValues(u.Mount, u.Filesystem).Set(u.UsedGB)
			metrics.DiskTotalGB.WithLabelValues(u.Mount, u.Filesystem).Set(u.TotalGB)
		}
	}
}

// handleContainerDown fires on the running→not-running edge. It either
// invokes the autopilot (if enabled + under attempt limit) or emits an
// operator alert with restart + RCA buttons.
func handleContainerDown(d *bot.Deps, s *state, rcaCache map[string]cachedRCA, id, name, safeName, currentState string) {
	count := s.restarts[id]
	autoPilot := d.Cfg.AutopilotEnabled

	if autoPilot && count < 3 {
		s.restarts[id]++
		slog.Info("🤖 Auto-Pilot restart", "attempt", s.restarts[id], "container", name, "id", id)
		audit.Log(audit.Event{
			Type: "autopilot", Actor: "ai", Action: "RestartContainer",
			Target: id, Status: "info",
			Detail: fmt.Sprintf("attempt %d/3 for %s", s.restarts[id], name),
		})

		var analysis string
		if cache, ok := rcaCache[id]; ok && time.Since(cache.timestamp) < 10*time.Minute {
			analysis = cache.result + "\n\n*(Nota: Analisis dari cache 10 minit terakhir)*"
		} else {
			evidence := handlers.BuildRCAEvidence(d.Cfg, id)
			result, diagErr := d.Agent.DiagnoseIssue(name, evidence)
			if diagErr != nil {
				slog.Warn("DiagnoseIssue failed during autopilot", "container", name, "error", diagErr)
				analysis = "*(Analisis AI tidak tersedia: " + bot.SafeMD(diagErr.Error()) + ")*"
			} else {
				analysis = result
				rcaCache[id] = cachedRCA{result: result, timestamp: time.Now()}
			}
		}

		if _, restartErr := actions.RestartContainer(id); restartErr != nil {
			slog.Warn("Autopilot restart failed", "container", name, "error", restartErr)
			audit.Log(audit.Event{
				Type: "autopilot", Actor: "ai", Action: "RestartContainer",
				Target: id, Status: "error", Detail: restartErr.Error(),
			})
		}

		msgText := fmt.Sprintf("🤖 **AUTOPILOT ACTION! (Attempt %d/3)**\n\nContainer **%s** tadi DOWN. Aku dah tolong restartkan untuk kau!\n\n**Analisis AI:**\n%s",
			s.restarts[id], safeName, analysis)
		d.Broadcaster.Broadcast(notify.Broadcast{Text: msgText, ParseMode: "Markdown"})
		s.alerts[id] = "autopilot_sent"
		s.alertSentAt[id] = time.Now()
		s.save()
		return
	}

	if !shouldAlert("ctr:"+id, s.alerts[id], s.alertSentAt[id], d.Cfg.AlertCooldown) {
		return
	}
	var alertMsg string
	if count >= 3 {
		alertMsg = fmt.Sprintf("🚨 **CRITICAL ALERT!**\n\nContainer **%s** dah restart 3 kali tapi still DOWN mat. 💀 Aku dah 'surrender'. Kau kena check manual jap bro.", safeName)
	} else {
		alertMsg = fmt.Sprintf("⚠️ **ALERT!** Container **%s** dah **DOWN**! (Status: %s)\nNak aku restart ke bro?", safeName, bot.SafeMD(currentState))
	}
	s.alerts[id] = "alert_sent"
	s.alertSentAt[id] = time.Now()
	metrics.AlertsTotal.WithLabelValues("container_down").Inc()
	audit.Log(audit.Event{
		Type: "alert", Actor: "system", Action: "container_down",
		Target: id, Status: "info", Detail: name,
	})

	btnRestart := tgbotapi.NewInlineKeyboardButtonData("🔄 Restart Now", "RestartContainer:"+id)
	btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 Siasat Punca (RCA)", "AnalyzeRCA:"+id)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btnRestart),
		tgbotapi.NewInlineKeyboardRow(btnRCA),
		snoozeRow("ctr:"+id),
	)
	d.Broadcaster.Broadcast(notify.Broadcast{
		Text: alertMsg, ParseMode: "Markdown", ReplyMarkup: &keyboard,
	})
	s.save()
}

// handleCrashLoop tracks restart timestamps independent of autopilot counter
// so crash-loop detection fires even when AUTOPILOT_ENABLED=false.
func handleCrashLoop(d *bot.Deps, s *state, id, name, safeName string) {
	now := time.Now()
	window := 5 * time.Minute
	cutoff := now.Add(-window)

	times := s.crashLoop[id]
	filtered := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	filtered = append(filtered, now)
	s.crashLoop[id] = filtered

	if len(filtered) >= 3 && s.alerts[id] != "crashloop" {
		loopMsg := fmt.Sprintf(
			"🔴 **CRASH LOOP DETECTED!**\n\nContainer **%s** dah restart **%d kali dalam 5 minit**. "+
				"Ini bukan fluke — ada root cause serius. Kau kena check sekarang!\n\n"+
				"Nak aku buat RCA?",
			safeName, len(filtered))
		btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 Deep RCA", "AnalyzeRCA:"+id)
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(btnRCA),
			snoozeRow("ctr:"+id),
		)
		d.Broadcaster.Broadcast(notify.Broadcast{Text: loopMsg, ParseMode: "Markdown", ReplyMarkup: &keyboard})
		s.alerts[id] = "crashloop"
		s.alertSentAt[id] = now
		metrics.AlertsTotal.WithLabelValues("crash_loop").Inc()
		audit.Log(audit.Event{
			Type: "alert", Actor: "system", Action: "crash_loop",
			Target: id, Status: "critical", Detail: fmt.Sprintf("%d restarts in 5m", len(filtered)),
		})
		s.save()
	}
}

func probeHTTP(d *bot.Deps) {
	for _, u := range d.Cfg.MonitorURLs {
		status := monitor.CheckHTTP(u)
		if !status.IsUp {
			alertMsg := fmt.Sprintf("🌐 **HTTP ALERT!** Website [%s](%s) return error!\nStatus Code: **%d**\nCheck jap bro, kot-kot app hang.", u, u, status.StatusCode)
			d.Broadcaster.Broadcast(notify.Broadcast{Text: alertMsg, ParseMode: "Markdown"})
		}
	}
}

func alertDisk(d *bot.Deps, s *state) {
	alerts, err := monitor.CheckDiskAlerts(d.Cfg.DiskAlertThresh)
	if err != nil {
		return
	}
	for _, a := range alerts {
		key := "disk:" + a.Mount
		if !shouldAlert(key, s.alerts[key], s.alertSentAt[key], d.Cfg.AlertCooldown) {
			continue
		}
		alertMsg := fmt.Sprintf("💾 **DISK ALERT!**\n\nMount **%s** dah `%.1f%%` penuh (%.1f / %.1f GB).\nKalau hit 100%% nanti container tak boleh write logs, app crash. Cleanup ASAP bro!",
			a.Mount, a.UsedPct, a.UsedGB, a.TotalGB)
		d.Broadcaster.Broadcast(notify.Broadcast{Text: alertMsg, ParseMode: "Markdown"})
		s.alerts[key] = "high"
		s.alertSentAt[key] = time.Now()
		metrics.AlertsTotal.WithLabelValues("disk_high").Inc()
		audit.Log(audit.Event{Type: "alert", Actor: "system", Action: "disk_high", Target: a.Mount, Status: "fired"})
	}
}

func alertMemPressure(d *bot.Deps, s *state) {
	thresh := d.Cfg.MemPressureThresh
	for _, c := range monitor.GetContainerStatSnapshot() {
		if c.MemLimitMB <= 0 || c.MemPct < thresh {
			continue
		}
		key := "mem:" + c.Name
		if !shouldAlert(key, s.alerts[key], s.alertSentAt[key], d.Cfg.AlertCooldown) {
			continue
		}
		memMsg := fmt.Sprintf(
			"🧠 **MEMORY PRESSURE!**\n\nContainer **%s** dah guna **%.1f%%** of its memory limit "+
				"(%.0f / %.0f MB).\nKalau hit 100%% — OOMKilled. Kau kena scale up atau investigate sekarang!",
			bot.SafeMD(c.Name), c.MemPct, c.MemUsedMB, c.MemLimitMB)
		btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 Investigate", "AnalyzeRCA:"+c.ID)
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(btnRCA),
			snoozeRow(key),
		)
		d.Broadcaster.Broadcast(notify.Broadcast{Text: memMsg, ParseMode: "Markdown", ReplyMarkup: &keyboard})
		s.alerts[key] = "mem_pressure"
		s.alertSentAt[key] = time.Now()
		metrics.AlertsTotal.WithLabelValues("mem_pressure").Inc()
		audit.Log(audit.Event{
			Type: "alert", Actor: "system", Action: "mem_pressure",
			Target: c.Name, Status: "warning",
			Detail: fmt.Sprintf("%.1f%% of %.0fMB", c.MemPct, c.MemLimitMB),
		})
		s.save()
	}
}

func alertSustainedCPU(d *bot.Deps) {
	if !monitor.GlobalMetricStore.CheckSustainedLoad(d.Cfg.CPUThreshold, d.Cfg.CPUDurationMin) {
		return
	}
	// Respect operator-set snoozes on the sustained-CPU key.
	if snooze.Active("cpu:sustained") {
		return
	}
	alertMsg := fmt.Sprintf("🔥 **SUSTAINED HIGH LOAD ALERT!**\n\nCPU load kau dah lebih **%.0f%%** untuk %d minit berturut-turut bro! Server tengah berpeluh tu. 🥵\n\nNak aku check container mana yang makan resource paling banyak?", d.Cfg.CPUThreshold, d.Cfg.CPUDurationMin)
	btnCheck := tgbotapi.NewInlineKeyboardButtonData("📊 Check Resource Hogs", "VisualMetrics:all")
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btnCheck),
		snoozeRow("cpu:sustained"),
	)
	d.Broadcaster.Broadcast(notify.Broadcast{
		Text: alertMsg, ParseMode: "Markdown", ReplyMarkup: &keyboard,
	})
}

func scanLogs(ctx context.Context, d *bot.Deps, s *state) {
	scanCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	matches, err := monitor.ScanContainerLogs(scanCtx, 65*time.Second)
	if err != nil {
		slog.Warn("log pattern scan failed", "error", err)
		return
	}
	for _, m := range matches {
		key := "log:" + m.ContainerID + ":" + m.PatternName
		if !shouldAlert(key, s.alerts[key], s.alertSentAt[key], d.Cfg.AlertCooldown) {
			continue
		}
		emoji := "⚠️"
		if m.Level == "critical" {
			emoji = "🚨"
		}
		alertMsg := fmt.Sprintf(
			"%s **LOG ALERT** — `%s` [**%s**]\n\n```\n%s\n```",
			emoji, m.ContainerName, m.PatternName, m.MatchedLine)
		btnRCA := tgbotapi.NewInlineKeyboardButtonData("🔍 RCA", "AnalyzeRCA:"+m.ContainerID)
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(btnRCA),
			snoozeRow(key),
		)
		d.Broadcaster.Broadcast(notify.Broadcast{Text: alertMsg, ParseMode: "Markdown", ReplyMarkup: &keyboard})
		s.alerts[key] = "log_alert"
		s.alertSentAt[key] = time.Now()
		metrics.AlertsTotal.WithLabelValues("log_" + m.PatternName).Inc()
		audit.Log(audit.Event{
			Type: "alert", Actor: "system", Action: "log_pattern",
			Target: m.ContainerName, Status: m.Level, Detail: m.PatternName,
		})
	}
}

// state bundles the in-memory trackers the monitor writes to state.json so
// they survive a restart. All access is serialized through save()/load().
type state struct {
	mu          sync.Mutex
	restarts    map[string]int
	alerts      map[string]string
	alertSentAt map[string]time.Time
	crashLoop   map[string][]time.Time
}

func newState() *state {
	return &state{
		restarts:    map[string]int{},
		alerts:      map[string]string{},
		alertSentAt: map[string]time.Time{},
		crashLoop:   map[string][]time.Time{},
	}
}

func (s *state) reset() {
	s.restarts = map[string]int{}
	s.alerts = map[string]string{}
	s.alertSentAt = map[string]time.Time{}
	s.crashLoop = map[string][]time.Time{}
}

func (s *state) load() {
	file, err := os.ReadFile(handlers.StateFile)
	if err != nil {
		return
	}
	var disk struct {
		Restarts    map[string]int       `json:"restarts"`
		Alerts      map[string]string    `json:"alerts"`
		AlertSentAt map[string]time.Time `json:"alert_sent_at"`
	}
	if err := json.Unmarshal(file, &disk); err != nil {
		slog.Warn("state.json corrupt — starting fresh", "error", err)
		return
	}
	if disk.Restarts != nil {
		s.restarts = disk.Restarts
	}
	if disk.Alerts != nil {
		s.alerts = disk.Alerts
	}
	if disk.AlertSentAt != nil {
		s.alertSentAt = disk.AlertSentAt
	}
	slog.Info("💾 State loaded from state.json")
}

func (s *state) save() {
	s.mu.Lock()
	defer s.mu.Unlock()

	payload := struct {
		Restarts    map[string]int       `json:"restarts"`
		Alerts      map[string]string    `json:"alerts"`
		AlertSentAt map[string]time.Time `json:"alert_sent_at"`
	}{s.restarts, s.alerts, s.alertSentAt}
	data, _ := json.Marshal(payload)
	if err := bot.AtomicWriteFile(handlers.StateFile, data, 0644); err != nil {
		slog.Warn("state.json write failed", "error", err)
	}
}
