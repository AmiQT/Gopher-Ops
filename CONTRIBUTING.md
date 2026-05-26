# Contributing to Gopher-Ops

Thanks for considering a contribution! This guide gets you from clone to merged PR.

## Quick start

```bash
git clone https://github.com/yourusername/gopher-ops.git
cd gopher-ops
cp .env.example .env       # fill in TELEGRAM_BOT_TOKEN, GEMINI_API_KEY, AUTHORIZED_CHAT_IDS
make tidy                  # download deps
make test                  # run the full unit test suite
make run                   # launch locally (requires Docker socket)
```

## Prerequisites

- **Go 1.22+** (`go version`) — the exact required version is pinned in [go.mod](go.mod); use that or newer.
- **Docker** running locally — many tests + the bot itself talk to the Docker socket
- **Terraform 1.5+** — only needed if you touch `terraform/`
- **kubectl** — only needed for the Kubernetes feature path
- **golangci-lint** — `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`

## Project layout

See [ARCHITECTURE.md](ARCHITECTURE.md) for the module breakdown. TL;DR:

- `cmd/main.go` — thin entrypoint (~140 lines)
- `internal/` — bot bootstrap, handlers, runner, config (app-private)
- `pkg/` — reusable building blocks (Docker, AI, monitor, audit, etc.)
- `terraform/` — local lab environment

## Development workflow

```bash
make fmt        # gofmt + goimports
make lint       # golangci-lint
make test       # go test ./...
make cover      # generates coverage.out + prints summary
make build      # compile binary
```

Run **all four** (`fmt`, `lint`, `test`, `build`) before pushing — that's exactly what CI does.

## Writing tests

- Unit tests live next to the code they test (`foo_test.go`).
- Use table-driven tests when covering multiple input cases.
- For Docker-touching code, prefer integration tests under `// +build integration` and run with `make test-integration`.
- Add fuzz tests (`func FuzzXxx`) for anything that parses untrusted input — chat IDs, Markdown, log lines.

Coverage gate: PRs must keep total coverage **≥ 60%**. CI will fail otherwise.

## Commit messages

Follow the existing pattern: `<type>: <short summary>`. Examples:

- `feat: add disk pressure alerts`
- `fix: prevent panic when state.json is empty`
- `refactor: split main.go into internal packages`
- `docs: clarify CHAOS_ENABLED behavior`
- `test: add fuzz test for markdown escaper`

Keep the subject ≤ 72 chars. Body explains **why**, not what.

## Pull request checklist

- [ ] Tests added/updated for the change
- [ ] `make fmt lint test` all pass locally
- [ ] No new env vars introduced without documenting in [README.md](README.md) + [.env.example](.env.example)
- [ ] If touching the audit log format → bump the version constant in `pkg/audit`
- [ ] If adding a new AI tool/intent → also update the button label in `internal/handlers/callback.go`

## Reporting bugs

Use the **Bug Report** issue template. Include:
- Gopher-Ops version (`/status` in Telegram)
- Go version (`go version`)
- Steps to reproduce
- Relevant audit log lines (redact chat IDs and tokens!)

## Feature proposals

Open a **Feature Request** issue *first* before sending a PR for anything non-trivial — saves you and us time if the direction isn't a fit.

## Security issues

**Do not** open a public issue for security vulnerabilities. See [SECURITY.md](SECURITY.md) for the disclosure policy and how to report privately.

## Code of conduct

Be kind. Be patient. We're all here to make ops less painful — keep the discourse focused on the code, not the people behind it.
