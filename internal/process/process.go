package process

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/sorinpanduru/goardian/internal/config"
	"github.com/sorinpanduru/goardian/internal/logger"
	"github.com/sorinpanduru/goardian/internal/metrics"
)

// ProcessGroup manages multiple Process instances as replicas of the same service
type ProcessGroup struct {
	config    config.ProcessConfig
	metrics   *metrics.MetricsCollector
	logger    *logger.Logger
	mu        sync.RWMutex
	processes []*Process
	done      chan struct{}
}

// Process represents a single process instance
type Process struct {
	groupName  string // Name of the process group this process belongs to
	instanceID int    // ID of this instance within the group
	cmd        *exec.Cmd
	metrics    *metrics.MetricsCollector
	logger     *logger.Logger
	config     config.ProcessConfig
	done       chan struct{}
	mu         sync.RWMutex
	running    bool       // Tracks if the process is currently running
	exitChan   chan error // Channel to receive process exit notifications
}

// NewProcessGroup creates a new process group
func NewProcessGroup(cfg config.ProcessConfig, metrics *metrics.MetricsCollector, log *logger.Logger) *ProcessGroup {
	return &ProcessGroup{
		config:    cfg,
		metrics:   metrics,
		logger:    log,
		processes: make([]*Process, 0, cfg.NumProcs),
		done:      make(chan struct{}),
	}
}

// Start starts all process instances in the group
func (pg *ProcessGroup) Start(ctx context.Context) error {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Clear any existing processes
	pg.processes = make([]*Process, pg.config.NumProcs)

	// Start processes
	for i := 0; i < pg.config.NumProcs; i++ {
		proc := &Process{
			groupName:  pg.config.Name,
			instanceID: i,
			metrics:    pg.metrics,
			logger:     pg.logger,
			config:     pg.config,
			done:       make(chan struct{}),
		}

		if err := proc.Start(ctx); err != nil {
			pg.logger.ErrorContext(ctx, "failed to start process instance",
				"process", pg.config.Name,
				"instance", i,
				"error", err)
			continue
		}

		pg.processes[i] = proc
	}

	pg.metrics.RecordStart(pg.config.Name)
	pg.logger.InfoContext(ctx, "process group started",
		"process", pg.config.Name,
		"instances", pg.config.NumProcs)
	return nil
}

// Stop stops all process instances in the group
func (pg *ProcessGroup) Stop(ctx context.Context) error {
	pg.mu.Lock()
	defer pg.mu.Unlock()

	// Stop all processes
	var wg sync.WaitGroup
	for _, proc := range pg.processes {
		if proc != nil {
			wg.Add(1)
			go func(p *Process) {
				defer wg.Done()
				if err := p.Stop(ctx); err != nil {
					pg.logger.WarnContext(ctx, "error stopping process instance",
						"process", pg.config.Name,
						"instance", p.instanceID,
						"error", err)
				}
			}(proc)
		}
	}

	// Wait for all processes to stop or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		pg.metrics.RecordStop(pg.config.Name)
		pg.logger.InfoContext(ctx, "all processes in group stopped",
			"process", pg.config.Name)
		close(pg.done)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(pg.config.StopTimeout):
		pg.logger.WarnContext(ctx, "timeout stopping processes in group",
			"process", pg.config.Name)
		close(pg.done)
		return fmt.Errorf("timeout stopping processes in group %s", pg.config.Name)
	}
}

// Monitor monitors a single process instance
func (p *Process) Monitor(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			p.Stop(ctx)
			return ctx.Err()
		case <-p.done:
			return nil
		case err := <-p.exitChan:
			// Process has exited
			exitCode := 0
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}

			p.logger.InfoContext(ctx, "process instance exited, restarting",
				"process", p.groupName,
				"instance", p.instanceID,
				"exitCode", exitCode)

			// Start a new process
			if err := p.Start(ctx); err != nil {
				p.logger.ErrorContext(ctx, "failed to restart process instance",
					"process", p.groupName,
					"instance", p.instanceID,
					"error", err)
				continue
			}

			p.logger.InfoContext(ctx, "restarted process instance",
				"process", p.groupName,
				"instance", p.instanceID)
		}
	}
}

