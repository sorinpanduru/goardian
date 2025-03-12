package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	"github.com/pkg/errors"
)

type processWriter struct {
	logger  *slog.Logger
	ctx     context.Context
	process string
	stream  string
}

func newProcessWriter(logger *slog.Logger, ctx context.Context, process, stream string) *processWriter {
	return &processWriter{
		logger:  logger,
		ctx:     ctx,
		process: process,
		stream:  stream,
	}
}

func (w *processWriter) Write(p []byte) (n int, err error) {
	// Trim trailing newline if present
	msg := string(p)
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		msg = msg[:len(msg)-1]
	}

	w.logger.InfoContext(w.ctx, msg,
		"process", w.process,
		"stream", w.stream,
		"type", "subprocess_output")

	return len(p), nil
}

type Process struct {
	config    ProcessConfig
	cmd       *exec.Cmd
	restarts  int
	mu        sync.Mutex
	stopChan  chan struct{}
	lastDelay time.Duration
	ctx       context.Context
	logger    *slog.Logger
}

func NewProcess(ctx context.Context, config ProcessConfig, logger *slog.Logger) *Process {
	return &Process{
		config:    config,
		stopChan:  make(chan struct{}),
		lastDelay: config.RestartDelay,
		ctx:       ctx,
		logger:    logger,
	}
}

func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd != nil && p.cmd.Process != nil && p.cmd.ProcessState.Exited() {
		// Process has exited, reset the command
		p.cmd = nil
	}

	if p.cmd != nil {
		return fmt.Errorf("process already running")
	}

	p.cmd = exec.CommandContext(p.ctx, p.config.Command, p.config.Args...)
	p.cmd.Dir = p.config.WorkingDir
	p.cmd.Env = p.config.Environment

	// Set up stdout and stderr pipes
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderr, err := p.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := p.cmd.Start(); err != nil {
		p.cmd = nil
		return fmt.Errorf("failed to start process: %w", err)
	}

	// Start goroutines to handle output with our custom writer
	go func() {
		io.Copy(newProcessWriter(p.logger, p.ctx, p.config.Name, "stdout"), stdout)
	}()

	go func() {
		io.Copy(newProcessWriter(p.logger, p.ctx, p.config.Name, "stderr"), stderr)
	}()

	// Wait for process to be ready
	ready := make(chan error)
	go func() {
		ready <- p.cmd.Wait()
	}()

	select {
	case err := <-ready:
		p.cmd = nil
		if err != nil {
			return fmt.Errorf("error waiting for process to start: %w", err)
		}
		return errors.New("process exited immediately")
	case <-p.ctx.Done():
		p.cmd = nil
		return p.ctx.Err()
	case <-time.After(p.config.StartTimeout):
		// Process is running
		return nil
	}
}

func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil {
		return nil
	}

	if err := p.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to kill process: %w", err)
	}

	p.cmd = nil
	return nil
}

func (p *Process) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil {
		return false
	}

	return p.cmd.Process != nil && p.cmd.ProcessState == nil
}

func (p *Process) calculateNextDelay() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.config.BackoffEnabled {
		return p.config.RestartDelay
	}

	// Calculate exponential backoff
	nextDelay := time.Duration(float64(p.lastDelay) * p.config.BackoffFactor)

	// Cap at max delay if specified
	if p.config.BackoffMaxDelay > 0 && nextDelay > p.config.BackoffMaxDelay {
		nextDelay = p.config.BackoffMaxDelay
	}

	p.lastDelay = nextDelay
	return nextDelay
}

func (p *Process) Monitor() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopChan:
			return
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			if !p.IsRunning() {
				p.mu.Lock()
				if p.restarts >= p.config.MaxRestarts {
					p.mu.Unlock()
					p.logger.InfoContext(p.ctx, "process reached maximum restart limit",
						"process", p.config.Name,
						"max_restarts", p.config.MaxRestarts)
					return
				}
				p.restarts++
				p.mu.Unlock()

				if err := p.Start(); err != nil {
					p.logger.ErrorContext(p.ctx, "failed to start process",
						"process", p.config.Name,
						"error", err)

					// Calculate and apply the next delay
					delay := p.calculateNextDelay()
					p.logger.InfoContext(p.ctx, "waiting before next restart attempt",
						"process", p.config.Name,
						"delay", delay)

					select {
					case <-p.stopChan:
						return
					case <-p.ctx.Done():
						return
					case <-time.After(delay):
						// Continue with next restart attempt
					}
				} else {
					p.logger.InfoContext(p.ctx, "successfully restarted process",
						"process", p.config.Name)
					// Reset delay on successful start
					p.mu.Lock()
					p.lastDelay = p.config.RestartDelay
					p.mu.Unlock()
				}
			}
		}
	}
}

func (p *Process) StopMonitoring() {
	close(p.stopChan)
}
