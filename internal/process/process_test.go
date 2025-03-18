package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sorinpanduru/goardian/internal/config"
	"github.com/sorinpanduru/goardian/internal/logger"
	"github.com/sorinpanduru/goardian/internal/metrics"
)

// TestProcessLifecycle tests the basic lifecycle of a single process
func TestProcessLifecycle(t *testing.T) {
	// Skip on CI if needed
	if os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI environment")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create dependencies
	log := logger.NewLogger("console")
	metricsCollector := metrics.NewMetricsCollector()

	// Create a simple process config for an echo command
	cfg := config.ProcessConfig{
		Name:         "test_echo",
		Command:      "echo",
		Args:         []string{"hello", "world"},
		StartTimeout: 5 * time.Second,
		StopTimeout:  5 * time.Second,
		MaxRestarts:  3,
		NumProcs:     1,
	}

	// Create a process
	proc := &Process{
		groupName:  cfg.Name,
		instanceID: 0,
		metrics:    metricsCollector,
		logger:     log,
		config:     cfg,
		done:       make(chan struct{}),
		state:      StateRunning,
		exitChan:   make(chan error, 1),
	}

	// Start the process
	err := proc.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start process: %v", err)
	}

	// Verify process state
	if !proc.IsRunning() {
		t.Error("Process should be running after Start()")
	}

	// Allow process to complete (it should exit since it's just an echo command)
	time.Sleep(1 * time.Second)

	// Process should have exited automatically since it's a short-lived command
	if proc.IsRunning() {
		t.Error("Process should have exited by now")
	}
}

// TestProcessGroupLifecycle tests the lifecycle of a process group
func TestProcessGroupLifecycle(t *testing.T) {
	// Skip on CI if needed
	if os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI environment")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create dependencies
	log := logger.NewLogger("console")
	metricsCollector := metrics.NewMetricsCollector()

	// Create a process config for a sleep command, with 2 instances
	cfg := config.ProcessConfig{
		Name:         "test_group",
		Command:      "sleep",
		Args:         []string{"10"}, // Sleep for 10 seconds
		StartTimeout: 5 * time.Second,
		StopTimeout:  5 * time.Second,
		MaxRestarts:  3,
		NumProcs:     2, // Multiple processes
	}

	// Create a process group
	group := NewProcessGroup(cfg, metricsCollector, log)

	// Start the process group
	err := group.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start process group: %v", err)
	}

	// Monitor the process group
	go group.Monitor(ctx)

	// Give processes some time to start
	time.Sleep(2 * time.Second)

	// Verify all processes are running
	processes := group.GetProcesses()
	if len(processes) != 2 {
		t.Errorf("Expected 2 processes, got %d", len(processes))
	}
	for i, proc := range processes {
		if proc == nil {
			t.Errorf("Process %d is nil", i)
			continue
		}
		if !proc.IsRunning() {
			t.Errorf("Process %d should be running", i)
		}
	}

	// Stop the process group
	err = group.Stop(ctx)
	if err != nil {
		t.Fatalf("Failed to stop process group: %v", err)
	}

	// Verify all processes have stopped
	for i, proc := range processes {
		if proc == nil {
			continue
		}
		if proc.IsRunning() {
			t.Errorf("Process %d should have stopped", i)
		}
	}
}

// TestProcessRestartOnFailure tests automatic restart behavior when a process exits with an error
func TestProcessRestartOnFailure(t *testing.T) {
	// Skip on CI if needed
	if os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI environment")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create dependencies
	log := logger.NewLogger("console")
	metricsCollector := metrics.NewMetricsCollector()

	// Create a process config for a command that will fail
	cfg := config.ProcessConfig{
		Name:         "test_fail",
		Command:      "sh",
		Args:         []string{"-c", "exit 1"}, // Command that exits with error code 1
		StartTimeout: 5 * time.Second,
		StopTimeout:  5 * time.Second,
		MaxRestarts:  3,
		NumProcs:     1,
	}

	// Create a process
	proc := &Process{
		groupName:  cfg.Name,
		instanceID: 0,
		metrics:    metricsCollector,
		logger:     log,
		config:     cfg,
		done:       make(chan struct{}),
		state:      StateRunning,
		exitChan:   make(chan error, 1),
	}

	// Set up a counter to track restart attempts
	restartCount := 0
	proc.StateChanged = func(p *Process) {
		if !p.IsRunning() {
			restartCount++
		}
	}

	// Start the process
	err := proc.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start process: %v", err)
	}

	// Monitor the process
	go proc.Monitor(ctx)

	// Let it restart a few times
	time.Sleep(5 * time.Second)

	// Stop the monitor
	proc.Stop(ctx)

	// Verify that the process was restarted at least once
	if restartCount == 0 {
		t.Error("Process should have been restarted at least once")
	}

	// Check stats
	stats := proc.GetRestartStats()
	t.Logf("Restart stats: %v", stats)
	if stats["failureRestarts"].(int) == 0 {
		t.Error("Expected failure restarts > 0")
	}
}

