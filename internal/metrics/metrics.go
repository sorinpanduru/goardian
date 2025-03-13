package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

var (
	processRestarts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goardian_process_restarts_total",
			Help: "Total number of process restarts",
		},
		[]string{"name"},
	)

	processUptime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "goardian_process_uptime_seconds",
			Help: "Process uptime in seconds",
		},
		[]string{"name"},
	)

	processMemoryUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "goardian_process_memory_bytes",
			Help: "Process memory usage in bytes",
		},
		[]string{"name"},
	)

	processStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "goardian_process_status",
			Help: "Process status (1=running, 0=stopped)",
		},
		[]string{"name"},
	)

	processRuntime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "goardian_process_runtime_seconds",
			Help:    "Process runtime in seconds",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		},
		[]string{"name"},
	)
)

type MetricsCollector struct {
	mu        sync.RWMutex
	processes map[string]*ProcessMetrics
}

type ProcessMetrics struct {
	startTime time.Time
	stopTime  time.Time
}

func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		processes: make(map[string]*ProcessMetrics),
	}
}

func (mc *MetricsCollector) Register(name string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	mc.processes[name] = &ProcessMetrics{}
}

func (mc *MetricsCollector) RecordStart(name string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if p, ok := mc.processes[name]; ok {
		p.startTime = time.Now()
		p.stopTime = time.Time{}
		processRestarts.WithLabelValues(name).Inc()
	}
}

func (mc *MetricsCollector) RecordStop(name string) {
	mc.mu.Lock()
	defer mc.mu.Unlock()
	if p, ok := mc.processes[name]; ok {
		p.stopTime = time.Now()
		if !p.startTime.IsZero() {
			runtime := p.stopTime.Sub(p.startTime).Seconds()
			processRuntime.WithLabelValues(name).Observe(runtime)
		}
	}
}

func (mc *MetricsCollector) Collect(ch chan<- prometheus.Metric) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	now := time.Now()
	for name, p := range mc.processes {
		if !p.startTime.IsZero() {
			uptime := now.Sub(p.startTime).Seconds()
			processUptime.WithLabelValues(name).Set(uptime)
			processStatus.WithLabelValues(name).Set(1)
		} else {
			processStatus.WithLabelValues(name).Set(0)
		}
	}

	processRestarts.Collect(ch)
	processUptime.Collect(ch)
	processMemoryUsage.Collect(ch)
	processStatus.Collect(ch)
	processRuntime.Collect(ch)
}

func (mc *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	processRestarts.Describe(ch)
	processUptime.Describe(ch)
	processMemoryUsage.Describe(ch)
	processStatus.Describe(ch)
	processRuntime.Describe(ch)
}

func (mc *MetricsCollector) Gather() ([]*dto.MetricFamily, error) {
	return prometheus.DefaultGatherer.Gather()
}
