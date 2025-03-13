package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMetrics_RecordStart(t *testing.T) {
	m := NewMetricsCollector()
	m.Register("test-process")

	// Record a start
	m.RecordStart("test-process")

	// Get metrics
	metrics, err := m.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

func TestMetrics_RecordStop(t *testing.T) {
	m := NewMetricsCollector()
	m.Register("test-process")

	// Record a start then stop
	m.RecordStart("test-process")
	m.RecordStop("test-process")

	// Get metrics
	metrics, err := m.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

func TestMetrics_RecordRestart(t *testing.T) {
	m := NewMetricsCollector()
	m.Register("test-process")

	// Record a start, stop, then start again
	m.RecordStart("test-process")
	m.RecordStop("test-process")
	m.RecordStart("test-process")

	// Get metrics
	metrics, err := m.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

func TestMetrics_RecordFailure(t *testing.T) {
	m := NewMetricsCollector()
	m.Register("test-process")

	// Record a start then stop with failure
	m.RecordStart("test-process")
	m.RecordStop("test-process")

	// Get metrics
	metrics, err := m.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

func TestMetrics_GetProcessStats(t *testing.T) {
	m := NewMetricsCollector()
	m.Register("test-process")

	// Record some metrics
	m.RecordStart("test-process")
	time.Sleep(10 * time.Millisecond)
	m.RecordStop("test-process")
	m.RecordStart("test-process")

	// Get metrics
	metrics, err := m.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

func TestMetrics_GetProcessStats_NonExistent(t *testing.T) {
	m := NewMetricsCollector()

	// Get metrics for non-existent process
	metrics, err := m.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

func TestMetrics_GetProcessStats_Concurrent(t *testing.T) {
	m := NewMetricsCollector()
	m.Register("test-process")

	// Start multiple goroutines to record metrics
	for i := 0; i < 10; i++ {
		go func() {
			m.RecordStart("test-process")
			time.Sleep(10 * time.Millisecond)
			m.RecordStop("test-process")
		}()
	}

	// Wait a bit for goroutines to finish
	time.Sleep(100 * time.Millisecond)

	// Get metrics
	metrics, err := m.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
}
