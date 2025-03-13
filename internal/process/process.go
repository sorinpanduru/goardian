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

type Process struct {
	config  config.ProcessConfig
	metrics *metrics.MetricsCollector
	logger  *logger.Logger
	mu      sync.RWMutex
	cmd     []*exec.Cmd
	done    chan struct{}
}

func New(cfg config.ProcessConfig, metrics *metrics.MetricsCollector, log *logger.Logger) *Process {
	return &Process{
		config:  cfg,
		metrics: metrics,
		logger:  log,
		done:    make(chan struct{}),
	}
}

func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Clear any existing instances
	p.cmd = make([]*exec.Cmd, p.config.NumProcs)

	// Start new instances
	for i := 0; i < p.config.NumProcs; i++ {
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

		p.cmd[i] = cmd

		// Handle output with structured logging
		go func(cmd *exec.Cmd, stdout, stderr io.ReadCloser, instance int) {
			// Create a custom writer for stdout
			stdoutWriter := &logWriter{
				logger:   p.logger,
				process:  p.config.Name,
				stream:   "stdout",
				instance: instance,
			}

			// Create a custom writer for stderr
			stderrWriter := &logWriter{
				logger:   p.logger,
				process:  p.config.Name,
				stream:   "stderr",
				instance: instance,
			}

			io.Copy(stdoutWriter, stdout)
			io.Copy(stderrWriter, stderr)
		}(cmd, stdout, stderr, i)
	}

	// Wait for processes to start successfully
	ready := make(chan struct{})
	go func() {
		// Give processes a moment to start
		time.Sleep(100 * time.Millisecond)

		// Check if all processes are still running
		for _, cmd := range p.cmd {
			if cmd.Process == nil {
				close(ready)
				return
			}

			// Check if process is still running
			if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
				// Process has exited
				if exitErr, ok := err.(*exec.ExitError); ok {
					if !p.isNormalExit(exitErr.ExitCode()) {
						p.metrics.RecordStart(p.config.Name)
						p.logger.ErrorContext(ctx, "process exited with error",
							"process", p.config.Name,
							"error", err)
					}
				}
				close(ready)
				return
			}
		}

		// All processes are running
		close(ready)
	}()

	select {
	case <-ready:
		p.metrics.RecordStart(p.config.Name)
		p.logger.InfoContext(ctx, "process started",
			"process", p.config.Name,
			"instances", p.config.NumProcs)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.config.StartTimeout):
		return fmt.Errorf("timeout waiting for process to start")
	}
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

func (p *Process) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// First try graceful shutdown with SIGTERM
	for _, cmd := range p.cmd {
		if cmd != nil && cmd.Process != nil {
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				p.logger.WarnContext(ctx, "failed to send SIGTERM",
					"process", p.config.Name,
					"pid", cmd.Process.Pid,
					"error", err)
			}
		}
	}

	// Wait for processes to stop gracefully
	var wg sync.WaitGroup
	wg.Add(len(p.cmd))

	// Start waiting for all processes in parallel
	for i, cmd := range p.cmd {
		if cmd != nil {
			go func(cmd *exec.Cmd, instance int) {
				defer wg.Done()
				if err := cmd.Wait(); err != nil {
					return
				}
			}(cmd, i)
		} else {
			wg.Done()
		}
	}

	// Create a channel to signal when all processes are done
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	// Wait for either all processes to stop or timeout
	select {
	case <-done:
		p.metrics.RecordStop(p.config.Name)
		p.logger.InfoContext(ctx, "all processes stopped gracefully",
			"process", p.config.Name)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(p.config.StopTimeout):
		p.logger.WarnContext(ctx, "graceful shutdown failed, sending SIGKILL",
			"process", p.config.Name)

		// Send SIGKILL to all remaining processes
		for _, cmd := range p.cmd {
			if cmd != nil && cmd.Process != nil {
				if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
					p.logger.WarnContext(ctx, "failed to send SIGKILL",
						"process", p.config.Name,
						"pid", cmd.Process.Pid,
						"error", err)
				}
			}
		}

		// Wait for all processes to finish after SIGKILL
		killDone := make(chan struct{})
		go func() {
			wg.Wait()
			close(killDone)
		}()

		select {
		case <-killDone:
			p.metrics.RecordStop(p.config.Name)
			p.logger.InfoContext(ctx, "all processes stopped after SIGKILL",
				"process", p.config.Name)
			return nil
		case <-time.After(5 * time.Second):
			return fmt.Errorf("processes still running after SIGKILL")
		}
	}
}

