---
name: deep-rca-protocol
description: Master Evidence-First Root Cause Analysis protocol for complex infrastructure anomalies, cascading failures, memory leaks, and network partitions.
---

# 🕵️ Master Deep RCA & Evidence-First Protocol

Gunakan runbook ini bila berlaku sebarang incident luar biasa, crash loop berulang, atau isu prestasi yang rumit.

## 🎯 5-Hop Hypothesis-Driven Investigation Workflow:

### 1. 🔍 Hop 1: Symptom & Post-Mortem Inspection
- Panggil `docker_inspect(containerID)` atau `k8s_describe_pod(podName, namespace)`.
- Periksa:
  - **Exit Code**: `137` (OOM / SIGKILL), `139` (Segmentation fault), `1` (Runtime crash), `0` (Clean exit).
  - **OOMKilled flag**: `true` vs `false`.
  - **Restart Count**: Berapa kali container telah crash?
  - **Health Check Probes**: Log kegagalan probe terkini.

### 2. 📜 Hop 2: Deep Log Error Correlation
- Panggil `docker_logs(containerID, filter="error")` atau `docker_logs(containerID, filter="panic")` untuk tapis 500 baris log kepada baris kritikal sahaja.
- Untuk Kubernetes: Panggil `k8s_pod_logs(podName, namespace, lines=100)`.
- Cari punca asal (*root cause*), bukan sekadar simptom hiliran (*downstream symptom* seperti "connection timeout").

### 3. 🌐 Hop 3: Network & Configuration Probe
- Jika isu berkaitan timeout / connection refused: Panggil `docker_network_inspect(containerID)`.
- Semak sama ada subnet bertindih, port binding hilang, atau DNS override salah.

### 4. 🎯 Hop 4: Isolate the "Smoking Gun"
- Bentangkan bukti konkrit kepada Operator:
  - Baris ralat spesifik dalam log atau fail konfigurasi.
  - Nilai metrik (cth: "Memory cecah hard-limit 512MB").
  - Kod ralat sebenar (cth: "Panic: nil pointer dereference at worker.go:42").

### 5. 🛠️ Hop 5: Prescriptive Fix & Self-Learning
- Cadangkan tindakan pembetulan yang tepat (tambah memory, baiki env var, restart pod).
- Jika tindakan bersifat memusnahkan (*destructive*): Cadangkan dalam teks untuk pengesahan Operator (HITL).
- Simpan pengajaran berulang menggunakan `memory_save(fact)` atau cipta runbook baharu menggunakan `skill_manage(action="create", ...)`.
