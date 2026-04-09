package autofix

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/nats-io/nats.go"
	"github.com/zerogate/api/internal/db"
	"github.com/zerogate/api/internal/llm"
)

type FindingCreatedEvent struct {
	ID          uint   `json:"id"`
	ProjectID   string `json:"project_id"`
	RuleID      string `json:"rule_id"`
	FilePath    string `json:"file_path"`
	Description string `json:"description"`
	LineStart   int    `json:"line_start"`
}

type FixProposedEvent struct {
	FindingID uint   `json:"finding_id"`
	FilePath  string `json:"file_path"`
	Patch     string `json:"patch"`
}

type Agent struct {
	nc *nats.Conn
}

func NewAgent(nc *nats.Conn) *Agent {
	return &Agent{nc: nc}
}

func (a *Agent) Start() error {
	_, err := a.nc.Subscribe("finding.created", a.handleFindingCreated)
	if err != nil {
		return fmt.Errorf("failed to subscribe to finding.created: %w", err)
	}

	log.Println("Auto-Fix Agent started, listening for 'finding.created'")
	return nil
}

func (a *Agent) handleFindingCreated(msg *nats.Msg) {
	var event FindingCreatedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return
	}

	// Load the finding from DB for full context
	var finding db.Finding
	if db.DB != nil {
		db.DB.First(&finding, event.ID)
	}

	if finding.LineStart == 0 {
		finding.LineStart = event.LineStart
		if finding.LineStart == 0 {
			finding.LineStart = 1
		}
	}
	if finding.Description == "" {
		finding.Description = event.Description
	}

	// Read the actual source file for context
	repoDir := filepath.Join(os.TempDir(), "zerogate-repos", event.ProjectID)
	fullPath := filepath.Join(repoDir, event.FilePath)

	codeSnippet := ""
	if data, err := os.ReadFile(fullPath); err == nil {
		codeSnippet = string(data)
		// Limit to reasonable context window
		if len(codeSnippet) > 6000 {
			codeSnippet = codeSnippet[:6000]
		}
	}

	// Generate patch using LLM (with fallback to stub)
	patch, err := llm.GeneratePatch(fullPath, codeSnippet, finding.Description, finding.LineStart)
	if err != nil {
		log.Printf("[AutoFix Agent] Failed to generate patch: %v", err)
		return
	}

	// Save remediation back to DB
	if err := db.UpdateFindingRemediation(event.ID, patch); err != nil {
		log.Printf("Failed to save patch to DB: %v", err)
		return
	}

	log.Printf("[AutoFix Agent] Proposed fix for finding %d in %s", event.ID, event.FilePath)

	// Publish for validation
	proposed := FixProposedEvent{
		FindingID: event.ID,
		FilePath:  event.FilePath,
		Patch:     patch,
	}
	data, _ := json.Marshal(proposed)
	a.nc.Publish("fix.proposed", data)
}
