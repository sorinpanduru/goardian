package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Create a context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start signal handler in a goroutine
	go func() {
		<-sigChan
		logger.InfoContext(ctx, "received shutdown signal, initiating graceful shutdown")
		cancel()
	}()

	config, err := LoadConfig(*configPath)
	if err != nil {
		logger.ErrorContext(ctx, "failed to load config", "error", err)
		os.Exit(1)
	}

	processes := make([]*Process, len(config.Processes))
	for i, procConfig := range config.Processes {
		processes[i] = NewProcess(ctx, procConfig, logger)
	}

	// Start all processes
	for _, proc := range processes {
		if err := proc.Start(); err != nil {
			logger.ErrorContext(ctx, "failed to start process",
				"process", proc.config.Name,
				"error", err)
		} else {
			logger.InfoContext(ctx, "started process",
				"process", proc.config.Name)
		}
	}

	// Start monitoring for all processes
	for _, proc := range processes {
		go proc.Monitor()
	}

	// Wait for context cancellation
	<-ctx.Done()

	// Stop all processes
	logger.InfoContext(ctx, "stopping all processes")
	for _, proc := range processes {
		proc.StopMonitoring()
		if err := proc.Stop(); err != nil {
			logger.ErrorContext(ctx, "error stopping process",
				"process", proc.config.Name,
				"error", err)
		}
	}

	logger.InfoContext(ctx, "shutdown complete")
}
