# 📋 SRE Incident Post-Mortem: Redis High Memory OOM Outage

```yaml
incident_id: "INC-20260903-REDIS-OOM"
severity: "CRITICAL"
service: "redis-cart-session"
detected_at: "2026-09-03T14:12:00+08:00"
resolved_at: "2026-09-03T14:26:30+08:00"
time_to_recovery: "14m30s"
```

## 1. 🔍 Executive Summary
Cart and checkout services experienced intermittent HTTP 504 Gateway Timeouts for 14 minutes due to the `redis-cart-session` container crashing repeatedly in an out-of-memory loop (OOMKilled).

## 2. 🧠 Root Cause Analysis (Smoking Gun)
Release v2.4.1 deployed at 14:05 introduced a new user cart analytics background worker that wrote cart state keys without setting expiration TTLs. Key volume grew to 1.8M keys within 7 minutes, exceeding the 1GB container memory threshold.

### 📜 Log & Diagnostic Evidence
```text
kernel: [12948.102] Out of memory: Killed process 41209 (redis-server) total-vm:1048576kB, anon-rss:998144kB
docker_inspect: ExitCode=137, OOMKilled=true, RestartCount=5, MemoryLimit=1073741824
```

## 3. 🛠️ Remediation Actions Taken
1. Autonomously adjusted container memory limit to 2GB via Gopher-Ops.
2. Flushed dangling transient session keys older than 24 hours.
3. Restarted `redis-cart-session` container and verified normal healthcheck.

## 4. 🛡️ Preventative Measures & Lessons Learned
1. Updated Redis configuration with `maxmemory-policy volatile-lru`.
2. Created procedural skill `skills/docker-oom-triage.md` for automated fast-path triage.
3. Saved persistent memory: *"Redis cart service requires volatile-lru and minimum 2GB memory boundary."*

---
*Generated autonomously by Gopher-Ops SRE Agent.*