// Monitor monitors all process instances in the group
func (pg *ProcessGroup) Monitor(ctx context.Context) error {
	var wg sync.WaitGroup
	for _, proc := range pg.processes {
		if proc != nil {
			wg.Add(1)
			go func(p *Process) {
				defer wg.Done()
				if err := p.Monitor(ctx); err != nil {
					pg.logger.ErrorContext(ctx, "process monitoring error",
						"process", pg.config.Name,
						"instance", p.instanceID,
						"error", err)
				}
			}(proc)
		}
	}

	// Wait for all process monitors to finish
	wg.Wait()
	return nil
}

// Config returns the process group configuration
func (pg *ProcessGroup) Config() config.ProcessConfig {
	pg.mu.RLock()
	defer pg.mu.RUnlock()
	return pg.config
}

// Start starts a single process instance
func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Create exit channel if it doesn't exist
	if p.exitChan == nil {
		p.exitChan = make(chan error, 1)
	}

	// Create and start the process
	cmd := exec.CommandContext(ctx, p.config.Command, p.config.Args...)
	cmd.Dir = p.config.WorkingDir
	cmd.Env = p.config.Environment

	// Set up pipes for stdout and stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	p.cmd = cmd
	p.running = true

	// Start a goroutine to wait for the process to exit
	go func() {
		err := p.cmd.Wait()
		p.mu.Lock()
		p.running = false
		p.mu.Unlock()
		p.exitChan <- err
	}()

	// Handle output with structured logging
	go func(stdout, stderr io.ReadCloser) {
		// Create a custom writer for stdout
		stdoutWriter := &logWriter{
			logger:   p.logger,
			process:  p.groupName,
			stream:   "stdout",
			instance: p.instanceID,
		}

		// Create a custom writer for stderr
		stderrWriter := &logWriter{
			logger:   p.logger,
			process:  p.groupName,
			stream:   "stderr",
			instance: p.instanceID,
		}

		io.Copy(stdoutWriter, stdout)
		io.Copy(stderrWriter, stderr)
	}(stdout, stderr)

	// Wait for process to start successfully
	ready := make(chan struct{})
	go func() {
		// Give process a moment to start
		time.Sleep(100 * time.Millisecond)

		// Check if process is still running
		if p.cmd.Process == nil {
			p.mu.Lock()
			p.running = false
			p.mu.Unlock()
			close(ready)
			return
		}

		// Process is running
		close(ready)
	}()

	select {
	case <-ready:
		p.logger.InfoContext(ctx, "process instance started",
			"process", p.groupName,
			"instance", p.instanceID)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.config.StartTimeout):
		return fmt.Errorf("timeout waiting for process to start")
	}
}

// Stop stops a process instance
func (p *Process) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil || p.cmd.Process == nil {
		// Process already stopped
		p.running = false
		return nil
	}

	// First try graceful shutdown with SIGTERM
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.logger.WarnContext(ctx, "failed to send SIGTERM",
			"process", p.groupName,
			"instance", p.instanceID,
			"error", err)
	}

	// Wait for process to stop gracefully
	select {
	case <-p.exitChan:
		p.logger.InfoContext(ctx, "process instance stopped gracefully",
			"process", p.groupName,
			"instance", p.instanceID)
		p.running = false
		close(p.done)
		p.cmd = nil
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.config.StopTimeout):
		p.logger.WarnContext(ctx, "graceful shutdown failed, sending SIGKILL",
			"process", p.groupName,
			"instance", p.instanceID)

		// Send SIGKILL
		if err := p.cmd.Process.Signal(syscall.SIGKILL); err != nil {
			p.logger.WarnContext(ctx, "failed to send SIGKILL",
				"process", p.groupName,
				"instance", p.instanceID,
				"error", err)
		}

		// Wait for process to exit after SIGKILL
		select {
		case <-p.exitChan:
			p.logger.InfoContext(ctx, "process instance stopped after SIGKILL",
				"process", p.groupName,
				"instance", p.instanceID)
			p.running = false
			close(p.done)
			p.cmd = nil
			return nil
		case <-time.After(5 * time.Second):
			p.logger.ErrorContext(ctx, "process instance still running after SIGKILL",
				"process", p.groupName,
				"instance", p.instanceID)
			return fmt.Errorf("process still running after SIGKILL")
		}
	}
}

// IsRunning checks if a process is running
func (p *Process) IsRunning() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.running
}

// logWriter implements io.Writer for structured logging of process output
type logWriter struct {
	logger   *logger.Logger
	process  string
	stream   string
	instance int
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	// Trim trailing newlines
	msg := string(p)
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}

	w.logger.Info(msg,
		"process", w.process,
		"stream", w.stream,
		"instance", w.instance,
		"type", "subprocess_output")

	return len(p), nil
}