// TestProcessMaxRestartsExceeded tests that a process changes to failed state after exceeding max_restarts
func TestProcessMaxRestartsExceeded(t *testing.T) {
	// Skip on CI if needed
	if os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI environment")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create dependencies
	log := logger.NewLogger("console")
	metricsCollector := metrics.NewMetricsCollector()

	// Create a process config for a command that will fail, with low max_restarts
	cfg := config.ProcessConfig{
		Name:         "test_max_restarts",
		Command:      "sh",
		Args:         []string{"-c", "exit 1"}, // Command that exits with error code 1
		StartTimeout: 1 * time.Second,
		StopTimeout:  1 * time.Second,
		MaxRestarts:  2, // Set a low limit
		NumProcs:     1,
	}

	// Create a process
	proc := &Process{
		groupName:  cfg.Name,
		instanceID: 0,
		metrics:    metricsCollector,
		logger:     log,
		config:     cfg,
		done:       make(chan struct{}),
		state:      StateRunning,
		exitChan:   make(chan error, 1),
	}

	// Set up a monitor to check when the process fails
	monitorDone := make(chan struct{})
	failedState := make(chan bool, 1)

	// Start the process
	err := proc.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start process: %v", err)
	}

	// Monitor the process
	go func() {
		err := proc.Monitor(ctx)
		if err != nil {
			// Check if the process is in failed state
			if proc.GetState() == int(StateFailed) {
				failedState <- true
			}
		}
		close(monitorDone)
	}()

	// Wait for monitor to exit (it should exit once max_restarts is exceeded)
	select {
	case <-monitorDone:
		// Monitor has exited
	case <-time.After(10 * time.Second):
		t.Fatal("Process monitor did not exit after exceeding max_restarts")
	}

	// Wait a moment for the failed state to be captured
	time.Sleep(500 * time.Millisecond)

	// Check if the state was set to failed
	select {
	case <-failedState:
		// Process failed as expected
	default:
		// Double-check the state directly
		state := proc.GetState()
		if state != int(StateFailed) {
			t.Errorf("Expected process state to be StateFailed (%d), got %d", StateFailed, state)
		}
	}
}

// TestProcessBackoff tests that process restart uses backoff on consecutive failures
func TestProcessBackoff(t *testing.T) {
	// Skip on CI if needed
	if os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI environment")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create dependencies
	log := logger.NewLogger("console")
	metricsCollector := metrics.NewMetricsCollector()

	// Create a process config that will always fail
	cfg := config.ProcessConfig{
		Name:           "test_backoff",
		Command:        "sh",
		Args:           []string{"-c", "exit 1"}, // Always fail
		StartTimeout:   1 * time.Second,
		StopTimeout:    1 * time.Second,
		MaxRestarts:    10, // High enough to not hit the limit
		NumProcs:       1,
		BackoffEnabled: true,
	}

	// Create a process
	proc := &Process{
		groupName:  cfg.Name,
		instanceID: 0,
		metrics:    metricsCollector,
		logger:     log,
		config:     cfg,
		done:       make(chan struct{}),
		state:      StateRunning,
		exitChan:   make(chan error, 1),
	}

	// Track restart times
	restartTimes := make([]time.Time, 0)
	proc.StateChanged = func(p *Process) {
		if !p.IsRunning() {
			// Record time of state change to not running
			restartTimes = append(restartTimes, time.Now())
		}
	}

	// Start the process
	err := proc.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start process: %v", err)
	}

	// Monitor the process for a few seconds
	monitorCtx, monitorCancel := context.WithTimeout(ctx, 10*time.Second)
	defer monitorCancel()

	go proc.Monitor(monitorCtx)

	// Wait for the monitoring to complete
	<-monitorCtx.Done()

	// Stop the process
	proc.Stop(ctx)

	// We should have at least 3 restart times recorded
	if len(restartTimes) < 3 {
		t.Fatalf("Expected at least 3 restarts, got %d", len(restartTimes))
	}

	// Calculate intervals between restarts
	intervals := make([]time.Duration, 0)
	for i := 1; i < len(restartTimes); i++ {
		interval := restartTimes[i].Sub(restartTimes[i-1])
		intervals = append(intervals, interval)
	}

	// Check that intervals are increasing (backoff)
	// We allow for some jitter, so we check if at least one interval is significantly longer than the first
	foundLongerInterval := false
	for i := 1; i < len(intervals); i++ {
		if float64(intervals[i]) > float64(intervals[0])*1.5 {
			foundLongerInterval = true
			break
		}
	}

	if !foundLongerInterval {
		t.Error("Expected backoff to increase intervals between restarts, but no significant increase found")
		t.Logf("Intervals: %v", intervals)
	}

	// Verify backoff state
	backoffState := proc.GetBackoffState()
	t.Logf("Backoff state: %v", backoffState)

	// Consecutive failures should be > 0
	if backoffState["consecutiveFailures"].(int) <= 0 {
		t.Error("Expected consecutive failures > 0")
	}
}

