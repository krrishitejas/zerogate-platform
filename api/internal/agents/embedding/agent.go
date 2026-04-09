package embedding

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/zerogate/api/internal/db"
)

type Agent struct {
	nc *nats.Conn
}

func NewAgent(nc *nats.Conn) *Agent {
	return &Agent{nc: nc}
}

// GraphNode matches the structure sent by the parser
type GraphNode struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
}

type GraphExtractedEvent struct {
	ProjectID string      `json:"project_id"`
	Nodes     []GraphNode `json:"nodes"`
}

func (a *Agent) Start() error {
	_, err := a.nc.Subscribe("graph.extracted", a.handleGraphExtracted)
	if err != nil {
		return fmt.Errorf("failed to subscribe to graph.extracted: %w", err)
	}

	log.Println("Embedding Agent started, listening for 'graph.extracted'")
	return nil
}

func (a *Agent) handleGraphExtracted(msg *nats.Msg) {
	var event GraphExtractedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		log.Printf("Error unmarshaling graph.extracted in embedding agent: %v", err)
		return
	}

	log.Printf("Embedding Agent: Creating embeddings for project %s (%d nodes)", event.ProjectID, len(event.Nodes))

	// Delete existing embeddings for this project before re-indexing
	db.DeleteProjectEmbeddings(event.ProjectID)

	repoDir := filepath.Join(os.TempDir(), "zerogate-repos", event.ProjectID)
	var points []db.QdrantPoint

	for _, node := range event.Nodes {
		// Build rich text document for embedding based on entity type
		docText := buildEmbeddingDocument(node, repoDir)

		vector, err := GenerateEmbedding(docText)
		if err != nil {
			log.Printf("Failed to generate embedding for node %s: %v", node.ID, err)
			continue
		}

		// Qdrant string IDs must be valid UUIDs
		pointID := uuid.New().String()

		// Truncate content preview for payload
		contentPreview := docText
		if len(contentPreview) > 500 {
			contentPreview = contentPreview[:500] + "..."
		}

		payload := map[string]any{
			"project_id":      event.ProjectID,
			"node_id":         node.ID,
			"node_label":      node.Label,
			"node_name":       node.Name,
			"node_type":       node.Type,
			"line_start":      node.LineStart,
			"line_end":        node.LineEnd,
			"content_preview": contentPreview,
		}

		points = append(points, db.QdrantPoint{
			ID:      pointID,
			Vector:  vector,
			Payload: payload,
		})
	}

	// Batch upsert to Qdrant
	if err := db.UpsertEmbeddings(points); err != nil {
		log.Printf("Error upserting vectors to Qdrant for project %s: %v", event.ProjectID, err)
		return
	}

	log.Printf("Successfully pushed %d vector embeddings to Qdrant for project %s", len(points), event.ProjectID)
}

// buildEmbeddingDocument creates a rich text document for embedding based on entity type.
func buildEmbeddingDocument(node GraphNode, repoDir string) string {
	var sb strings.Builder

	switch node.Label {
	case "File":
		// Embed actual file content (first 2000 chars)
		fullPath := filepath.Join(repoDir, node.Name)
		if content, err := os.ReadFile(fullPath); err == nil {
			contentStr := string(content)
			if len(contentStr) > 2000 {
				contentStr = contentStr[:2000]
			}
			sb.WriteString(fmt.Sprintf("File: %s\nLanguage: %s\n\n%s", node.Name, node.Type, contentStr))
		} else {
			sb.WriteString(fmt.Sprintf("File: %s\nLanguage: %s", node.Name, node.Type))
		}

	case "Function":
		// Embed function with its source code if we can find it
		sb.WriteString(fmt.Sprintf("Function: %s\nType: %s\nLines: %d-%d\n", node.Name, node.Type, node.LineStart, node.LineEnd))
		// Try to read the function source from the file
		if content := extractSourceLines(repoDir, node); content != "" {
			sb.WriteString(fmt.Sprintf("\nSource:\n%s", content))
		}

	case "Class":
		sb.WriteString(fmt.Sprintf("Class/Struct: %s\nType: %s\nLines: %d-%d\n", node.Name, node.Type, node.LineStart, node.LineEnd))
		if content := extractSourceLines(repoDir, node); content != "" {
			sb.WriteString(fmt.Sprintf("\nSource:\n%s", content))
		}

	case "Import":
		sb.WriteString(fmt.Sprintf("Import/Dependency: %s\nType: %s", node.Name, node.Type))

	default:
		sb.WriteString(fmt.Sprintf("Entity: %s\nLabel: %s\nType: %s\nName: %s", node.ID, node.Label, node.Type, node.Name))
	}

	return sb.String()
}

// extractSourceLines reads lines from a source file for a given node's line range.
func extractSourceLines(repoDir string, node GraphNode) string {
	if node.LineStart == 0 || node.LineEnd == 0 {
		return ""
	}

	// Extract the file path from the node ID: "function:projID:path/to/file.go:funcName"
	parts := strings.SplitN(node.ID, ":", 4)
	if len(parts) < 4 {
		return ""
	}
	filePath := parts[2]
	fullPath := filepath.Join(repoDir, filePath)

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return ""
	}

	lines := strings.Split(string(content), "\n")
	startIdx := node.LineStart - 1
	endIdx := node.LineEnd
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > len(lines) {
		endIdx = len(lines)
	}
	if startIdx >= endIdx {
		return ""
	}

	// Limit extracted content
	extracted := strings.Join(lines[startIdx:endIdx], "\n")
	if len(extracted) > 1500 {
		extracted = extracted[:1500]
	}

	return extracted
}
