package ingestion

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/nats-io/nats.go"
	"github.com/zerogate/api/internal/db"
)

type ProjectRequestedEvent struct {
	ProjectID     string `json:"project_id"`
	RepositoryURL string `json:"repository_url"`
}

type ProjectIngestedEvent struct {
	ProjectID string `json:"project_id"`
	Path      string `json:"path"`
	FileCount int    `json:"file_count"`
	LineCount int    `json:"line_count"`
}

type Agent struct {
	nc *nats.Conn
}

func NewAgent(nc *nats.Conn) *Agent {
	return &Agent{nc: nc}
}

func (a *Agent) Start() error {
	_, err := a.nc.Subscribe("project.requested", a.handleProjectRequested)
	if err != nil {
		return fmt.Errorf("failed to subscribe to project.requested: %w", err)
	}

	log.Println("Ingestion Agent started, listening for 'project.requested'")
	return nil
}

func (a *Agent) handleProjectRequested(msg *nats.Msg) {
	var event ProjectRequestedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		log.Printf("Error unmarshaling project.requested event: %v", err)
		return
	}

	log.Printf("Received ingestion request for project %s (%s)", event.ProjectID, event.RepositoryURL)

	// Clone the repo
	repoPath := filepath.Join(os.TempDir(), "zerogate-repos", event.ProjectID)
	// Clean up existing dir if any
	os.RemoveAll(repoPath)

	log.Printf("Cloning to %s...", repoPath)
	_, err := git.PlainClone(repoPath, false, &git.CloneOptions{
		URL:      event.RepositoryURL,
		Progress: nil,
		Depth:    1, // Only need the latest commit for analysis
	})
	if err != nil {
		log.Printf("Error cloning repository: %v", err)
		return
	}

	log.Printf("Repository cloned. Analyzing...")

	// Save to DB
	// Extract project name from URL (e.g. org/repo)
	name := filepath.Base(event.RepositoryURL)
	if name == "" {
		name = event.ProjectID
	}

	project := &db.Project{
		ProjectHash:   event.ProjectID,
		Name:          name,
		RepositoryURL: event.RepositoryURL,
	}
	db.DB.Create(project)

	// Count files and lines
	fileCount := 0
	lineCount := 0

	err = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		fileCount++
		
		f, err := os.Open(path)
		if err != nil {
			return nil // Skip files we can't open
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			lineCount++
		}
		return nil
	})

	if err != nil {
		log.Printf("Error analyzing repository: %v", err)
		return
	}

	log.Printf("Analysis complete: %d files, %d lines", fileCount, lineCount)

	// Publish ingested event
	ingestedEvent := ProjectIngestedEvent{
		ProjectID: event.ProjectID,
		Path:      repoPath,
		FileCount: fileCount,
		LineCount: lineCount,
	}

	data, err := json.Marshal(ingestedEvent)
	if err != nil {
		log.Printf("Error marshaling project.ingested event: %v", err)
		return
	}

	if err := a.nc.Publish("project.ingested", data); err != nil {
		log.Printf("Error publishing project.ingested event: %v", err)
		return
	}
	
	log.Printf("Published project.ingested for project %s", event.ProjectID)
}
