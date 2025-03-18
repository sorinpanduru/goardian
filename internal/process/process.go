package process

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sorinpanduru/goardian/internal/config"
	"github.com/sorinpanduru/goardian/internal/logger"
	"github.com/sorinpanduru/goardian/internal/metrics"
)

func init() {
	// Seed the random number generator for backoff jitter
	rand.Seed(time.Now().UnixNano())
}

// ProcessState represents the desired state of a process
type ProcessState int

const (
	// StateRunning indicates the process should be running and will be auto-restarted
	StateRunning ProcessState = iota
	// StateStopped indicates the process was manually stopped and should not be auto-restarted
	StateStopped
	// StateFailed indicates the process has reached max_restarts and will not be restarted
	StateFailed
)

// ProcessGroup manages multiple Process instances as replicas of the same service
type ProcessGroup struct {
	config    config.ProcessConfig
	metrics   *metrics.MetricsCollector
	logger    *logger.Logger
	mu        sync.RWMutex
	processes []*Process
	done      chan struct{}
	closeOnce sync.Once
}

// Process represents a single process instance
type Process struct {
	groupName           string // Name of the process group this process belongs to
	instanceID          int    // ID of this instance within the group
	cmd                 *exec.Cmd
	metrics             *metrics.MetricsCollector
	logger              *logger.Logger
	config              config.ProcessConfig
	done                chan struct{}
	mu                  sync.RWMutex
	running             bool         // Tracks if the process is currently running
	state               ProcessState // Tracks the desired state of the process
	exitChan            chan error   // Channel to receive process exit notifications
	closeOnce           sync.Once
	StateChanged        func(p *Process) // Callback for state changes
	consecutiveFailures int              // Count of consecutive non-zero exits
	lastRestartTime     time.Time        // Time of the last restart attempt
	startTime           time.Time        // Time when the process was started
	memoryUsage         uint64           // Last measured memory usage in bytes
	lastMemoryUpdate    time.Time        // Time of last memory usage update
	totalRestarts       int              // Total number of restarts
	failureRestarts     int              // Number of restarts due to failures
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
			groupName:           pg.config.Name,
			instanceID:          i,
			metrics:             pg.metrics,
			logger:              pg.logger,
			config:              pg.config,
			done:                make(chan struct{}),
			state:               StateRunning,
			consecutiveFailures: 0,
			lastRestartTime:     time.Time{},
			startTime:           time.Time{},
			memoryUsage:         0,
			lastMemoryUpdate:    time.Time{},
		}

		// Set up state change callback
		proc.StateChanged = func(p *Process) {
			// Add additional metrics/logging if needed
			pg.metrics.RecordStateChange(pg.config.Name, p.instanceID, p.IsRunning())
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

	// Get current processes slice
	processes := make([]*Process, len(pg.processes))
	copy(processes, pg.processes)
	pg.mu.Unlock()

	// Stop all processes
	var wg sync.WaitGroup
	stopErrors := make([]error, 0)
	var errorsMu sync.Mutex

	for i, proc := range processes {
		if proc != nil {
			wg.Add(1)
			go func(p *Process, idx int) {
				defer wg.Done()
				if err := p.Stop(ctx); err != nil {
					errorsMu.Lock()
					stopErrors = append(stopErrors, fmt.Errorf("failed to stop instance %d: %w", idx, err))
					errorsMu.Unlock()

					pg.logger.WarnContext(ctx, "error stopping process instance",
						"process", pg.config.Name,
						"instance", p.instanceID,
						"error", err)
				}
			}(proc, i)
		}
	}

	// Wait for all processes to stop or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Use a shorter timeout for the process group
	groupTimeout := pg.config.StopTimeout
	if groupTimeout > 10*time.Second {
		groupTimeout = 10 * time.Second
	}

	var stoppedSuccessfully bool
	var err error

	select {
	case <-done:
		stoppedSuccessfully = true
	case <-ctx.Done():
		err = ctx.Err()
	case <-time.After(groupTimeout):
		pg.logger.WarnContext(ctx, "timeout stopping processes in group, verifying status",
			"process", pg.config.Name)
	}

	// Verify all processes are stopped, regardless of timeout
	allStopped := true
	var unstoppedInstances []int

	pg.mu.RLock()
	for i, proc := range pg.processes {
		if proc != nil && proc.IsRunning() {
			allStopped = false
			unstoppedInstances = append(unstoppedInstances, i)
		}
	}
	pg.mu.RUnlock()

	if !allStopped {
		// Some processes are still running, attempt to force stop them
		pg.logger.WarnContext(ctx, "some process instances still running after stop, forcing stop",
			"process", pg.config.Name,
			"instances", fmt.Sprintf("%v", unstoppedInstances))

		// Try to forcefully stop the remaining processes
		for _, idx := range unstoppedInstances {
			pg.mu.RLock()
			proc := pg.processes[idx]
			pg.mu.RUnlock()

			if proc != nil && proc.IsRunning() {
				// Create a shorter context for the force stop
				forceCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				err := proc.Stop(forceCtx)
				cancel()

				if err != nil {
					pg.logger.ErrorContext(ctx, "failed to force stop process instance",
						"process", pg.config.Name,
						"instance", idx,
						"error", err)
				}
			}
		}

		// Verify again after force stop
		pg.mu.RLock()
		allStoppedAfterForce := true
		for i, proc := range pg.processes {
			if proc != nil && proc.IsRunning() {
				allStoppedAfterForce = false
				pg.logger.ErrorContext(ctx, "process instance still running after force stop",
					"process", pg.config.Name,
					"instance", i)
			}
		}
		pg.mu.RUnlock()

		if !allStoppedAfterForce {
			err = fmt.Errorf("failed to stop all process instances")
		} else {
			// All processes eventually stopped
			stoppedSuccessfully = true
		}
	}

	// Record metrics and clean up regardless of outcome
	pg.metrics.RecordStop(pg.config.Name)

	if stoppedSuccessfully {
		pg.logger.InfoContext(ctx, "all processes in group stopped",
			"process", pg.config.Name)
	}

	pg.mu.Lock()
	pg.closeOnce.Do(func() {
		close(pg.done)
	})
	pg.mu.Unlock()

	if len(stopErrors) > 0 && err == nil {
		err = fmt.Errorf("errors stopping some processes: %v", stopErrors)
	}

	return err
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

			p.mu.RLock()
			state := p.state
			p.mu.RUnlock()

			// Only restart if the process is supposed to be running
			if state == StateRunning {
				// Update failure count if exit code is non-zero
				if exitCode != 0 {
					p.mu.Lock()
					p.consecutiveFailures++
					p.failureRestarts++
					p.metrics.RecordFailureRestart(p.groupName)

					// Check if we've reached max_restarts
					if p.failureRestarts >= p.config.MaxRestarts {
						p.state = StateFailed
						p.running = false
						p.mu.Unlock()

						// Record failed state in metrics
						p.metrics.RecordInstanceState(p.groupName, p.instanceID, 2)

						p.logger.ErrorContext(ctx, "process instance reached max_restarts, marking as failed",
							"process", p.groupName,
							"instance", p.instanceID,
							"max_restarts", p.config.MaxRestarts,
							"failure_restarts", p.failureRestarts)
						return fmt.Errorf("process reached max_restarts (%d)", p.config.MaxRestarts)
					}

					backoffDuration := p.getBackoffDuration()
					p.mu.Unlock()

					if backoffDuration > 0 {
						p.logger.WarnContext(ctx, "process instance exited with error, backing off before restart",
							"process", p.groupName,
							"instance", p.instanceID,
							"exitCode", exitCode,
							"consecutiveFailures", p.consecutiveFailures,
							"backoffDuration", backoffDuration.String())

						// Create a timer for the backoff
						backoffTimer := time.NewTimer(backoffDuration)
						select {
						case <-backoffTimer.C:
							// Backoff complete, continue with restart
							p.logger.InfoContext(ctx, "backoff complete, restarting process instance",
								"process", p.groupName,
								"instance", p.instanceID)
						case <-ctx.Done():
							// Context canceled during backoff
							backoffTimer.Stop()
							return ctx.Err()
						case <-p.done:
							// Process group was stopped during backoff
							backoffTimer.Stop()
							return nil
						}
					}
				} else {
					// Reset failure count on clean exit
					p.mu.Lock()
					p.consecutiveFailures = 0
					p.mu.Unlock()
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

					// Increment failure count for restart failures too
					p.mu.Lock()
					p.consecutiveFailures++
					p.failureRestarts++
					p.metrics.RecordFailureRestart(p.groupName)
					p.mu.Unlock()

					// Short delay before retrying to prevent tight restart loops
					time.Sleep(1 * time.Second)
					continue
				}

				// Record total restart
				p.mu.Lock()
				p.totalRestarts++
				p.mu.Unlock()

				// Reset failure count on successful restart
				if exitCode == 0 {
					p.resetBackoff()
				}

				p.logger.InfoContext(ctx, "restarted process instance",
					"process", p.groupName,
					"instance", p.instanceID)
			} else {
				p.logger.InfoContext(ctx, "process instance exited (not restarting - manually stopped)",
					"process", p.groupName,
					"instance", p.instanceID,
					"exitCode", exitCode)
				return nil
			}
		}
	}
}

