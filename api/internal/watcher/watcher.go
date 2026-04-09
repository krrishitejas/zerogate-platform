package watcher

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/nats-io/nats.go"
)

// FileChangedEvent is published to NATS when a file is modified.
type FileChangedEvent struct {
	ProjectID  string `json:"project_id"`
	FilePath   string `json:"file_path"`
	ChangeType string `json:"change_type"` // modified, created, deleted
	Timestamp  string `json:"timestamp"`
}

// WatcherService manages file watchers for active projects.
type WatcherService struct {
	nc       *nats.Conn
	watchers map[string]*fsnotify.Watcher
	mu       sync.Mutex
	// Debounce: collect changes and batch them
	pending  map[string]map[string]string // projectID -> {filePath -> changeType}
	debounce time.Duration
}

// NewWatcherService creates a new file watcher service.
func NewWatcherService(nc *nats.Conn) *WatcherService {
	ws := &WatcherService{
		nc:       nc,
		watchers: make(map[string]*fsnotify.Watcher),
		pending:  make(map[string]map[string]string),
		debounce: 2 * time.Second,
	}
	go ws.flushLoop()
	return ws
}

// StartWatching begins watching a project's repository directory for changes.
func (ws *WatcherService) StartWatching(projectID, repoPath string) error {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	// Stop existing watcher if any
	if existing, ok := ws.watchers[projectID]; ok {
		existing.Close()
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	ws.watchers[projectID] = watcher
	ws.pending[projectID] = make(map[string]string)

	// Recursively add all directories
	err = filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// Skip non-source directories
			if name == ".git" || name == "node_modules" || name == "vendor" ||
				name == "__pycache__" || name == ".next" || name == "target" ||
				name == "build" || name == "dist" {
				return filepath.SkipDir
			}
			return watcher.Add(path)
		}
		return nil
	})
	if err != nil {
		watcher.Close()
		return err
	}

	// Start event processing goroutine
	go ws.processEvents(projectID, repoPath, watcher)

	log.Printf("[FileWatcher] Started watching project %s at %s", projectID, repoPath)
	return nil
}

// StopWatching stops watching a project's directory.
func (ws *WatcherService) StopWatching(projectID string) {
	ws.mu.Lock()
	defer ws.mu.Unlock()

	if watcher, ok := ws.watchers[projectID]; ok {
		watcher.Close()
		delete(ws.watchers, projectID)
		delete(ws.pending, projectID)
		log.Printf("[FileWatcher] Stopped watching project %s", projectID)
	}
}

// IsWatching returns whether a project is currently being watched.
func (ws *WatcherService) IsWatching(projectID string) bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	_, ok := ws.watchers[projectID]
	return ok
}

// processEvents handles fsnotify events for a project.
func (ws *WatcherService) processEvents(projectID, repoPath string, watcher *fsnotify.Watcher) {
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Skip directory events and temporary files
			if strings.HasSuffix(event.Name, "~") || strings.HasPrefix(filepath.Base(event.Name), ".") {
				continue
			}

			relPath, err := filepath.Rel(repoPath, event.Name)
			if err != nil {
				continue
			}

			var changeType string
			switch {
			case event.Op&fsnotify.Create == fsnotify.Create:
				changeType = "created"
				// If it's a new directory, add it to the watcher
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					watcher.Add(event.Name)
					continue
				}
			case event.Op&fsnotify.Write == fsnotify.Write:
				changeType = "modified"
			case event.Op&fsnotify.Remove == fsnotify.Remove:
				changeType = "deleted"
			case event.Op&fsnotify.Rename == fsnotify.Rename:
				changeType = "deleted"
			default:
				continue
			}

			// Queue the change for debounced processing
			ws.mu.Lock()
			if _, ok := ws.pending[projectID]; !ok {
				ws.pending[projectID] = make(map[string]string)
			}
			ws.pending[projectID][relPath] = changeType
			ws.mu.Unlock()

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[FileWatcher] Error for project %s: %v", projectID, err)
		}
	}
}

// flushLoop periodically flushes pending file changes to NATS.
func (ws *WatcherService) flushLoop() {
	ticker := time.NewTicker(ws.debounce)
	defer ticker.Stop()

	for range ticker.C {
		ws.mu.Lock()
		for projectID, changes := range ws.pending {
			if len(changes) == 0 {
				continue
			}

			// Process all pending changes for this project
			for filePath, changeType := range changes {
				event := FileChangedEvent{
					ProjectID:  projectID,
					FilePath:   filePath,
					ChangeType: changeType,
					Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
				}

				data, err := json.Marshal(event)
				if err != nil {
					continue
				}

				if err := ws.nc.Publish("file.changed", data); err != nil {
					log.Printf("[FileWatcher] Failed to publish file.changed: %v", err)
					continue
				}

				log.Printf("[FileWatcher] %s: %s (%s)", projectID, filePath, changeType)
			}

			// Clear pending changes for this project
			ws.pending[projectID] = make(map[string]string)
		}
		ws.mu.Unlock()
	}
}
