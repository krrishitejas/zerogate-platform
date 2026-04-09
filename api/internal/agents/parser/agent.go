package parser

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"strings"

	"github.com/nats-io/nats.go"
	tsparser "github.com/zerogate/api/internal/parser"
)

type ProjectIngestedEvent struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
	FileCount int    `json:"file_count"`
	LineCount int    `json:"line_count"`
}

type Node struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type Edge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // e.g., CONTAINS, DEFINED_IN, CALLS, IMPORTS
}

type GraphExtractedEvent struct {
	ProjectID string `json:"project_id"`
	Nodes     []Node `json:"nodes"`
	Edges     []Edge `json:"edges"`
}

type Agent struct {
	nc *nats.Conn
}

func NewAgent(nc *nats.Conn) *Agent {
	return &Agent{nc: nc}
}

func (a *Agent) Start() error {
	_, err := a.nc.Subscribe("project.ingested", a.handleProjectIngested)
	if err != nil {
		return fmt.Errorf("failed to subscribe to project.ingested: %w", err)
	}

	log.Println("AST Parser Agent started, listening for 'project.ingested'")
	return nil
}

func (a *Agent) handleProjectIngested(msg *nats.Msg) {
	var event ProjectIngestedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		log.Printf("Error unmarshaling project.ingested event: %v", err)
		return
	}

	log.Printf("Received parsing request for project %s at path %s", event.ProjectID, event.Path)

	var nodes []Node
	var edges []Edge

	// Create root node for the project
	projectNodeID := "proj:" + event.ProjectID
	nodes = append(nodes, Node{
		ID:    projectNodeID,
		Label: "Project",
		Name:  event.ProjectID,
		Type:  "repository",
	})

	parsedFiles := 0
	totalEntities := 0

	// Walk directory and parse each file
	err := filepath.WalkDir(event.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirName := d.Name()
			// Skip common non-source directories
			if dirName == ".git" || dirName == "node_modules" || dirName == "vendor" ||
				dirName == "__pycache__" || dirName == ".next" || dirName == "target" ||
				dirName == "build" || dirName == "dist" {
				return filepath.SkipDir
			}
			return nil
		}

		// Calculate relative path for stable node IDs
		relPath, err := filepath.Rel(event.Path, path)
		if err != nil {
			return nil
		}

		fileExt := strings.ToLower(filepath.Ext(d.Name()))
		fileID := "file:" + event.ProjectID + ":" + relPath

		// Add file node
		nodes = append(nodes, Node{
			ID:    fileID,
			Label: "File",
			Name:  relPath,
			Type:  fileExt,
		})

		// File belongs to project
		edges = append(edges, Edge{
			Source: projectNodeID,
			Target: fileID,
			Type:   "CONTAINS",
		})

		// Attempt Tree-sitter AST parsing for supported languages
		if tsparser.IsSupportedExtension(fileExt) {
			astNodes, astEdges, err := tsparser.ParseFileAST(event.ProjectID, event.Path, relPath)
			if err != nil {
				// Non-fatal: log and continue with file-level node only
				log.Printf("  AST parse warning for %s: %v", relPath, err)
				return nil
			}

			// Convert AST nodes to graph nodes
			for _, an := range astNodes {
				nodes = append(nodes, Node{
					ID:        an.ID,
					Label:     an.Label,
					Name:      an.Name,
					Type:      an.Type,
					LineStart: an.LineStart,
					LineEnd:   an.LineEnd,
					Signature: an.Signature,
				})
				totalEntities++
			}

			// Convert AST edges to graph edges
			for _, ae := range astEdges {
				edges = append(edges, Edge{
					Source: ae.Source,
					Target: ae.Target,
					Type:   ae.Type,
				})
			}

			parsedFiles++
		}

		return nil
	})

	if err != nil {
		log.Printf("Error walking directory for parsing: %v", err)
		return
	}

	// Publish extracted graph
	graphEvent := GraphExtractedEvent{
		ProjectID: event.ProjectID,
		Nodes:     nodes,
		Edges:     edges,
	}

	data, err := json.Marshal(graphEvent)
	if err != nil {
		log.Printf("Error marshaling graph.extracted event: %v", err)
		return
	}

	if err := a.nc.Publish("graph.extracted", data); err != nil {
		log.Printf("Error publishing graph.extracted event: %v", err)
		return
	}

	log.Printf("Successfully parsed project %s: %d nodes, %d edges (%d files AST-parsed, %d entities extracted)",
		event.ProjectID, len(nodes), len(edges), parsedFiles, totalEntities)
}