// Monitor monitors all process instances in the group
func (pg *ProcessGroup) Monitor(ctx context.Context) error {
	for _, proc := range pg.processes {
		if proc != nil {
			// Log the initial state for debugging
			pg.logger.InfoContext(ctx, "monitoring process with initial state",
				"process", pg.config.Name,
				"instance", proc.instanceID,
				"state", proc.GetState())

			go func(p *Process) {
				if err := p.Monitor(ctx); err != nil {
					pg.logger.ErrorContext(ctx, "process monitoring error",
						"process", pg.config.Name,
						"instance", p.instanceID,
						"error", err)
				}
			}(proc)
		}
	}

	// Start a goroutine to periodically update memory usage
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-pg.done:
				return
			case <-ticker.C:
				pg.updateProcessMetrics(ctx)
			}
		}
	}()

	return nil
}

// updateProcessMetrics updates metrics like memory usage for all processes in the group
func (pg *ProcessGroup) updateProcessMetrics(ctx context.Context) {
	pg.mu.RLock()
	processes := make([]*Process, len(pg.processes))
	copy(processes, pg.processes)
	pg.mu.RUnlock()

	for _, proc := range processes {
		if proc != nil && proc.IsRunning() {
			proc.UpdateMemoryUsage(ctx)
		}
	}
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
	// Set state to running before starting
	p.state = StateRunning
	wasRunning := p.running
	p.lastRestartTime = time.Now() // Record restart attempt time
	p.startTime = time.Now()       // Record process start time
	p.mu.Unlock()

	// Record state change in metrics
	p.metrics.RecordInstanceState(p.groupName, p.instanceID, 0)
	// Record instance start in metrics
	p.metrics.RecordInstanceStart(p.groupName, p.instanceID)

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

	// Notify about state change if there was a change
	if !wasRunning && p.StateChanged != nil {
		stateChanged := p.StateChanged
		go func(proc *Process) {
			stateChanged(proc)
		}(p)
	}

	// Start a goroutine to wait for the process to exit
	go func() {
		err := p.cmd.Wait()
		p.mu.Lock()
		wasRunning := p.running
		p.running = false

		// Calculate runtime for metrics
		var runtimeSeconds float64 = 0
		if !p.startTime.IsZero() {
			runtimeSeconds = time.Since(p.startTime).Seconds()
		}

		p.mu.Unlock()

		// Record runtime metrics
		p.metrics.RecordInstanceStop(p.groupName, p.instanceID, runtimeSeconds)

		// Notify about state change
		if wasRunning && p.StateChanged != nil {
			p.StateChanged(p)
		}

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
			wasRunning := p.running
			p.running = false
			p.mu.Unlock()

			// Notify about state change
			if wasRunning && p.StateChanged != nil {
				p.StateChanged(p)
			}

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
	// Set state to stopped
	p.state = StateStopped
	wasRunning := p.running

	// Calculate runtime for metrics if the process was running
	var runtimeSeconds float64 = 0
	if wasRunning && !p.startTime.IsZero() {
		runtimeSeconds = time.Since(p.startTime).Seconds()
	}

	// If the process isn't running or doesn't exist, handle it immediately
	if p.cmd == nil || p.cmd.Process == nil || !p.running {
		p.running = false
		p.closeOnce.Do(func() {
			close(p.done)
		})
		p.mu.Unlock()

		// Record state change in metrics
		p.metrics.RecordInstanceState(p.groupName, p.instanceID, 1)

		// Only record runtime if we had a valid runtime calculation
		if runtimeSeconds > 0 {
			p.metrics.RecordInstanceStop(p.groupName, p.instanceID, runtimeSeconds)
		}

		// Notify about state change if there was a change
		if wasRunning && p.StateChanged != nil {
			p.StateChanged(p)
		}

		p.logger.InfoContext(ctx, "process instance already stopped",
			"process", p.groupName,
			"instance", p.instanceID)
		return nil
	}

	// Check for exitChan events before proceeding
	// This ensures we don't miss any exit events that might have occurred
	select {
	case err := <-p.exitChan:
		// Process has already exited, handle it
		p.running = false
		p.cmd = nil
		p.closeOnce.Do(func() {
			close(p.done)
		})
		p.mu.Unlock()

		// Record runtime metrics if we had a valid runtime calculation
		if runtimeSeconds > 0 {
			p.metrics.RecordInstanceStop(p.groupName, p.instanceID, runtimeSeconds)
		}

		// Notify about state change
		if p.StateChanged != nil {
			p.StateChanged(p)
		}

		exitCode := 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}

		p.logger.InfoContext(ctx, "process instance already exited before stop",
			"process", p.groupName,
			"instance", p.instanceID,
			"exitCode", exitCode)
		return nil
	default:
		// Process hasn't exited yet, continue with stop
	}

	// Try graceful shutdown with SIGTERM
	process := p.cmd.Process // Store reference to avoid race conditions
	pid := process.Pid       // Remember the PID for additional checks
	p.mu.Unlock()

	// Double-check if the process is still running using the OS
	if !processExists(pid) {
		p.mu.Lock()
		p.running = false
		p.cmd = nil
		p.closeOnce.Do(func() {
			close(p.done)
		})
		p.mu.Unlock()

		// Notify about state change
		if p.StateChanged != nil {
			p.StateChanged(p)
		}

		p.logger.InfoContext(ctx, "process instance already exited (OS check)",
			"process", p.groupName,
			"instance", p.instanceID)
		return nil
	}

	// Send SIGTERM
	if err := process.Signal(syscall.SIGTERM); err != nil {
		// Check if process has already exited
		if err.Error() == "os: process already finished" {
			p.mu.Lock()
			wasRunning := p.running
			p.running = false
			p.cmd = nil
			p.closeOnce.Do(func() {
				close(p.done)
			})
			p.mu.Unlock()

			// Notify about state change if there was a change
			if wasRunning && p.StateChanged != nil {
				p.StateChanged(p)
			}

			p.logger.InfoContext(ctx, "process instance already exited",
				"process", p.groupName,
				"instance", p.instanceID)
			return nil
		}

		p.logger.WarnContext(ctx, "failed to send SIGTERM",
			"process", p.groupName,
			"instance", p.instanceID,
			"error", err)
	}

	return nil
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

func (w *logWriter) Write(p []byte) (int, error) {
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

// InstanceID returns the instance ID of the process
func (p *Process) InstanceID() int {
	return p.instanceID
}

// GroupName returns the name of the process group
func (p *Process) GroupName() string {
	return p.groupName
}

// GetProcesses returns all process instances in the group
func (pg *ProcessGroup) GetProcesses() []*Process {
	pg.mu.RLock()
	defer pg.mu.RUnlock()

	processes := make([]*Process, len(pg.processes))
	copy(processes, pg.processes)
	return processes
}

// processExists checks if a process with the given PID exists
func processExists(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// On Unix systems, FindProcess always succeeds, so we need to send
	// a signal 0 to check if the process exists
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// getBackoffDuration calculates the backoff duration based on consecutive failures
// using an exponential backoff strategy with jitter
func (p *Process) getBackoffDuration() time.Duration {
	if p.consecutiveFailures <= 0 {
		return 0
	}

	// Base delay is 1 second
	baseDelay := 1 * time.Second

	// Calculate exponential backoff: baseDelay * 2^(failures-1)
	// but cap at 5 failures to prevent extremely long waits
	exponent := p.consecutiveFailures
	if exponent > 5 {
		exponent = 5
	}

	// Calculate delay with exponential backoff
	backoff := baseDelay * time.Duration(1<<uint(exponent-1))

	// Add jitter: +/- 20% of the calculated backoff
	jitter := time.Duration(rand.Float64()*0.4-0.2) * backoff
	backoff = backoff + jitter

	// Cap at 60 seconds maximum
	maxBackoff := 60 * time.Second
	if backoff > maxBackoff {
		backoff = maxBackoff
	}

	return backoff
}

// resetBackoff resets the consecutive failures counter
func (p *Process) resetBackoff() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consecutiveFailures = 0
	p.lastRestartTime = time.Time{}
}

// GetBackoffState returns information about the current backoff state of the process
func (p *Process) GetBackoffState() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	nextBackoff := p.getBackoffDuration()
	timeSinceLastRestart := time.Duration(0)
	if !p.lastRestartTime.IsZero() {
		timeSinceLastRestart = time.Since(p.lastRestartTime)
	}

	return map[string]interface{}{
		"consecutiveFailures":  p.consecutiveFailures,
		"lastRestartTime":      p.lastRestartTime,
		"nextBackoffDuration":  nextBackoff.String(),
		"timeSinceLastRestart": timeSinceLastRestart.String(),
	}
}

// GetUptime returns the current uptime of the process in seconds
func (p *Process) GetUptime() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.running || p.startTime.IsZero() {
		return 0
	}

	return time.Since(p.startTime).Seconds()
}

