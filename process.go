package main

import (
	"fmt"
	"os/exec"
	"sync"
	"time"
)

type Process struct {
	config    ProcessConfig
	cmd       *exec.Cmd
	restarts  int
	mu        sync.Mutex
	stopChan  chan struct{}
	lastDelay time.Duration
}

func NewProcess(config ProcessConfig) *Process {
	return &Process{
		config:    config,
		stopChan:  make(chan struct{}),
		lastDelay: config.RestartDelay,
	}
}

func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd != nil {
		return fmt.Errorf("process already running")
	}

	p.cmd = exec.Command(p.config.Command, p.config.Args...)
	p.cmd.Dir = p.config.WorkingDir
	p.cmd.Env = p.config.Environment

	if err := p.cmd.Start(); err != nil {
		p.cmd = nil
		return fmt.Errorf("failed to start process: %w", err)
	}

	// Wait for process to be ready
	ready := make(chan error)
	go func() {
		ready <- p.cmd.Wait()
	}()

	select {
	case err := <-ready:
		return fmt.Errorf("process exited immediately: %w", err)
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
		case <-ticker.C:
			if !p.IsRunning() {
				p.mu.Lock()
				if p.restarts >= p.config.MaxRestarts {
					p.mu.Unlock()
					fmt.Printf("Process %s has reached maximum restart limit (%d)\n",
						p.config.Name, p.config.MaxRestarts)
					return
				}
				p.restarts++
				p.mu.Unlock()

				if err := p.Start(); err != nil {
					fmt.Printf("Failed to start process %s: %v\n", p.config.Name, err)

					// Calculate and apply the next delay
					delay := p.calculateNextDelay()
					fmt.Printf("Waiting %v before next restart attempt for %s\n",
						delay, p.config.Name)

					select {
					case <-p.stopChan:
						return
					case <-time.After(delay):
						// Continue with next restart attempt
					}
				} else {
					fmt.Printf("Successfully restarted process %s\n", p.config.Name)
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
