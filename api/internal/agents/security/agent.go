package security

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/nats-io/nats.go"
	"github.com/zerogate/api/internal/llm"
	"github.com/zerogate/api/internal/scanner"
)

type GraphNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Name  string `json:"name"`
	Type  string `json:"type"`
}

type GraphExtractedEvent struct {
	ProjectID string      `json:"project_id"`
	Nodes     []GraphNode `json:"nodes"`
}

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
	Source      string  `json:"source"`
}

type Agent struct {
	nc *nats.Conn
}

func NewAgent(nc *nats.Conn) *Agent {
	return &Agent{nc: nc}
}

func (a *Agent) Start() error {
	_, err := a.nc.Subscribe("graph.extracted", a.handleGraphExtracted)
	if err != nil {
		return fmt.Errorf("failed to subscribe to graph.extracted: %w", err)
	}

	log.Println("Security Vulnerability Agent started, listening for 'graph.extracted'")
	return nil
}

func (a *Agent) handleGraphExtracted(msg *nats.Msg) {
	var event GraphExtractedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return
	}

	repoDir := filepath.Join(os.TempDir(), "zerogate-repos", event.ProjectID)
	useLLM := llm.IsOllamaAvailable()

	for _, node := range event.Nodes {
		if node.Label == "File" {
			fullPath := filepath.Join(repoDir, node.Name)

			// Phase 1: Regex-based SAST rules (CWE-tagged)
			results, err := scanner.ScanFileForSecurity(fullPath)
			if err != nil {
				continue
			}

			for _, res := range results {
				a.publishFinding(event.ProjectID, node.Name, res)
			}

			// Phase 2: LLM deep security analysis (Qwen2.5-Coder or fallback)
			if useLLM {
				model := os.Getenv("SECURITY_MODEL")
				if model == "" {
					model = "" // Use default from config
				}
				llmResults, err := scanner.ScanFileWithLLM(fullPath, "security", model)
				if err != nil {
					log.Printf("[Security Agent] LLM analysis failed for %s: %v", node.Name, err)
					continue
				}
				for _, res := range llmResults {
					a.publishFinding(event.ProjectID, node.Name, res)
				}
			}
		}
	}
}

func (a *Agent) publishFinding(projectID, filePath string, res scanner.FindingResult) {
	finding := FindingDetectedEvent{
		ProjectID:   projectID,
		RuleID:      res.RuleID,
		Severity:    res.Severity,
		Category:    "security",
		Title:       res.Title,
		Description: res.Description,
		FilePath:    filePath,
		LineStart:   res.LineStart,
		LineEnd:     res.LineEnd,
		CweID:       res.CweID,
		CvssScore:   res.CvssScore,
		RootCause:   res.RootCause,
		Impact:      res.Impact,
		Confidence:  res.Confidence,
		Source:      res.Source,
	}

	data, _ := json.Marshal(finding)
	a.nc.Publish("finding.detected", data)
	log.Printf("[Security Agent] %s finding for %s (Rule: %s, CWE: %s)", res.Severity, filePath, res.RuleID, res.CweID)
}
