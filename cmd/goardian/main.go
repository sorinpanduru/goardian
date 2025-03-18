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
	"github.com/sorinpanduru/goardian/internal/web"
)

var (
	configPath = flag.String("config", "config.yaml", "Path to config file")
	webAddr    = flag.String("web.addr", ":8080", "Web interface address")
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

	// Create process manager
	processManager := process.NewManager(metricsCollector, logger)

	// Add process groups from config
	for _, procCfg := range cfg.Processes {
		if err := processManager.AddProcessGroup(procCfg); err != nil {
			logger.ErrorContext(ctx, "failed to add process group",
				"process", procCfg.Name,
				"error", err)
		}
	}

	// Start all processes
	logger.InfoContext(ctx, "starting processes")
	if err := processManager.StartAll(ctx); err != nil {
		logger.ErrorContext(ctx, "failed to start processes", "error", err)
	}

	// Start monitoring all processes
	if err := processManager.MonitorAll(ctx); err != nil {
		logger.ErrorContext(ctx, "failed to monitor processes", "error", err)
	}

	// Create and start web server
	webServer := web.NewServer(*webAddr, processManager, logger, ctx)
	if err := webServer.Start(ctx); err != nil {
		logger.ErrorContext(ctx, "failed to start web server", "error", err)
	}

	// Wait for shutdown signal
	sig := <-sigChan
	logger.InfoContext(ctx, "received shutdown signal", "signal", sig)

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownDelay)
	defer shutdownCancel()

	// Stop all processes
	logger.InfoContext(shutdownCtx, "stopping processes")
	if err := processManager.StopAll(shutdownCtx); err != nil {
		logger.ErrorContext(shutdownCtx, "error stopping processes", "error", err)
	}

	// Stop web server
	if err := webServer.Stop(shutdownCtx); err != nil {
		logger.ErrorContext(shutdownCtx, "error stopping web server", "error", err)
	}

	// Shutdown metrics server
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.ErrorContext(shutdownCtx, "error shutting down metrics server", "error", err)
	}
}
