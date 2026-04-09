package knowledgegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/nats-io/nats.go"
	"github.com/zerogate/api/internal/db"
)

type Agent struct {
	nc *nats.Conn
}

func NewAgent(nc *nats.Conn) *Agent {
	return &Agent{nc: nc}
}

// GraphExtractedEvent matches the payload from the Parser Agent
type GraphExtractedEvent struct {
	ProjectID string         `json:"project_id"`
	Nodes     []db.GraphNode `json:"nodes"`
	Edges     []db.GraphEdge `json:"edges"`
}

// GraphPersistedEvent notifies downstream that the graph is queryable.
type GraphPersistedEvent struct {
	ProjectID  string `json:"project_id"`
	NodeCount  int    `json:"node_count"`
	EdgeCount  int    `json:"edge_count"`
}

func (a *Agent) Start() error {
	_, err := a.nc.Subscribe("graph.extracted", a.handleGraphExtracted)
	if err != nil {
		return fmt.Errorf("failed to subscribe to graph.extracted: %w", err)
	}

	// Listen for incremental file change events
	_, err = a.nc.Subscribe("file.changed", a.handleFileChanged)
	if err != nil {
		log.Printf("Warning: could not subscribe to file.changed: %v", err)
	}

	log.Println("Knowledge Graph Agent started, listening for 'graph.extracted'")
	return nil
}

func (a *Agent) handleGraphExtracted(msg *nats.Msg) {
	var event GraphExtractedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		log.Printf("Error unmarshaling graph.extracted event: %v", err)
		return
	}

	log.Printf("Knowledge Graph Agent: Received graph for project %s (%d nodes, %d edges)", event.ProjectID, len(event.Nodes), len(event.Edges))

	ctx := context.Background()

	// Save nodes and edges to Memgraph
	err := db.SaveNodesAndEdges(ctx, event.ProjectID, event.Nodes, event.Edges)
	if err != nil {
		log.Printf("Error saving graph to Memgraph: %v", err)
		return
	}

	log.Printf("Successfully persisted graph into Memgraph for project %s", event.ProjectID)

	// Publish graph.persisted so downstream agents know the graph is queryable
	persistedEvent := GraphPersistedEvent{
		ProjectID: event.ProjectID,
		NodeCount: len(event.Nodes),
		EdgeCount: len(event.Edges),
	}

	data, err := json.Marshal(persistedEvent)
	if err != nil {
		log.Printf("Error marshaling graph.persisted event: %v", err)
		return
	}

	if err := a.nc.Publish("graph.persisted", data); err != nil {
		log.Printf("Error publishing graph.persisted event: %v", err)
		return
	}

	log.Printf("Published graph.persisted for project %s", event.ProjectID)
}

// handleFileChanged handles incremental file changes for graph updates.
type FileChangedEvent struct {
	ProjectID  string `json:"project_id"`
	FilePath   string `json:"file_path"`
	ChangeType string `json:"change_type"` // modified, created, deleted
}

func (a *Agent) handleFileChanged(msg *nats.Msg) {
	var event FileChangedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return
	}

	ctx := context.Background()
	fileID := "file:" + event.ProjectID + ":" + event.FilePath

	switch event.ChangeType {
	case "deleted":
		// Remove the file and its subgraph from Memgraph
		if err := db.DeleteFileSubgraph(ctx, event.ProjectID, fileID); err != nil {
			log.Printf("Error deleting file subgraph for %s: %v", event.FilePath, err)
		}
		log.Printf("[KG Agent] Deleted graph subgraph for removed file: %s", event.FilePath)

	case "modified", "created":
		// For modified files, delete old subgraph first, then let the parser re-extract
		if event.ChangeType == "modified" {
			if err := db.DeleteFileSubgraph(ctx, event.ProjectID, fileID); err != nil {
				log.Printf("Error deleting old subgraph for %s: %v", event.FilePath, err)
			}
		}
		log.Printf("[KG Agent] Queued incremental graph update for: %s", event.FilePath)
	}
}
