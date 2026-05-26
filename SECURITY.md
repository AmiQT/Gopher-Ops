# Security Policy

Gopher-Ops binds to the host Docker socket and Kubernetes API to execute real infrastructure operations. We take security reports seriously.

## Supported Versions

Only the **latest `main`** branch and the most recent tagged release receive security fixes. Older releases are not patched — please upgrade.

| Version | Supported |
|---------|-----------|
| `main`  | ✅ Yes |
| Latest `v1.x.x` tag | ✅ Yes |
| Older tags | ❌ No |

## Reporting a Vulnerability

**Do not open a public GitHub issue.** Public disclosure of an unpatched vulnerability puts every user at risk.

Instead, please use one of these private channels:

1. **GitHub Security Advisory (preferred)** — go to [Security → Advisories → Report a vulnerability](../../security/advisories/new). This creates a private discussion only the maintainers can see.
2. **Email** — open an issue requesting a contact address if you cannot use GitHub Advisories.

### What to include

- A clear description of the vulnerability
- Steps to reproduce (PoC code or commands if possible)
- The Gopher-Ops version (`/status` in Telegram, or commit hash)
- The impact you believe this has
- Any suggested mitigations

### What to expect

- **Acknowledgement** within **72 hours** of your report
- **Initial assessment** within **7 days** (severity + reproducibility confirmation)
- **Patch + disclosure** within **30 days** for high/critical issues, **90 days** for medium/low
- **Credit** in the release notes (unless you prefer to remain anonymous)

We follow [coordinated disclosure](https://www.cisa.gov/coordinated-vulnerability-disclosure-process) — please give us a reasonable window to ship a fix before going public.

## Scope

In scope:

- Authentication bypass (unauthorized chat IDs gaining bot access)
- Command injection via AI-generated tool calls
- Audit log tampering that the chain hash fails to detect
- Container escape via misconfigured Docker socket handling
- Privilege escalation via the bot's container actions
- Secrets leakage (API keys, tokens) into logs, audit, or Telegram
- Denial of service against the bot itself (e.g. unbounded resource use)

Out of scope (not security issues):

- Bugs in third-party dependencies — please report to those upstreams (we monitor via Dependabot + CodeQL)
- Issues requiring physical access to the host
- Social engineering of bot operators
- DoS against the **target infrastructure** the bot manages (that's the bot's job — operators authorize it)
- Missing security headers on `/health` and `/metrics` (these endpoints expose intentionally simple JSON / Prometheus formats — wrap in a reverse proxy if you need TLS or auth)

## Security-relevant features

These are intentional design decisions, not bugs:

- **Zero-trust chat ID gating** — only IDs in `AUTHORIZED_CHAT_IDS` can interact. A leaked bot token alone is not enough.
- **HITL-by-default for destructive actions** — `AI_DESTRUCTIVE_ALLOWED` must explicitly be `true` to let the AI directly call Stop/Restart. Default forces operator confirmation buttons.
- **Tamper-evident audit log** — every entry is SHA-256 chained against the previous. Run `/audit verify` to check integrity.
- **Per-operator rate limiting** — `RATE_LIMIT_PER_MINUTE` caps commands per chat, preventing a compromised account from burning your LLM quota or DoS'ing the bot.
- **Markdown escaping** — untrusted strings (container names, log payloads) are escaped before rendering. Fuzz-tested in `internal/bot/deps_test.go`.
- **Distroless container image** — no shell, no package manager, runs as nonroot user (UID 65532).

## Hardening recommendations

For production deployments:

1. Use **per-environment Telegram bots** — never share tokens between staging and prod.
2. Run with `read_only: true` on the container filesystem (the docker-compose ships this).
3. Mount the Docker socket via `:ro` if you only need read access. Most Gopher-Ops features require RW, but you can disable destructive actions with `AI_DESTRUCTIVE_ALLOWED=false`.
4. Place `/health` and `/metrics` behind a reverse proxy with TLS + basic auth if exposed beyond localhost.
5. Rotate `GEMINI_API_KEY` and `TELEGRAM_BOT_TOKEN` quarterly; immediately if a chat ID is removed from the authorized list.
6. Periodically run `/audit verify` (or schedule it) to catch silent tampering.

## Bug bounty

We don't currently offer monetary rewards. We **do** offer:

- Public credit in release notes + a security hall-of-fame in this file (with your consent)
- Maintainer's gratitude 🙏
