package watcher

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/nats-io/nats.go"
)

// WebSocketHub manages WebSocket connections per project.
type WebSocketHub struct {
	nc          *nats.Conn
	connections map[string]map[*websocket.Conn]bool // projectID -> set of connections
	mu          sync.RWMutex
	subs        []*nats.Subscription
}

// WSEvent is the JSON event sent to WebSocket clients.
type WSEvent struct {
	Type      string `json:"type"`       // file.changed, finding.detected, fix.proposed, graph.persisted, agent.log
	ProjectID string `json:"project_id"`
	Agent     string `json:"agent,omitempty"`
	Event     string `json:"event,omitempty"`
	Message   string `json:"message,omitempty"`
	Data      any    `json:"data,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
}

// NewWebSocketHub creates a new hub and subscribes to NATS events.
func NewWebSocketHub(nc *nats.Conn) *WebSocketHub {
	hub := &WebSocketHub{
		nc:          nc,
		connections: make(map[string]map[*websocket.Conn]bool),
	}

	// Subscribe to all relevant NATS events and broadcast to WebSocket clients
	events := []struct {
		subject string
		handler func(msg *nats.Msg)
	}{
		{"file.changed", hub.handleFileChanged},
		{"finding.detected", hub.handleFindingDetected},
		{"fix.proposed", hub.handleFixProposed},
		{"graph.persisted", hub.handleGraphPersisted},
	}

	for _, e := range events {
		sub, err := nc.Subscribe(e.subject, e.handler)
		if err != nil {
			log.Printf("[WebSocket] Failed to subscribe to %s: %v", e.subject, err)
			continue
		}
		hub.subs = append(hub.subs, sub)
	}

	log.Println("[WebSocket] Hub initialized, listening for NATS events")
	return hub
}

// RegisterRoutes adds the WebSocket upgrade endpoint to the Fiber app.
func (hub *WebSocketHub) RegisterRoutes(app fiber.Router) {
	app.Use("/ws", func(c *fiber.Ctx) error {
		if websocket.IsWebSocketUpgrade(c) {
			return c.Next()
		}
		return fiber.ErrUpgradeRequired
	})

	app.Get("/ws/:projectId", websocket.New(func(c *websocket.Conn) {
		projectID := c.Params("projectId")
		hub.addConnection(projectID, c)
		defer hub.removeConnection(projectID, c)

		log.Printf("[WebSocket] Client connected for project %s", projectID)

		// Send welcome event
		welcome := WSEvent{
			Type:      "connection",
			ProjectID: projectID,
			Message:   "Connected to ZEROGATE real-time event stream",
		}
		if data, err := json.Marshal(welcome); err == nil {
			c.WriteMessage(websocket.TextMessage, data)
		}

		// Keep connection alive by reading (client pings)
		for {
			_, _, err := c.ReadMessage()
			if err != nil {
				break
			}
		}

		log.Printf("[WebSocket] Client disconnected for project %s", projectID)
	}))
}

func (hub *WebSocketHub) addConnection(projectID string, conn *websocket.Conn) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if hub.connections[projectID] == nil {
		hub.connections[projectID] = make(map[*websocket.Conn]bool)
	}
	hub.connections[projectID][conn] = true
}

func (hub *WebSocketHub) removeConnection(projectID string, conn *websocket.Conn) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if conns, ok := hub.connections[projectID]; ok {
		delete(conns, conn)
		if len(conns) == 0 {
			delete(hub.connections, projectID)
		}
	}
}

// broadcast sends an event to all WebSocket clients for a project.
func (hub *WebSocketHub) broadcast(projectID string, event WSEvent) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()

	conns, ok := hub.connections[projectID]
	if !ok || len(conns) == 0 {
		return
	}

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	for conn := range conns {
		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Printf("[WebSocket] Write error, will clean up: %v", err)
		}
	}
}

// broadcastAll sends an event to all connected clients regardless of project.
func (hub *WebSocketHub) broadcastAll(event WSEvent) {
	hub.mu.RLock()
	defer hub.mu.RUnlock()

	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	for _, conns := range hub.connections {
		for conn := range conns {
			conn.WriteMessage(websocket.TextMessage, data)
		}
	}
}

// NATS event handlers

func (hub *WebSocketHub) handleFileChanged(msg *nats.Msg) {
	var raw map[string]any
	if err := json.Unmarshal(msg.Data, &raw); err != nil {
		return
	}

	projectID, _ := raw["project_id"].(string)
	filePath, _ := raw["file_path"].(string)
	changeType, _ := raw["change_type"].(string)
	timestamp, _ := raw["timestamp"].(string)

	hub.broadcast(projectID, WSEvent{
		Type:      "file.changed",
		ProjectID: projectID,
		Agent:     "FILE_WATCHER",
		Event:     "FILE_" + toUpper(changeType),
		Message:   filePath,
		Timestamp: timestamp,
	})
}

func (hub *WebSocketHub) handleFindingDetected(msg *nats.Msg) {
	var raw map[string]any
	if err := json.Unmarshal(msg.Data, &raw); err != nil {
		return
	}

	projectID, _ := raw["project_id"].(string)
	category, _ := raw["category"].(string)
	title, _ := raw["title"].(string)
	severity, _ := raw["severity"].(string)

	agentName := "ANALYSIS_AGENT"
	switch category {
	case "bug":
		agentName = "BUG_AGENT"
	case "security":
		agentName = "SECURITY_AGENT"
	case "performance":
		agentName = "PERF_AGENT"
	case "architecture":
		agentName = "LOGIC_AGENT"
	}

	hub.broadcast(projectID, WSEvent{
		Type:      "finding.detected",
		ProjectID: projectID,
		Agent:     agentName,
		Event:     "FINDING_" + toUpper(severity),
		Message:   title,
		Data:      raw,
	})
}

func (hub *WebSocketHub) handleFixProposed(msg *nats.Msg) {
	var raw map[string]any
	if err := json.Unmarshal(msg.Data, &raw); err != nil {
		return
	}

	filePath, _ := raw["file_path"].(string)

	// Broadcast to all projects since we may not know the project ID directly
	hub.broadcastAll(WSEvent{
		Type:    "fix.proposed",
		Agent:   "AUTOFIX_AGENT",
		Event:   "PATCH_READY",
		Message: "Auto-fix patch generated for " + filePath,
		Data:    raw,
	})
}

func (hub *WebSocketHub) handleGraphPersisted(msg *nats.Msg) {
	var raw map[string]any
	if err := json.Unmarshal(msg.Data, &raw); err != nil {
		return
	}

	projectID, _ := raw["project_id"].(string)

	hub.broadcast(projectID, WSEvent{
		Type:      "graph.persisted",
		ProjectID: projectID,
		Agent:     "KG_AGENT",
		Event:     "GRAPH_SYNC",
		Message:   "Knowledge graph updated in Memgraph",
		Data:      raw,
	})
}

func toUpper(s string) string {
	if s == "" {
		return "UNKNOWN"
	}
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		result[i] = c
	}
	return string(result)
}
