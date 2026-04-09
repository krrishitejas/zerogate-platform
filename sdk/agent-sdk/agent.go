package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
)

// AgentMeta provides metadata about the custom agent to the platform.
type AgentMeta struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Events       []string `json:"events"`       // NATS subjects to subscribe to
	Capabilities []string `json:"capabilities"` // e.g. "ast", "llm", "secret_scan"
}

// Event represents an incoming payload from NATS.
type Event struct {
	Subject string
	Data    []byte
}

// Finding represents an issue detected by the agent.
type Finding struct {
	RuleID      string  `json:"rule_id"`
	Severity    string  `json:"severity"` // critical, high, medium, low, info
	Category    string  `json:"category"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	FilePath    string  `json:"file_path"`
	LineStart   int     `json:"line_start,omitempty"`
	LineEnd     int     `json:"line_end,omitempty"`
	CweID       string  `json:"cwe_id,omitempty"`
	CvssScore   float64 `json:"cvss_score,omitempty"`
	Source      string  `json:"source,omitempty"`
}

// Agent is the interface that all third-party agents must implement.
type Agent interface {
	Metadata() AgentMeta
	Handle(ctx context.Context, event Event) ([]Finding, error)
}

// AgentRunner manages the NATS subscription and event routing for a custom agent.
type AgentRunner struct {
	nc     *nats.Conn
	agent  Agent
	subs   []*nats.Subscription
	ctx    context.Context
	cancel context.CancelFunc
}

// NewRunner creates a new AgentRunner for the given custom agent.
func NewRunner(nc *nats.Conn, agent Agent) *AgentRunner {
	ctx, cancel := context.WithCancel(context.Background())
	return &AgentRunner{
		nc:     nc,
		agent:  agent,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Start registers the agent on the NATS bus and begins listening for its requested events.
func (r *AgentRunner) Start() error {
	meta := r.agent.Metadata()

	log.Printf("Starting Custom Agent: %s (v%s)", meta.Name, meta.Version)

	// Subscribe to all requested events
	for _, subject := range meta.Events {
		sub, err := r.nc.Subscribe(subject, func(msg *nats.Msg) {
			go r.processMessage(msg)
		})
		if err != nil {
			return fmt.Errorf("failed to subscribe to %s: %w", subject, err)
		}
		r.subs = append(r.subs, sub)
		log.Printf("[%s] Subscribed to %s", meta.Name, subject)
	}

	// Publish heartbeat to register with the platform
	go r.heartbeatLoop()

	return nil
}

func (r *AgentRunner) processMessage(msg *nats.Msg) {
	// Optional context timeout per event processing can be added here
	ctx := context.Background()

	event := Event{
		Subject: msg.Subject,
		Data:    msg.Data,
	}

	meta := r.agent.Metadata()

	// 1. Let the agent handle the event
	findings, err := r.agent.Handle(ctx, event)
	if err != nil {
		log.Printf("[%s] Error handling event %s: %v", meta.Name, msg.Subject, err)
		return
	}

	// 2. Publish any findings emitted by the agent back to the aggregator
	for _, finding := range findings {
		// Embed the project ID (needs to be parsed from the original event payload, simplifying for SDK via a helper convention)
		var baseEvent map[string]interface{}
		_ = json.Unmarshal(msg.Data, &baseEvent)
		projectID, _ := baseEvent["project_id"].(string)

		if projectID == "" {
			continue
		}

		findingPayload := map[string]interface{}{
			"project_id":  projectID,
			"rule_id":     finding.RuleID,
			"severity":    finding.Severity,
			"category":    finding.Category,
			"title":       finding.Title,
			"description": finding.Description,
			"file_path":   finding.FilePath,
			"line_start":  finding.LineStart,
			"line_end":    finding.LineEnd,
			"cwe_id":      finding.CweID,
			"cvss_score":  finding.CvssScore,
			"source":      meta.Name,
		}

		data, _ := json.Marshal(findingPayload)
		_ = r.nc.Publish("finding.detected", data)
	}

	if len(findings) > 0 {
		log.Printf("[%s] Published %d findings", meta.Name, len(findings))
	}
}

func (r *AgentRunner) heartbeatLoop() {
	meta := r.agent.Metadata()
	data, _ := json.Marshal(meta)
	
	// Register on startup
	_ = r.nc.Publish("agent.registered", data)
	
	// Could loop and ping periodically if needed by platform
}

// Stop gracefully shuts down the agent runner.
func (r *AgentRunner) Stop() {
	log.Printf("Stopping Agent: %s", r.agent.Metadata().Name)
	r.cancel()
	for _, sub := range r.subs {
		_ = sub.Unsubscribe()
	}
}
