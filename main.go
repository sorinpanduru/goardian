package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	config, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	processes := make([]*Process, len(config.Processes))
	for i, procConfig := range config.Processes {
		processes[i] = NewProcess(procConfig)
	}

	// Start all processes
	for _, proc := range processes {
		if err := proc.Start(); err != nil {
			log.Printf("Failed to start process %s: %v", proc.config.Name, err)
		} else {
			log.Printf("Started process %s", proc.config.Name)
		}
	}

	// Start monitoring for all processes
	for _, proc := range processes {
		go proc.Monitor()
	}

	// Handle shutdown gracefully
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Println("Shutting down...")

	// Stop all processes
	for _, proc := range processes {
		proc.StopMonitoring()
		if err := proc.Stop(); err != nil {
			log.Printf("Error stopping process %s: %v", proc.config.Name, err)
		}
	}

	fmt.Println("Shutdown complete")
}
