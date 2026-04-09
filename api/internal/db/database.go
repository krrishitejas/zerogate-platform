package db

import (
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func ConnectPostgres(dsn string) error {
	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return err
	}

	err = DB.AutoMigrate(
		&Organization{},
		&User{},
		&Project{},
		&Scan{},
		&Finding{},
		&FixApproval{},
		&AuditLog{},
		&AgentRun{},
	)
	if err != nil {
		return err
	}

	DB.Exec("ALTER TABLE findings DROP CONSTRAINT IF EXISTS fk_scans_findings")
	DB.Exec("ALTER TABLE scans DROP CONSTRAINT IF EXISTS fk_projects_scans")

	log.Println("Database schemas migrated successfully.")
	return nil
}

func SaveFinding(f *Finding) error {
	if DB == nil {
		return nil
	}
	return DB.Create(f).Error
}

func UpdateFindingRemediation(findingID uint, patch string) error {
	if DB == nil {
		return nil
	}
	return DB.Model(&Finding{}).Where("id = ?", findingID).Updates(map[string]interface{}{
		"remediation": patch,
		"diff_patch":  patch,
		"has_fix":     true,
	}).Error
}

func UpdateFindingStatus(findingID uint, status string) error {
	if DB == nil {
		return nil
	}
	return DB.Model(&Finding{}).Where("id = ?", findingID).Update("status", status).Error
}

// GetFindingByID retrieves a single finding by its ID.
func GetFindingByID(findingID uint) (*Finding, error) {
	if DB == nil {
		return nil, nil
	}
	var finding Finding
	err := DB.First(&finding, findingID).Error
	if err != nil {
		return nil, err
	}
	return &finding, nil
}

// GetFindingApprovals retrieves all approval records for a finding.
func GetFindingApprovals(findingID uint) ([]FixApproval, error) {
	if DB == nil {
		return nil, nil
	}
	var approvals []FixApproval
	err := DB.Where("finding_id = ?", findingID).Order("created_at desc").Find(&approvals).Error
	return approvals, err
}

// CreateFixApproval creates a new fix approval record and updates finding status.
func CreateFixApproval(approval *FixApproval) error {
	if DB == nil {
		return nil
	}
	if err := DB.Create(approval).Error; err != nil {
		return err
	}

	// Update finding status based on action
	var newStatus string
	switch approval.Action {
	case "approve":
		newStatus = "fixed"
	case "reject":
		newStatus = "open"
	case "defer":
		newStatus = "deferred"
	default:
		newStatus = "open"
	}

	return UpdateFindingStatus(approval.FindingID, newStatus)
}

// GetPendingFixes returns findings that have auto-generated fixes awaiting approval.
func GetPendingFixes(projectID string) ([]Finding, error) {
	if DB == nil {
		return nil, nil
	}
	var findings []Finding
	query := DB.Where("has_fix = ? AND status IN ?", true, []string{"open", "validated_patch"}).Order("created_at desc")
	if projectID != "" {
		query = query.Where("project_hash = ?", projectID)
	}
	err := query.Find(&findings).Error
	return findings, err
}

// GetFindingStats returns counts of findings by severity for a project.
func GetFindingStats(projectID string) (map[string]int64, error) {
	if DB == nil {
		return nil, nil
	}
	stats := map[string]int64{
		"critical": 0,
		"high":     0,
		"medium":   0,
		"low":      0,
		"total":    0,
	}

	var results []struct {
		Severity string
		Count    int64
	}

	query := DB.Model(&Finding{}).Select("severity, count(*) as count").Group("severity")
	if projectID != "" {
		query = query.Where("project_hash = ?", projectID)
	}
	if err := query.Scan(&results).Error; err != nil {
		return stats, err
	}

	for _, r := range results {
		stats[r.Severity] = r.Count
		stats["total"] += r.Count
	}

	return stats, nil
}
