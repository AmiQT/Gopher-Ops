---
name: k8s-pod-restart
description: Triage CrashLoopBackOff pods in Kubernetes and perform safe rollout restarts.
---

# Kubernetes Pod CrashLoopBackOff Triage Runbook

## 🔍 Investigation Steps:
1. **Inspect Pod Status**: Check if pod status is `CrashLoopBackOff`, `Error`, or `OOMKilled`.
2. **Fetch Pod Logs**: Read previous container termination logs to identify startup crashes or failed readiness/liveness probes.
3. **Verify Config & Secrets**: Ensure mounted ConfigMaps or Secrets are not missing or corrupted.

## 🛠️ Remediation Actions:
1. Trigger rollout restart of deployment if transient deadlock or network timeout occurred.
2. If configuration error: Do NOT blindly restart; notify Operator with specific line in ConfigMap.
3. Save incident pattern with `memory_save`.