func (p *Process) Monitor(ctx context.Context) error {
	for {
		p.mu.RLock()
		// Check if any processes are running
		anyRunning := false
		for _, cmd := range p.cmd {
			if cmd != nil && cmd.Process != nil {
				anyRunning = true
				break
			}
		}
		p.mu.RUnlock()

		if !anyRunning {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-p.done:
				return nil
			case <-time.After(time.Second):
				continue
			}
		}

		// Create channels to receive process exits for all instances
		exitChans := make([]chan error, len(p.cmd))
		for i := range exitChans {
			exitChans[i] = make(chan error, 1)
		}

		// Start monitoring all processes
		for i, cmd := range p.cmd {
			if cmd == nil || cmd.Process == nil {
				continue
			}

			go func(cmd *exec.Cmd, exitChan chan<- error, instance int) {
				_, err := cmd.Process.Wait()
				exitChan <- err
			}(cmd, exitChans[i], i)
		}

		// Wait for any process to exit or context cancellation
		select {
		case <-ctx.Done():
			p.Stop(ctx)
			return ctx.Err()
		case <-p.done:
			return nil
		case err := <-mergeErrorChannels(exitChans):
			// Process has exited
			p.mu.Lock()
			p.cmd = nil
			p.mu.Unlock()

			// Record the stop in metrics
			p.metrics.RecordStop(p.config.Name)

			// Check if this was a normal exit
			if err == nil {
				// Process exited successfully (code 0)
				if p.isNormalExit(0) {
					// Normal exit - restart immediately without backoff
					p.logger.InfoContext(ctx, "process exited normally, restarting",
						"process", p.config.Name,
						"exit_code", 0)
					if err := p.Start(ctx); err != nil {
						return fmt.Errorf("failed to restart process after normal exit: %w", err)
					}
					continue
				}

				// Unexpected exit - return error to trigger backoff
				return fmt.Errorf("process stopped unexpectedly with exit code 0")
			}

			// Check if this was an error exit
			if exitErr, ok := err.(*exec.ExitError); ok {
				// Process has exited with non-zero code
				if p.isNormalExit(exitErr.ExitCode()) {
					// Normal exit - restart immediately without backoff
					p.logger.InfoContext(ctx, "process exited normally, restarting",
						"process", p.config.Name,
						"exit_code", exitErr.ExitCode())
					if err := p.Start(ctx); err != nil {
						return fmt.Errorf("failed to restart process after normal exit: %w", err)
					}
					continue
				}

				// Unexpected exit - return error to trigger backoff
				return fmt.Errorf("process stopped unexpectedly with exit code %d", exitErr.ExitCode())
			}

			// Some other error occurred
			return fmt.Errorf("error checking process state: %w", err)
		}
	}
}

// mergeErrorChannels merges multiple error channels into a single channel
func mergeErrorChannels(channels []chan error) chan error {
	merged := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(len(channels))

	for _, ch := range channels {
		go func(ch chan error) {
			defer wg.Done()
			if err := <-ch; err != nil {
				select {
				case merged <- err:
				default:
				}
			}
		}(ch)
	}

	go func() {
		wg.Wait()
		close(merged)
	}()

	return merged
}

func (p *Process) isNormalExit(code int) bool {
	for _, normalCode := range p.config.ExitCodes {
		if code == normalCode {
			return true
		}
	}
	return false
}

func (p *Process) SetMetricsCollector(m *metrics.MetricsCollector) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.metrics = m
}

func (p *Process) Config() config.ProcessConfig {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.config
}
