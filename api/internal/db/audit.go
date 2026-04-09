package db

// AuditLog represents a single action performed on the platform, enabling SOC2 compliance.
type AuditLog struct {
	ID         uint   `gorm:"primarykey"`
	UserID     string `json:"user_id"`
	UserEmail  string `json:"user_email"`
	OrgID      string `gorm:"index" json:"org_id"`
	Action     string `gorm:"index" json:"action"`
	Resource   string `json:"resource"`
	ResourceID string `json:"resource_id"`
	IPAddress  string `json:"ip_address"`
	UserAgent  string `json:"user_agent"`
	Metadata   string `gorm:"type:jsonb" json:"metadata"` // For PG JSONB support
	CreatedAt  int64  `gorm:"autoCreateTime" json:"created_at"`
}

// LogAuditEvent creates an immutable audit trail entry.
func LogAuditEvent(log *AuditLog) error {
	if DB == nil {
		return nil
	}
	return DB.Create(log).Error
}

// GetAuditLogs retrieves audit logs for an organization.
func GetAuditLogs(orgID string, limit, offset int) ([]AuditLog, error) {
	if DB == nil {
		return nil, nil
	}
	var logs []AuditLog
	query := DB.Where("org_id = ?", orgID).Order("created_at desc")
	
	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}
	
	err := query.Find(&logs).Error
	return logs, err
}
