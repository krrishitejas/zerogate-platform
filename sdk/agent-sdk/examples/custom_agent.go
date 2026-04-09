package main

import (
	"context"
	"encoding/json"
	"log"
	"path/filepath"
	"strings"

	"github.com/nats-io/nats.go"
	"github.com/zerogate/sdk/agent-sdk"
)

// ExampleAgent is a custom secret scanner agent.
type ExampleAgent struct{}

func (a *ExampleAgent) Metadata() sdk.AgentMeta {
	return sdk.AgentMeta{
		Name:         "simple-secret-scanner",
		Version:      "1.0.0",
		Description:  "Scans files for dummy secret patterns",
		Events:       []string{"file.changed"},
		Capabilities: []string{"secret_scan"},
	}
}

func (a *ExampleAgent) Handle(ctx context.Context, event sdk.Event) ([]sdk.Finding, error) {
	// Parse the event payload triggered by file watcher
	var payload struct {
		ProjectID string `json:"project_id"`
		Path      string `json:"path"`
		Operation string `json:"operation"`
	}
	
	if err := json.Unmarshal(event.Data, &payload); err != nil {
		return nil, err
	}

	// Only process new/modified go files as an example
	if payload.Operation == "REMOVE" || !strings.HasSuffix(payload.Path, ".go") {
		return nil, nil // Return empty, no findings
	}

	// In a real agent, we'd read the file content from disk using payload.Path
	log.Printf("Custom Agent processing file: %s", filepath.Base(payload.Path))

	// Mocking a finding
	findings := []sdk.Finding{
		{
			RuleID:      "hardcoded-credentials",
			Severity:    "high",
			Category:    "security",
			Title:       "Hardcoded Credentials Detected",
			Description: "A hardcoded secret was found in the source file.",
			FilePath:    payload.Path,
			LineStart:   12,
			LineEnd:     12,
			CweID:       "CWE-798",
		},
	}

	return findings, nil
}

func main() {
	// Assume NATS is running locally
	nc, err := nats.Connect("nats://localhost:4222")
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	agent := &ExampleAgent{}
	runner := sdk.NewRunner(nc, agent)

	if err := runner.Start(); err != nil {
		log.Fatalf("Failed to start custom agent: %v", err)
	}

	// Block forever
	select {}
}
