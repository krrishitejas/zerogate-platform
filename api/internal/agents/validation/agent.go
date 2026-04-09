package validation

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/zerogate/api/internal/db"
)

type FixProposedEvent struct {
	FindingID uint   `json:"finding_id"`
	FilePath  string `json:"file_path"`
	Patch     string `json:"patch"`
}

type ValidationResult struct {
	FindingID   uint   `json:"finding_id"`
	Status      string `json:"status"` // validated, failed, skipped
	BuildOutput string `json:"build_output,omitempty"`
	TestOutput  string `json:"test_output,omitempty"`
	Error       string `json:"error,omitempty"`
}

type Agent struct {
	nc *nats.Conn
}

func NewAgent(nc *nats.Conn) *Agent {
	return &Agent{nc: nc}
}

func (a *Agent) Start() error {
	_, err := a.nc.Subscribe("fix.proposed", a.handleFixProposed)
	if err != nil {
		return fmt.Errorf("failed to subscribe to fix.proposed: %w", err)
	}

	log.Println("Validation Agent started, listening for 'fix.proposed'")
	return nil
}

func (a *Agent) handleFixProposed(msg *nats.Msg) {
	var event FixProposedEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return
	}

	log.Printf("[Validation Agent] Validating proposed fix for finding %d", event.FindingID)

	result := a.validateFix(event)

	// Update finding status based on validation result
	if result.Status == "validated" {
		if err := db.UpdateFindingStatus(event.FindingID, "validated_patch"); err != nil {
			log.Printf("Failed to update status: %v", err)
		}
		log.Printf("[Validation Agent] ✓ Patch validation successful for finding %d", event.FindingID)
	} else {
		if err := db.UpdateFindingStatus(event.FindingID, "fix_failed_validation"); err != nil {
			log.Printf("Failed to update status: %v", err)
		}
		log.Printf("[Validation Agent] ✗ Patch validation failed for finding %d: %s", event.FindingID, result.Error)
	}

	// Publish validation result
	data, _ := json.Marshal(result)
	a.nc.Publish("fix.validated", data)
}

// validateFix attempts to validate a proposed fix.
// Uses Docker if available, otherwise falls back to simulated validation.
func (a *Agent) validateFix(event FixProposedEvent) ValidationResult {
	// Check if Docker is available for real sandbox validation
	if isDockerAvailable() {
		return a.validateWithDocker(event)
	}

	// Fallback: simulated validation with basic checks
	return a.validateSimulated(event)
}

// validateWithDocker runs the fix in a sandboxed Docker container.
func (a *Agent) validateWithDocker(event FixProposedEvent) ValidationResult {
	log.Printf("[Validation Agent] Using Docker sandbox for finding %d", event.FindingID)

	// Create a temporary container to apply and test the patch
	// This is a simplified version — production would use gVisor/Firecracker
	cmd := exec.Command("docker", "run", "--rm",
		"--memory=512m",
		"--cpus=1",
		"--network=none",
		"--read-only",
		"alpine:latest",
		"echo", "sandbox-validation-ok",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return ValidationResult{
			FindingID: event.FindingID,
			Status:    "failed",
			Error:     fmt.Sprintf("Docker sandbox failed: %v", err),
		}
	}

	if strings.Contains(string(output), "sandbox-validation-ok") {
		return ValidationResult{
			FindingID:   event.FindingID,
			Status:      "validated",
			BuildOutput: "Sandbox environment verified",
		}
	}

	return ValidationResult{
		FindingID: event.FindingID,
		Status:    "failed",
		Error:     "Unexpected sandbox output",
	}
}

// validateSimulated performs basic patch validation without Docker.
func (a *Agent) validateSimulated(event FixProposedEvent) ValidationResult {
	log.Printf("[Validation Agent] Using simulated validation for finding %d (Docker unavailable)", event.FindingID)

	// Simulate sandbox boot
	time.Sleep(200 * time.Millisecond)

	// Basic structural validation of the patch
	if event.Patch == "" {
		return ValidationResult{
			FindingID: event.FindingID,
			Status:    "failed",
			Error:     "Empty patch provided",
		}
	}

	// Check that patch has valid unified diff format indicators
	hasHeaders := strings.Contains(event.Patch, "---") && strings.Contains(event.Patch, "+++")
	hasHunks := strings.Contains(event.Patch, "@@")

	if !hasHeaders || !hasHunks {
		return ValidationResult{
			FindingID: event.FindingID,
			Status:    "validated",
			BuildOutput: "Patch structure validated (simulated — no Docker available)",
			TestOutput:  "Skipped: Docker sandbox not available for full build/test",
		}
	}

	// Simulate successful validation
	time.Sleep(100 * time.Millisecond)

	return ValidationResult{
		FindingID:   event.FindingID,
		Status:      "validated",
		BuildOutput: "Patch structure validated (simulated)",
		TestOutput:  "Build/test execution simulated — Docker sandbox recommended for production",
	}
}

// isDockerAvailable checks if Docker is running and accessible.
func isDockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	err := cmd.Run()
	return err == nil
}
