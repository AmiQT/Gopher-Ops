# Gopher-Ops Architecture

A map of the codebase: where things live, what depends on what, and where to make changes.

## High-level flow

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│   Telegram   │◀───▶│  cmd/main.go │◀───▶│  Gemini API  │
│   operator   │     │  (entrypoint)│     │  / LocalLLM  │
└──────────────┘     └──────┬───────┘     └──────────────┘
                            │
              ┌─────────────┼─────────────┐
              ▼             ▼             ▼
       ┌──────────┐  ┌──────────┐  ┌──────────┐
       │ handlers │  │  runner  │  │   bot    │
       │ (intent) │  │ (monitor)│  │ (deps)   │
       └─────┬────┘  └─────┬────┘  └─────┬────┘
             │             │              │
             └─────────────┼──────────────┘
                           ▼
                  ┌────────────────┐
                  │   pkg/* (DI'd) │
                  │  docker, ai,   │
                  │  monitor, k8s, │
                  │  audit, chaos  │
                  └────────────────┘
```

## Layer responsibilities

### `cmd/main.go` — entrypoint
- Loads `.env`, builds `config.Config`, calls `bot.Boot()`, supervises `runner.Monitor`, runs the Telegram update loop.
- **No business logic.** If you find yourself adding a `switch update.Message.Command()` here, push it into `internal/handlers/`.

### `internal/config` — single source of truth for env
- One `Config` struct, one `Load()`, one `Validate()`.
- **Rule:** no other package should call `os.Getenv()` for runtime knobs. Add the field here.

### `internal/bot` — dependency wiring
- `Deps` struct bundles the long-lived runtime objects (bot API client, AI agent, broadcaster, config).
- `Boot()` is the single side-effecting init point.
- Shared helpers: `SafeSend`, `SafeMD`, `AtomicWriteFile`.

### `internal/handlers` — Telegram intent dispatch
- `Command()` — `/start`, `/help`, `/status`, `/containers`, `/metrics`, etc.
- `Inline()` — text patterns (`update X cpu Y mem Z`, `search X pattern`, `chaos kill X`).
- `Callback()` — inline-button clicks (`StopContainer`, `AnalyzeRCA`, …).
- `BuildRCAEvidence()` — assembles the three-block AI evidence bundle.
- **Rule:** handlers return rendered text + parse mode; `main.go` sends. Easier to test.

### `internal/runner` — background monitor
- `Monitor()` is the 1-minute tick loop: collect stats, detect events, emit alerts, persist state.
- `SuperviseMonitor()` wraps it in panic recovery + 5s restart so the bot survives a runtime panic.
- State (`restarts`, `alerts`, `alertSentAt`, `crashLoop`) is serialized to `state.json` atomically.

### `pkg/` — reusable building blocks
| Package | Responsibility |
|---------|----------------|
| `actions` | Docker lifecycle (start/stop/restart), Terraform apply, network/config triage |
| `ai` | LLM providers (Gemini, Local) + `FallbackAgent` wrapper for transient failure tolerance |
| `audit` | JSON-line audit log with rotation + tamper-evident hash chain |
| `chaos` | Fault injection (kill random / kill named container) |
| `docker` | Shared Docker client singleton — every package uses this, not its own |
| `health` | HTTP `/health` endpoint with pluggable subsystem probers |
| `k8s` | `kubectl` wrapper for cluster info, pod ops, deployment restart |
| `mcp` | Model Context Protocol bridge (Kubernetes) |
| `metrics` | Prometheus collectors |
| `monitor` | Host + container stats, disk usage, log pattern scanner, Trivy CVE scan |
| `memory` | Persistent declarative SRE knowledge base & environment facts (`memories/SRE_FACTS.md`) |
| `notify` | Async multi-operator Telegram broadcaster (bounded queue) |
| `ratelimit` | Per-chat token-bucket throttle |
| `skills` | Procedural SRE runbooks & self-improving markdown skill engine (`skills/*.md`) |

## Dependency direction

```
cmd ──▶ internal/* ──▶ pkg/*
internal/handlers, internal/runner ──▶ internal/bot
```

- `internal/*` may depend on `pkg/*` freely.
- `pkg/*` must NOT import `internal/*`.
- `pkg/*` packages should not import each other unnecessarily — keep them leaf nodes.

## State + persistence

- `state.json` — restart counters, alert state, sent timestamps. Atomically written (tmp+rename).
- `audit.log` — append-only JSON lines with rotation (size-based). See `pkg/audit`.
- No database. Everything is file-based or in-memory by design (single-instance bot).

## Failure handling philosophy

- **Hard fail at boot** for missing required config (`GEMINI_API_KEY`, `TELEGRAM_BOT_TOKEN`, authorized IDs).
- **Soft degrade at runtime** for transient LLM/Docker/Telegram failures (FallbackAgent, retry+backoff, panic recovery).
- **HITL-by-default** for destructive actions — AI cannot stop/restart without operator confirmation unless `AI_DESTRUCTIVE_ALLOWED=true`.

## Adding a new feature: cheat sheet

| You want to add… | Touch these files |
|---|---|
| A new `/command` | `internal/handlers/commands.go` + entry in `helpText()` |
| A new inline-text pattern | `internal/handlers/inline.go` |
| A new AI-emitted button | `internal/handlers/callback.go` (`dispatchCallback` + `buttonLabel`) |
| A new env var | `internal/config/config.go` + `.env.example` + README table |
| A new background alert | `internal/runner/monitor.go` (add `alertXxx` function, wire into `Monitor()` loop) |
| A new Prometheus metric | `pkg/metrics/prometheus.go` |
| A new audit event type | Just call `audit.Log(Event{Type: "xxx", ...})` — no schema to update |
| A new `/audit` subcommand | `internal/handlers/audit.go` (`AuditCommand` switch) |
| A new alert with snooze | Emit via `broadcaster.Broadcast`; append `runner.snoozeRow("yourkey")` to keyboard; gate via `shouldAlert("yourkey", ...)` |

## Testing strategy

- `pkg/*` packages have focused unit tests.
- `internal/*` mostly relies on `pkg/*` being well-tested, plus future integration tests.
- Fuzz tests target untrusted-input boundaries (`SafeMD`, chat ID parser).
- Future: `testcontainers-go` integration tests for end-to-end Docker flows.