// GetMemoryUsage returns the last measured memory usage in bytes
func (p *Process) GetMemoryUsage() uint64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.memoryUsage
}

// GetStartTime returns the start time of the process
func (p *Process) GetStartTime() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.startTime
}

// UpdateMemoryUsage updates the memory usage of the process
func (p *Process) UpdateMemoryUsage(ctx context.Context) {
	if !p.IsRunning() {
		return
	}

	p.mu.RLock()
	pid := int64(-1)
	if p.cmd != nil && p.cmd.Process != nil {
		pid = int64(p.cmd.Process.Pid)
	}
	p.mu.RUnlock()

	if pid < 0 {
		return
	}

	// Use platform-specific method to get process memory usage
	memUsage, err := getProcessMemoryUsage(pid)
	if err != nil {
		p.logger.WarnContext(ctx, "failed to get process memory usage",
			"process", p.groupName,
			"instance", p.instanceID,
			"error", err)
		return
	}

	p.mu.Lock()
	p.memoryUsage = memUsage
	p.lastMemoryUpdate = time.Now()
	p.mu.Unlock()

	// Record metrics
	p.metrics.RecordMemoryUsage(p.groupName, p.instanceID, float64(memUsage))
}

// getProcessMemoryUsage gets the memory usage for a process with the given PID
func getProcessMemoryUsage(pid int64) (uint64, error) {
	// Platform-specific implementation for getting process memory usage
	// For Unix/Linux systems
	if runtime.GOOS == "linux" {
		return getLinuxMemoryUsage(pid)
	} else if runtime.GOOS == "darwin" {
		return getDarwinMemoryUsage(pid)
	} else if runtime.GOOS == "windows" {
		return getWindowsMemoryUsage(pid)
	}

	return 0, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
}

