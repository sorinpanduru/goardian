package metrics

import (
	"fmt"
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

	processFailureRestarts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "goardian_process_failure_restarts_total",
			Help: "Total number of process restarts due to failures",
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
			Help: "Process status (0=running, 1=stopped, 2=failed)",
		},
		[]string{"name"},
	)

	processInstanceStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "goardian_process_instance_status",
			Help: "Process instance status (0=running, 1=stopped, 2=failed)",
		},
		[]string{"name", "instance"},
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

// RecordStateChange records a change in the running state of a process instance
func (mc *MetricsCollector) RecordStateChange(name string, instance int, running bool) {
	// Convert boolean running to numeric state (0=running, 1=stopped)
	state := 1 // Stopped
	if running {
		state = 0 // Running
	}
	processInstanceStatus.WithLabelValues(name, fmt.Sprintf("%d", instance)).Set(float64(state))
}

// RecordMemoryUsage records the memory usage for a specific process instance
func (mc *MetricsCollector) RecordMemoryUsage(name string, instance int, memoryBytes float64) {
	processMemoryUsage.WithLabelValues(name).Set(memoryBytes)
}

// RecordFailureRestart records a restart due to a failure
func (mc *MetricsCollector) RecordFailureRestart(name string) {
	processFailureRestarts.WithLabelValues(name).Inc()
}

// RecordProcessState records the state of a process (running, stopped, or failed)
func (mc *MetricsCollector) RecordProcessState(name string, state int) {
	processStatus.WithLabelValues(name).Set(float64(state))
}

// RecordInstanceState records the state of a process instance (running, stopped, or failed)
func (mc *MetricsCollector) RecordInstanceState(name string, instance int, state int) {
	processInstanceStatus.WithLabelValues(name, fmt.Sprintf("%d", instance)).Set(float64(state))
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
	processFailureRestarts.Collect(ch)
	processUptime.Collect(ch)
	processMemoryUsage.Collect(ch)
	processStatus.Collect(ch)
	processInstanceStatus.Collect(ch)
	processRuntime.Collect(ch)
}

func (mc *MetricsCollector) Describe(ch chan<- *prometheus.Desc) {
	processRestarts.Describe(ch)
	processFailureRestarts.Describe(ch)
	processUptime.Describe(ch)
	processMemoryUsage.Describe(ch)
	processStatus.Describe(ch)
	processInstanceStatus.Describe(ch)
	processRuntime.Describe(ch)
}

func (mc *MetricsCollector) Gather() ([]*dto.MetricFamily, error) {
	return prometheus.DefaultGatherer.Gather()
}
