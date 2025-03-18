package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/sorinpanduru/goardian/internal/logger"
	"github.com/sorinpanduru/goardian/internal/process"
)

// Server represents the web interface server
type Server struct {
	addr           string
	processManager *process.Manager
	logger         *logger.Logger
	wsServer       *WSServer
	httpServer     *http.Server
	appCtx         context.Context // Application-level context
}

// NewServer creates a new web interface server
func NewServer(addr string, manager *process.Manager, log *logger.Logger, appCtx context.Context) *Server {
	wsServer := NewWSServer(log)

	server := &Server{
		addr:           addr,
		processManager: manager,
		logger:         log,
		wsServer:       wsServer,
		appCtx:         appCtx,
	}

	// Set up state change notifications for all existing processes
	go func() {
		// Give a moment for things to initialize
		time.Sleep(500 * time.Millisecond)

		// Set up listeners for all process groups
		for _, group := range manager.GetProcessGroups() {
			groupName := group.Config().Name

			// Set up state change listeners for each process
			for _, proc := range group.GetProcesses() {
				if proc != nil {
					// Create a closure to capture the group name
					setStateChangedFunc := func(p *process.Process) {
						// Current state change function (if any)
						existingFunc := p.StateChanged

						// Set new state change function that also broadcasts to WebSocket
						p.StateChanged = func(p *process.Process) {
							// Call existing function if it exists
							if existingFunc != nil {
								existingFunc(p)
							}

							// Broadcast state change to WebSocket clients
							wsServer.BroadcastProcessState(groupName, p)
						}
					}

					// Apply the function to this process
					setStateChangedFunc(proc)
				}
			}
		}
	}()

	return server
}

