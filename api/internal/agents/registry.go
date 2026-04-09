package agents

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// ExternalAgentMeta represents metadata published by a custom agent linking into the platform.
type ExternalAgentMeta struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Events       []string `json:"events"`
	Capabilities []string `json:"capabilities"`
}

// Registry dynamically tracks connected custom agents.
type Registry struct {
	nc     *nats.Conn
	agents map[string]ExternalAgentMeta
	mu     sync.RWMutex
}

func NewRegistry(nc *nats.Conn) *Registry {
	return &Registry{
		nc:     nc,
		agents: make(map[string]ExternalAgentMeta),
	}
}

func (r *Registry) Start() error {
	_, err := r.nc.Subscribe("agent.registered", func(msg *nats.Msg) {
		var meta ExternalAgentMeta
		if err := json.Unmarshal(msg.Data, &meta); err == nil {
			r.mu.Lock()
			r.agents[meta.Name] = meta
			r.mu.Unlock()
			log.Printf("[Agent Registry] Discovered custom agent: %s v%s", meta.Name, meta.Version)
		}
	})
	
	if err != nil {
		return err
	}

	// Simulated periodic log of connected custom agents
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			r.mu.RLock()
			count := len(r.agents)
			r.mu.RUnlock()
			if count > 0 {
				log.Printf("[Agent Registry] %d custom agents healthy and connected.", count)
			}
		}
	}()

	return nil
}
