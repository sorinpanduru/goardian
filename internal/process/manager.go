package process

import (
	"context"
	"sync"

	"github.com/sorinpanduru/goardian/internal/config"
	"github.com/sorinpanduru/goardian/internal/logger"
	"github.com/sorinpanduru/goardian/internal/metrics"
)

// Manager manages all process groups
type Manager struct {
	groups  map[string]*ProcessGroup
	metrics *metrics.MetricsCollector
	logger  *logger.Logger
	mu      sync.RWMutex
}

// NewManager creates a new process manager
func NewManager(metrics *metrics.MetricsCollector, log *logger.Logger) *Manager {
	return &Manager{
		groups:  make(map[string]*ProcessGroup),
		metrics: metrics,
		logger:  log,
	}
}

// AddProcessGroup adds a new process group to the manager
func (m *Manager) AddProcessGroup(cfg config.ProcessConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	group := NewProcessGroup(cfg, m.metrics, m.logger)
	m.groups[cfg.Name] = group
	return nil
}

// GetProcessGroup returns a process group by name
func (m *Manager) GetProcessGroup(name string) *ProcessGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.groups[name]
}

// GetProcessGroups returns all process groups
func (m *Manager) GetProcessGroups() []*ProcessGroup {
	m.mu.RLock()
	defer m.mu.RUnlock()

	groups := make([]*ProcessGroup, 0, len(m.groups))
	for _, group := range m.groups {
		groups = append(groups, group)
	}
	return groups
}

// StartAll starts all process groups
func (m *Manager) StartAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, group := range m.groups {
		if err := group.Start(ctx); err != nil {
			m.logger.ErrorContext(ctx, "failed to start process group",
				"process", group.Config().Name,
				"error", err)
		}
	}
	return nil
}

// StopAll stops all process groups
func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, group := range m.groups {
		if err := group.Stop(ctx); err != nil {
			m.logger.ErrorContext(ctx, "failed to stop process group",
				"process", group.Config().Name,
				"error", err)
		}
	}
	return nil
}

// MonitorAll starts monitoring all process groups
func (m *Manager) MonitorAll(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, group := range m.groups {
		if err := group.Monitor(ctx); err != nil {
			m.logger.ErrorContext(ctx, "failed to monitor process group",
				"process", group.Config().Name,
				"error", err)
		}
	}
	return nil
}
