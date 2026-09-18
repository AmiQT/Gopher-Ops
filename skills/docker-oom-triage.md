---
name: docker-oom-triage
description: Triage and safely remediate Docker containers killed by Out-Of-Memory (OOM, exit code 137).
---

# Docker Out-Of-Memory (OOM) Triage Runbook

## 🔍 Investigation Steps:
1. **Verify Exit Code**: Check if container exited with code `137` or `OOMKilled: true`.
2. **Inspect Container Logs**: Use `GetContainerLogs(containerID)` to capture heap allocations, memory leak stacktraces, or runaway worker jobs.
3. **Check Host Metrics**: Inspect memory spikes from recent telemetry to see if host memory was exhausted.

## 🛠️ Remediation Actions:
1. If temporary traffic surge: Restart container using `RestartContainer(containerID)`.
2. If memory leak detected: Recommend bumping memory limit in `docker-compose.yml` or container flags.
3. Record root cause and save durable findings using `memory_save`.
