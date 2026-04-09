package aggregator

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
	"github.com/zerogate/api/internal/db"
)

type FindingDetectedEvent struct {
	ProjectID   string  `json:"project_id"`
	RuleID      string  `json:"rule_id"`
	Severity    string  `json:"severity"`
	Category    string  `json:"category"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	FilePath    string  `json:"file_path"`
	LineStart   int     `json:"line_start"`
	LineEnd     int     `json:"line_end"`
	CweID       string  `json:"cwe_id,omitempty"`
	CvssScore   float64 `json:"cvss_score,omitempty"`
	RootCause   string  `json:"root_cause,omitempty"`
	Impact      string  `json:"impact,omitempty"`
	Confidence  float64 `json:"confidence,omitempty"`
	Source      string  `json:"source,omitempty"`
}

type Agent struct {
	nc *nats.Conn
}

func NewAgent(nc *nats.Conn) *Agent {
	return &Agent{nc: nc}
}

func (a *Agent) Start() error {
	_, err := a.nc.Subscribe("finding.detected", a.handleFindingDetected)
	if err != nil {
		return fmt.Errorf("failed to subscribe to finding.detected: %w", err)
	}

	log.Println("Aggregator Agent started, listening for 'finding.detected'")
	return nil
}

func (a *Agent) handleFindingDetected(msg *nats.Msg) {
	var event FindingDetectedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return
	}

	finding := db.Finding{
		ProjectHash: event.ProjectID,
		RuleID:      event.RuleID,
		Category:    event.Category,
		Title:       event.Title,
		Severity:    event.Severity,
		Description: event.Description,
		FilePath:    event.FilePath,
		LineStart:   event.LineStart,
		LineEnd:     event.LineEnd,
		CweID:       event.CweID,
		CvssScore:   event.CvssScore,
		RootCause:   event.RootCause,
		Impact:      event.Impact,
		Confidence:  event.Confidence,
		Source:      event.Source,
		Status:      "open",
		HasFix:      false,
	}

	if err := db.SaveFinding(&finding); err != nil {
		log.Printf("Failed to save finding to DB: %v", err)
		return
	}
	log.Printf("[Aggregator] Saved finding %s to DB with ID %d (source: %s)", event.RuleID, finding.ID, event.Source)

	// Publish finding.created with the actual DB ID for autofix agent
	eventWithID := map[string]interface{}{
		"id":          finding.ID,
		"project_id":  event.ProjectID,
		"rule_id":     event.RuleID,
		"file_path":   event.FilePath,
		"description": event.Description,
		"line_start":  event.LineStart,
	}
	data, _ := json.Marshal(eventWithID)
	a.nc.Publish("finding.created", data)
}