// Start starts the web server
func (s *Server) Start(ctx context.Context) error {
	// Start WebSocket server
	go s.wsServer.Start(ctx)

	// Start periodic metrics broadcaster
	go s.startPeriodicMetricsUpdate(ctx)

	// Set up HTTP server routes
	mux := http.NewServeMux()

	// Create a file server for static files
	staticHandler := http.FileServer(http.FS(staticFiles))

	// Handle root and static files
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Handle OPTIONS requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// For the root path, serve index.html
		if r.URL.Path == "/" {
			content, err := staticFiles.ReadFile("static/index.html")
			if err != nil {
				http.Error(w, "Failed to read index.html", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(content)
			return
		}

		// Serve favicon.png
		if r.URL.Path == "/favicon.ico" {
			content, err := staticFiles.ReadFile("static/favicon.png")
			if err != nil {
				http.Error(w, "Failed to read favicon", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write(content)
			return
		}

		// For .js files, set the correct content type
		if strings.HasSuffix(r.URL.Path, ".js") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}

		staticHandler.ServeHTTP(w, r)
	})

	// API routes
	mux.HandleFunc("GET /api/processes", s.handleGetProcesses)
	mux.HandleFunc("GET /api/processes/{group}", s.handleGetProcessGroup)
	mux.HandleFunc("POST /api/processes/{group}/start", s.handleStartProcess)
	mux.HandleFunc("POST /api/processes/{group}/stop", s.handleStopProcess)
	mux.HandleFunc("POST /api/processes/{group}/restart", s.handleRestartProcess)
	mux.HandleFunc("/ws", s.wsServer.ServeWS)

	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	// Start HTTP server
	go func() {
		s.logger.InfoContext(ctx, "starting web server", "addr", s.addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.ErrorContext(ctx, "web server error", "error", err)
		}
	}()

	return nil
}

// Stop stops the web server
func (s *Server) Stop(ctx context.Context) error {
	s.wsServer.Stop()
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}

// handleGetProcesses returns a list of all processes
func (s *Server) handleGetProcesses(w http.ResponseWriter, r *http.Request) {
	groups := s.processManager.GetProcessGroups()
	response := make([]map[string]interface{}, 0, len(groups))

	for _, group := range groups {
		cfg := group.Config()
		processes := group.GetProcesses()

		groupInfo := map[string]interface{}{
			"name":      cfg.Name,
			"command":   cfg.Command,
			"args":      cfg.Args,
			"numProcs":  cfg.NumProcs,
			"processes": make([]map[string]interface{}, 0, len(processes)),
		}

		runningCount := 0
		for _, proc := range processes {
			if proc != nil {
				// Get memory usage in human-readable format
				memoryBytes := proc.GetMemoryUsage()
				memoryMB := float64(memoryBytes) / 1024 / 1024

				// Get uptime in seconds and format it
				uptimeSeconds := proc.GetUptime()

				procInfo := map[string]interface{}{
					"instanceId":   proc.InstanceID(),
					"running":      proc.IsRunning(),
					"memoryBytes":  memoryBytes,
					"memoryMB":     memoryMB,
					"memoryString": FormatMemorySize(memoryBytes),
					"uptime":       uptimeSeconds,
					"uptimeString": FormatUptime(uptimeSeconds),
					"startTime":    proc.GetStartTime().Format(time.RFC3339),
					"state":        proc.GetState(),
				}

				if proc.IsRunning() {
					runningCount++
				}

				// Add backoff state if available
				if backoffState := proc.GetBackoffState(); backoffState != nil {
					procInfo["backoffState"] = backoffState
				}

				// Add restart statistics if available
				if restartStats := proc.GetRestartStats(); restartStats != nil {
					procInfo["restartStats"] = restartStats
				}

				groupInfo["processes"] = append(groupInfo["processes"].([]map[string]interface{}), procInfo)
			}
		}

		// Add running count
		groupInfo["runningCount"] = runningCount
		groupInfo["totalCount"] = len(processes)

		response = append(response, groupInfo)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleGetProcessGroup returns details about a specific process group
func (s *Server) handleGetProcessGroup(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group")
	group := s.processManager.GetProcessGroup(groupName)
	if group == nil {
		http.Error(w, "Process group not found", http.StatusNotFound)
		return
	}

	cfg := group.Config()
	processes := group.GetProcesses()

	response := map[string]interface{}{
		"name":      cfg.Name,
		"command":   cfg.Command,
		"args":      cfg.Args,
		"numProcs":  cfg.NumProcs,
		"processes": make([]map[string]interface{}, 0, len(processes)),
	}

	runningCount := 0
	for _, proc := range processes {
		if proc != nil {
			// Get memory usage in human-readable format
			memoryBytes := proc.GetMemoryUsage()
			memoryMB := float64(memoryBytes) / 1024 / 1024

			// Get uptime in seconds and format it
			uptimeSeconds := proc.GetUptime()

			procInfo := map[string]interface{}{
				"instanceId":   proc.InstanceID(),
				"running":      proc.IsRunning(),
				"memoryBytes":  memoryBytes,
				"memoryMB":     memoryMB,
				"memoryString": FormatMemorySize(memoryBytes),
				"uptime":       uptimeSeconds,
				"uptimeString": FormatUptime(uptimeSeconds),
				"startTime":    proc.GetStartTime().Format(time.RFC3339),
				"state":        proc.GetState(),
			}

			if proc.IsRunning() {
				runningCount++
			}

			// Add backoff state if available
			if backoffState := proc.GetBackoffState(); backoffState != nil {
				procInfo["backoffState"] = backoffState
			}

			// Add restart statistics if available
			if restartStats := proc.GetRestartStats(); restartStats != nil {
				procInfo["restartStats"] = restartStats
			}

			response["processes"] = append(response["processes"].([]map[string]interface{}), procInfo)
		}
	}

	// Add running count
	response["runningCount"] = runningCount
	response["totalCount"] = len(processes)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleStartProcess starts a process group
func (s *Server) handleStartProcess(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group")
	group := s.processManager.GetProcessGroup(groupName)
	if group == nil {
		http.Error(w, "Process group not found", http.StatusNotFound)
		return
	}

	// Reset restart counters on manual start
	group.ResetRestartCounters()
	s.logger.InfoContext(r.Context(), "manual start requested, reset restart counters",
		"process", groupName)

	if err := group.Start(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Start monitoring the process group after starting it
	// Use application context for monitoring so it can be canceled on application shutdown
	if err := group.Monitor(s.appCtx); err != nil {
		s.logger.WarnContext(r.Context(), "failed to monitor process group after starting",
			"process", groupName, "error", err)
		// Don't return an error to the client, just log it
	}

	// Broadcast process state updates to all WebSocket clients
	go func() {
		// Allow a short delay for processes to fully start
		time.Sleep(100 * time.Millisecond)

		for _, proc := range group.GetProcesses() {
			if proc != nil {
				s.wsServer.BroadcastProcessState(groupName, proc)
			}
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// handleStopProcess stops a process group
func (s *Server) handleStopProcess(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group")
	group := s.processManager.GetProcessGroup(groupName)
	if group == nil {
		http.Error(w, "Process group not found", http.StatusNotFound)
		return
	}

	// Reset restart counters on manual stop
	group.ResetRestartCounters()
	s.logger.InfoContext(r.Context(), "manual stop requested, reset restart counters",
		"process", groupName)

	if err := group.Stop(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Broadcast process state updates to all WebSocket clients
	go func() {
		// Allow a short delay for processes to fully stop
		time.Sleep(100 * time.Millisecond)

		for _, proc := range group.GetProcesses() {
			if proc != nil {
				s.wsServer.BroadcastProcessState(groupName, proc)
			}
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// handleRestartProcess restarts a process group
func (s *Server) handleRestartProcess(w http.ResponseWriter, r *http.Request) {
	groupName := r.PathValue("group")
	group := s.processManager.GetProcessGroup(groupName)
	if group == nil {
		http.Error(w, "Process group not found", http.StatusNotFound)
		return
	}

	// Reset restart counters on manual restart
	group.ResetRestartCounters()
	s.logger.InfoContext(r.Context(), "manual restart requested, reset restart counters",
		"process", groupName)

	if err := group.Stop(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := group.Start(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Start monitoring the process group after restarting it
	// Use application context for monitoring so it can be canceled on application shutdown
	if err := group.Monitor(s.appCtx); err != nil {
		s.logger.WarnContext(r.Context(), "failed to monitor process group after restarting",
			"process", groupName, "error", err)
		// Don't return an error to the client, just log it
	}

	// Broadcast process state updates to all WebSocket clients
	go func() {
		// Allow a short delay for processes to fully restart
		time.Sleep(100 * time.Millisecond)

		for _, proc := range group.GetProcesses() {
			if proc != nil {
				s.wsServer.BroadcastProcessState(groupName, proc)
			}
		}
	}()

	w.WriteHeader(http.StatusOK)
}

// startPeriodicMetricsUpdate broadcasts process metrics updates periodically
func (s *Server) startPeriodicMetricsUpdate(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// For each process group
			for _, group := range s.processManager.GetProcessGroups() {
				groupName := group.Config().Name

				// For each process in the group
				for _, proc := range group.GetProcesses() {
					if proc != nil {
						// Broadcast current state
						s.wsServer.BroadcastProcessState(groupName, proc)
					}
				}
			}
		}
	}
}
