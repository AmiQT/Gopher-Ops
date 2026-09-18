---
name: container-cleanup
description: Inspect and safely purge dangling images, stopped containers, and reclaim host disk space.
---

# Host Disk Space & Container Cleanup Runbook

## 🔍 Investigation Steps:
1. **Check Disk Space**: Verify if host root partition usage exceeds 85%.
2. **List Stopped Containers**: Identify exited containers that are not part of active staging or prod stacks.
3. **List Dangling Images**: Identify `<none>:<none>` intermediate build layers.

## 🛠️ Remediation Actions:
1. Stop runaway orphan test containers.
2. Request Operator confirmation before running deep prune operations.
3. Document freed capacity in post-cleanup summary.
