package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/sorinpanduru/goardian/internal/config"
	"github.com/sorinpanduru/goardian/internal/logger"
	"github.com/sorinpanduru/goardian/internal/metrics"
)

// MockCmd creates a simple shell script that can be used for testing processes.
// The script can be configured to run for a specific duration and exit with a specific code.
func MockCmd(t *testing.T, duration int, exitCode int) string {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "goardian-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}

	// Create the script content
	var scriptContent string
	if exitCode != 0 {
		scriptContent = `#!/bin/sh
sleep %d
exit %d
`
		scriptContent = sprintf(scriptContent, duration, exitCode)
	} else {
		scriptContent = `#!/bin/sh
sleep %d
exit 0
`
		scriptContent = sprintf(scriptContent, duration)
	}

	// Create the script file
	scriptPath := filepath.Join(tmpDir, "test-script.sh")
	err = os.WriteFile(scriptPath, []byte(scriptContent), 0755)
	if err != nil {
		t.Fatalf("Failed to write script file: %v", err)
	}

	// Register cleanup
	t.Cleanup(func() {
		os.RemoveAll(tmpDir)
	})

	return scriptPath
}

// MockProcess creates a test Process instance
func MockProcess(t *testing.T, name string, cmd string, args []string) *Process {
	log := logger.NewLogger("console")
	metricsCollector := metrics.NewMetricsCollector()

	cfg := config.ProcessConfig{
		Name:         name,
		Command:      cmd,
		Args:         args,
		StartTimeout: 5 * 1000000000, // 5 seconds
		StopTimeout:  5 * 1000000000, // 5 seconds
		MaxRestarts:  3,
		NumProcs:     1,
	}

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

	return proc
}

// WaitForProcessExit waits for a process to exit or timeout
func WaitForProcessExit(ctx context.Context, t *testing.T, proc *Process, timeout int) bool {
	// Create a done channel
	done := make(chan struct{})

	// Start a goroutine to check if the process is running
	go func() {
		for {
			if !proc.IsRunning() {
				close(done)
				return
			}

			select {
			case <-ctx.Done():
				return
			default:
				// Continue checking
			}
		}
	}()

	// Wait for the process to exit or timeout
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// sprintf is a helper function to format strings
func sprintf(format string, a ...interface{}) string {
	return fmt.Sprintf(format, a...)
}
