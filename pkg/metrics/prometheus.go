package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Host-level gauges
	HostCPUPct = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "gopherops_host_cpu_percent",
		Help: "Host CPU usage percent (1-second sample).",
	})
	HostRAMPct = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "gopherops_host_ram_percent",
		Help: "Host RAM usage percent.",
	})

	// Per-container gauges — labelled by container name
	ContainerCPUPct = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_cpu_percent",
		Help: "Container CPU usage percent.",
	}, []string{"name"})
	ContainerRAMUsedMB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_ram_used_mb",
		Help: "Container RAM used in MB.",
	}, []string{"name"})
	ContainerRAMPct = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_ram_percent",
		Help: "Container RAM usage as percent of its limit.",
	}, []string{"name"})
	ContainerNetRxMB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_net_rx_mb",
		Help: "Container cumulative network bytes received (MB).",
	}, []string{"name"})
	ContainerNetTxMB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_net_tx_mb",
		Help: "Container cumulative network bytes transmitted (MB).",
	}, []string{"name"})
	ContainerBlkReadMB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_blk_read_mb",
		Help: "Container cumulative block device bytes read (MB).",
	}, []string{"name"})
	ContainerBlkWriteMB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_blk_write_mb",
		Help: "Container cumulative block device bytes written (MB).",
	}, []string{"name"})
	ContainerPIDs = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_pids",
		Help: "Number of processes running inside the container.",
	}, []string{"name"})
	ContainerUp = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_container_up",
		Help: "1 if the container state is 'running', 0 otherwise.",
	}, []string{"name"})

	// Disk gauges — labelled by mountpoint
	DiskUsedPct = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_disk_used_percent",
		Help: "Filesystem used percent.",
	}, []string{"mount", "fstype"})
	DiskUsedGB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_disk_used_gb",
		Help: "Filesystem used gigabytes.",
	}, []string{"mount", "fstype"})
	DiskTotalGB = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "gopherops_disk_total_gb",
		Help: "Filesystem total gigabytes.",
	}, []string{"mount", "fstype"})

	// Bot operational counters
	AlertsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gopherops_alerts_total",
		Help: "Total number of alerts fired, by type.",
	}, []string{"type"})
	RCATotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "gopherops_rca_total",
		Help: "Total number of AI root-cause analyses performed.",
	})
	AIRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gopherops_ai_requests_total",
		Help: "Total AI requests by outcome (success, retry, error).",
	}, []string{"outcome"})
	AILatencySeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "gopherops_ai_latency_seconds",
		Help:    "AI round-trip latency in seconds.",
		Buckets: prometheus.DefBuckets,
	})
	TelegramMsgTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "gopherops_telegram_messages_total",
		Help: "Telegram messages sent, by outcome (ok, error).",
	}, []string{"outcome"})
)

// Register adds all Gopher-Ops metrics to the default Prometheus registry.
// Call once from main() before starting any HTTP server.
func Register() {
	prometheus.MustRegister(
		HostCPUPct, HostRAMPct,
		ContainerCPUPct, ContainerRAMUsedMB, ContainerRAMPct,
		ContainerNetRxMB, ContainerNetTxMB,
		ContainerBlkReadMB, ContainerBlkWriteMB, ContainerPIDs, ContainerUp,
		DiskUsedPct, DiskUsedGB, DiskTotalGB,
		AlertsTotal, RCATotal, AIRequestsTotal, AILatencySeconds,
		TelegramMsgTotal,
	)
}

// Handler returns the standard Prometheus HTTP handler for /metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}
