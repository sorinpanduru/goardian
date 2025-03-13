package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sorinpanduru/goardian/internal/config"
	"github.com/sorinpanduru/goardian/internal/logger"
	"github.com/sorinpanduru/goardian/internal/metrics"
	"github.com/sorinpanduru/goardian/internal/process"
)

var (
	configPath = flag.String("config", "config.yaml", "Path to config file")
)

func main() {
	flag.Parse()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Initialize logger
	logger := logger.NewLogger(cfg.LogFormat)

	// Initialize metrics collector with custom registry
	registry := prometheus.NewRegistry()
	metricsCollector := metrics.NewMetricsCollector()
	registry.MustRegister(metricsCollector)
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	// Start metrics server
	metricsServer := &http.Server{
		Addr:    cfg.MetricsAddr,
		Handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{EnableOpenMetrics: true}),
	}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.ErrorContext(ctx, "metrics server error", "error", err)
		}
	}()

	// Create and start processes
	processes := make([]*process.Process, len(cfg.Processes))
	for i, procCfg := range cfg.Processes {
		proc := process.New(procCfg, metricsCollector, logger)
		processes[i] = proc
		metricsCollector.Register(procCfg.Name)

		if err := proc.Start(ctx); err != nil {
			logger.ErrorContext(ctx, "failed to start process",
				"process", procCfg.Name,
				"error", err)
			continue
		}

		// Monitor process in background
		go func(p *process.Process) {
			if err := p.Monitor(ctx); err != nil {
				logger.ErrorContext(ctx, "process monitoring error",
					"process", p.Config().Name,
					"error", err)
			}
		}(proc)
	}

	// Wait for shutdown signal
	sig := <-sigChan
	logger.InfoContext(ctx, "received shutdown signal", "signal", sig)

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownDelay)
	defer shutdownCancel()

	// Stop all processes
	logger.InfoContext(shutdownCtx, "received shutdown signal, stopping processes")
	for _, proc := range processes {
		logger.InfoContext(shutdownCtx, "stopping process", "process", proc.Config().Name)
		if err := proc.Stop(shutdownCtx); err != nil {
			logger.ErrorContext(shutdownCtx, "error stopping process",
				"process", proc.Config().Name,
				"error", err)
		}
	}

	// Shutdown metrics server
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorContext(shutdownCtx, "error shutting down metrics server", "error", err)
	}
}
