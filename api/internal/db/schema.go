package db

import (
	"time"

	"gorm.io/gorm"
)

// Organization represents a tenant in the system.
type Organization struct {
	gorm.Model
	Name     string    `gorm:"uniqueIndex;not null"`
	Projects []Project `gorm:"foreignKey:OrgID"`
	Users    []User    `gorm:"foreignKey:OrgID"`
}

// User represents an individual user.
type User struct {
	gorm.Model
	OrgID    uint
	Email    string `gorm:"uniqueIndex;not null"`
	Name     string
	Role     string // e.g., admin, member
}

// Project represents a repository being analyzed.
type Project struct {
	gorm.Model
	OrgID         uint
	ProjectHash   string `gorm:"uniqueIndex"`
	Name          string `gorm:"not null"`
	RepositoryURL string `gorm:"not null"`
	DefaultBranch string `gorm:"default:'main'"`
	Scans         []Scan `gorm:"foreignKey:ProjectID"`
}

// Scan represents a single security analysis run.
type Scan struct {
	gorm.Model
	ProjectID uint
	Status    string    // e.g., queued, running, completed, failed
	StartTime time.Time
	EndTime   *time.Time
	Findings  []Finding `gorm:"foreignKey:ScanID"`
}

// Finding represents a security vulnerability or code issue detected in a scan.
type Finding struct {
	gorm.Model
	ScanID         *uint   `json:"scan_id"`
	ProjectHash    string  `json:"project_id"`
	Title          string  `json:"title"`
	Category       string  `json:"category"`
	RuleID         string  `gorm:"not null" json:"rule_id"`
	Severity       string  `gorm:"not null" json:"severity"`
	Description    string  `gorm:"type:text" json:"description"`
	FilePath       string  `gorm:"not null" json:"file_path"`
	LineStart      int     `json:"line_start"`
	LineEnd        int     `json:"line_end"`
	Status         string  `gorm:"default:'open'" json:"status"`
	Remediation    string  `gorm:"type:text" json:"remediation"`
	HasFix         bool    `gorm:"default:false" json:"has_fix"`
	// Extended fields for Phase 2
	DiffPatch      string  `gorm:"type:text" json:"diff_patch"`
	RootCause      string  `gorm:"type:text" json:"root_cause"`
	Impact         string  `gorm:"type:text" json:"impact"`
	Confidence     float64 `json:"confidence"`
	CweID          string  `json:"cwe_id"`
	CvssScore      float64 `json:"cvss_score"`
	Source         string  `json:"source"` // regex, llm
	CodeSnippet    string  `gorm:"type:text" json:"code_snippet"`
}

// FixApproval tracks the approval/rejection of auto-generated fixes.
type FixApproval struct {
	gorm.Model
	FindingID  uint   `json:"finding_id"`
	ApprovedBy string `json:"approved_by"`
	Action     string `json:"action"` // approve, reject, defer
	Comment    string `gorm:"type:text" json:"comment"`
}

// AgentRun represents a single agent execution within a scan.
type AgentRun struct {
	gorm.Model
	ScanID        uint   `json:"scan_id"`
	AgentName     string `json:"agent_name"`
	ModelUsed     string `json:"model_used"`
	Status        string `gorm:"default:'running'" json:"status"`
	TokensIn      int    `json:"tokens_in"`
	TokensOut     int    `json:"tokens_out"`
	DurationMs    int    `json:"duration_ms"`
	ErrorMessage  string `gorm:"type:text" json:"error_message"`
	StartedAt     time.Time
	CompletedAt   *time.Time
}