// getLinuxMemoryUsage gets memory usage on Linux systems
func getLinuxMemoryUsage(pid int64) (uint64, error) {
	// Read /proc/[pid]/statm for memory stats
	statmPath := fmt.Sprintf("/proc/%d/statm", pid)
	statmContent, err := os.ReadFile(statmPath)
	if err != nil {
		return 0, err
	}

	// Parse statm file - first value is total program size in pages
	fields := strings.Fields(string(statmContent))
	if len(fields) < 1 {
		return 0, fmt.Errorf("invalid format in %s", statmPath)
	}

	sizeInPages, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}

	// Convert pages to bytes (usually 4KB per page)
	pageSize := uint64(syscall.Getpagesize())
	return sizeInPages * pageSize, nil
}

// getDarwinMemoryUsage gets memory usage on macOS systems
func getDarwinMemoryUsage(pid int64) (uint64, error) {
	// Use ps command to get memory usage on macOS
	cmd := exec.Command("ps", "-o", "rss=", "-p", fmt.Sprintf("%d", pid))
	output, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	// Parse the output - RSS in kilobytes
	rssKB, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, err
	}

	// Convert KB to bytes
	return rssKB * 1024, nil
}

// getWindowsMemoryUsage gets memory usage on Windows systems
func getWindowsMemoryUsage(pid int64) (uint64, error) {
	// On Windows, we'd use a different approach, possibly with wmic or PowerShell
	// For this implementation, we'll just return a placeholder
	// In a real implementation, you would use Windows API or execute wmic/PowerShell
	return 0, fmt.Errorf("Windows memory usage tracking not implemented")
}

// GetRestartStats returns information about process restarts
func (p *Process) GetRestartStats() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return map[string]interface{}{
		"totalRestarts":   p.totalRestarts,
		"failureRestarts": p.failureRestarts,
	}
}

// GetState returns the current state of the process
func (p *Process) GetState() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return int(p.state)
}

// ResetRestartCounters resets the restart counters
func (p *Process) ResetRestartCounters() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.totalRestarts = 0
	p.failureRestarts = 0
	p.consecutiveFailures = 0
}

// ResetRestartCounters resets the restart counters for all processes in the group
func (pg *ProcessGroup) ResetRestartCounters() {
	pg.logger.InfoContext(context.Background(), "resetting restart counters",
		"process", pg.config.Name)

	pg.mu.RLock()
	processes := make([]*Process, len(pg.processes))
	copy(processes, pg.processes)
	pg.mu.RUnlock()

	for _, proc := range processes {
		if proc != nil {
			proc.ResetRestartCounters()
		}
	}
}