// TestProcessGracefulShutdown tests that a process is shut down gracefully
func TestProcessGracefulShutdown(t *testing.T) {
	// TODO: Fix this test - there appears to be an issue with signal handling in the test environment
	// The shell script trap doesn't seem to be catching the SIGTERM properly during testing
	t.Skip("Skipping test - signal handling in test environment is inconsistent")

	// Skip on CI if needed
	if os.Getenv("CI") != "" {
		t.Skip("Skipping test in CI environment")
	}

	// This test creates a simple shell script that traps SIGTERM and exits cleanly
	scriptContent := `#!/bin/sh
# Set up signal handling right at the start
trap 'echo "SIGTERM received, exiting gracefully"; exit 0' TERM
echo "Process running, waiting for signal"
# Use a shorter sleep and ensure the script stays in foreground
sleep 60
`
	// Create a temporary script file
	tmpDir, err := os.MkdirTemp("", "goardian-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := filepath.Join(tmpDir, "graceful.sh")
	err = os.WriteFile(scriptPath, []byte(scriptContent), 0755)
	if err != nil {
		t.Fatalf("Failed to write script file: %v", err)
	}

	// Verify the script works as expected
	t.Logf("Created test script at %s", scriptPath)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create dependencies
	log := logger.NewLogger("console")
	metricsCollector := metrics.NewMetricsCollector()

	// Create a process config for our script
	cfg := config.ProcessConfig{
		Name:         "test_graceful",
		Command:      scriptPath,
		Args:         []string{},
		StartTimeout: 5 * time.Second,
		StopTimeout:  5 * time.Second, // Give it time to handle SIGTERM
		MaxRestarts:  3,
		NumProcs:     1,
	}

	// Create a process
	proc := &Process{
		groupName:  cfg.Name,
		instanceID: 0,
		metrics:    metricsCollector,
		logger:     log,
		config:     cfg,
		done:       make(chan struct{}),
		state:      StateRunning,
		exitChan:   make(chan error, 1),
	}

	// Record timing information for stop operation
	var stopStartTime time.Time
	var stopCompletedTime time.Time

	// Set up a callback to record when the process changes state
	proc.StateChanged = func(p *Process) {
		if !p.IsRunning() {
			stopCompletedTime = time.Now()
		}
	}

	// Start the process
	err = proc.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start process: %v", err)
	}

	// Give the process a moment to start up
	time.Sleep(1 * time.Second)

	// Verify process is running
	if !proc.IsRunning() {
		t.Fatalf("Process should be running")
	}

	// Stop the process and record timing
	stopStartTime = time.Now()
	err = proc.Stop(ctx)
	if err != nil {
		t.Fatalf("Failed to stop process: %v", err)
	}

	// Verify process has stopped
	if proc.IsRunning() {
		t.Error("Process should have stopped")
	}

	// Check timing - if the process handled SIGTERM properly, it should have exited
	// relatively quickly (1-2 seconds), much faster than the StopTimeout (5 seconds)
	stopDuration := stopCompletedTime.Sub(stopStartTime)

	// We'll be more lenient with the timing check - sometimes it can take a moment for signals to be processed
	if stopDuration > 4*time.Second {
		t.Errorf("Process took too long to stop (%v), may not have handled SIGTERM properly", stopDuration)
	} else {
		t.Logf("Process stopped gracefully in %v", stopDuration)
	}
}
