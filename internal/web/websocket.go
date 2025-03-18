package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/sorinpanduru/goardian/internal/logger"
	"github.com/sorinpanduru/goardian/internal/process"
)

// Event represents a WebSocket event
type Event struct {
	Type    string      `json:"type"`    // "process_state", "process_output", "metrics"
	Group   string      `json:"group"`   // Process group name
	Process int         `json:"process"` // Process instance ID
	Data    interface{} `json:"data"`    // Event-specific data
}

// Client represents a connected WebSocket client
type Client struct {
	conn      *websocket.Conn
	send      chan Event
	server    *WSServer
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

// WSServer handles WebSocket connections and broadcasts process updates
type WSServer struct {
	clients    map[*Client]bool
	broadcast  chan Event
	register   chan *Client
	unregister chan *Client
	logger     *logger.Logger
	mu         sync.RWMutex
	done       chan struct{}
}

// NewWSServer creates a new WebSocket server
func NewWSServer(log *logger.Logger) *WSServer {
	return &WSServer{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan Event),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		logger:     log,
		done:       make(chan struct{}),
	}
}

// Start starts the WebSocket server
func (s *WSServer) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			s.Stop()
			return
		case client := <-s.register:
			s.mu.Lock()
			s.clients[client] = true
			s.mu.Unlock()
			s.logger.InfoContext(ctx, "new websocket client connected")

		case client := <-s.unregister:
			s.mu.Lock()
			if _, ok := s.clients[client]; ok {
				delete(s.clients, client)
				client.Close()
			}
			s.mu.Unlock()
			s.logger.InfoContext(ctx, "websocket client disconnected")

		case event := <-s.broadcast:
			s.mu.RLock()
			for client := range s.clients {
				select {
				case client.send <- event:
				default:
					// Client send buffer is full, close the connection
					client.Close()
					delete(s.clients, client)
				}
			}
			s.mu.RUnlock()
		}
	}
}

// Stop stops the WebSocket server
func (s *WSServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Close all client connections
	for client := range s.clients {
		client.Close()
		delete(s.clients, client)
	}

	close(s.done)
}

// Broadcast sends an event to all connected clients
func (s *WSServer) Broadcast(event Event) {
	select {
	case s.broadcast <- event:
	case <-s.done:
		return
	}
}

// BroadcastProcessState sends a process state update to all clients
func (s *WSServer) BroadcastProcessState(group string, proc *process.Process) {
	// Get memory usage in human-readable format
	memoryBytes := proc.GetMemoryUsage()
	memoryMB := float64(memoryBytes) / 1024 / 1024

	// Get uptime in seconds and format it
	uptimeSeconds := proc.GetUptime()

	state := map[string]interface{}{
		"running":      proc.IsRunning(),
		"memoryBytes":  memoryBytes,
		"memoryMB":     memoryMB,
		"uptime":       uptimeSeconds,
		"uptimeString": FormatUptime(uptimeSeconds),
		"memoryString": FormatMemorySize(memoryBytes),
		"startTime":    proc.GetStartTime().Format(time.RFC3339),
	}

	// Add backoff state if available
	if backoffState := proc.GetBackoffState(); backoffState != nil {
		state["backoffState"] = backoffState
	}

	// Add restart statistics
	if restartStats := proc.GetRestartStats(); restartStats != nil {
		state["restartStats"] = restartStats
	}

	s.Broadcast(Event{
		Type:    "process_state",
		Group:   group,
		Process: proc.InstanceID(),
		Data:    state,
	})
}

// BroadcastProcessOutput sends process output to all clients
func (s *WSServer) BroadcastProcessOutput(group string, instanceID int, stream string, output string) {
	s.Broadcast(Event{
		Type:    "process_output",
		Group:   group,
		Process: instanceID,
		Data: map[string]string{
			"stream": stream,
			"output": output,
		},
	})
}

// ServeWS handles WebSocket connections
func (s *WSServer) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	upgrader := websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // Allow all origins in development
		},
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "websocket upgrade failed", "error", err)
		return
	}

	client := &Client{
		conn:   conn,
		send:   make(chan Event, 256),
		server: s,
	}

	s.register <- client

	// Start client read/write pumps
	go client.writePump()
	go client.readPump()
}

// writePump pumps messages from the hub to the websocket connection
func (c *Client) writePump() {
	// Send ping every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case event, ok := <-c.send:
			if !ok {
				// Server closed the channel
				return
			}

			c.mu.Lock()
			err := c.conn.WriteJSON(event)
			c.mu.Unlock()
			if err != nil {
				return
			}

		case <-ticker.C:
			// Send ping message with current timestamp
			c.mu.Lock()
			err := c.conn.WriteMessage(websocket.PingMessage, []byte(time.Now().Format(time.RFC3339)))
			c.mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

// readPump pumps messages from the websocket connection to the hub
func (c *Client) readPump() {
	defer func() {
		c.server.unregister <- c
	}()

	// Set a shorter read deadline (30 seconds) for faster detection of failed connections
	c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		// Reset the read deadline when we receive a pong
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		return nil
	})

	for {
		messageType, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.server.logger.ErrorContext(context.Background(), "websocket read error", "error", err)
			}
			break
		}

		// Reset the read deadline on any received message
		c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		// Handle client messages (ping messages to keep connection alive)
		if messageType == websocket.TextMessage {
			// Try to parse as JSON (for ping messages)
			var data map[string]interface{}
			if err := json.Unmarshal(message, &data); err == nil {
				// Check if it's a ping message
				if msgType, ok := data["type"].(string); ok && msgType == "ping" {
					// Respond with a pong message
					c.mu.Lock()
					err := c.conn.WriteJSON(map[string]string{"type": "pong", "time": time.Now().Format(time.RFC3339)})
					c.mu.Unlock()
					if err != nil {
						c.server.logger.ErrorContext(context.Background(), "error sending pong response", "error", err)
					}
				}
			}
		}
	}
}

// Close closes the client connection
func (c *Client) Close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		if !c.closed {
			close(c.send)
			c.conn.Close()
			c.closed = true
		}
		c.mu.Unlock()
	})
}
