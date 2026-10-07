package model

// AssistantSecurityReviewNotice retains the retired table definition for the
// existing schema contract; no runtime reports read or write this table.
type AssistantSecurityReviewNotice struct {
	ID                int64  `json:"id" gorm:"primaryKey"`
	TaskID            string `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	WindowStart       int64  `json:"window_start" gorm:"not null;index"`
	WindowEnd         int64  `json:"window_end" gorm:"not null;index"`
	TotalMatches      int64  `json:"total_matches" gorm:"not null"`
	BlockedMatches    int64  `json:"blocked_matches" gorm:"not null"`
	AuditedMatches    int64  `json:"audited_matches" gorm:"not null"`
	AffectedRequests  int64  `json:"affected_requests" gorm:"not null"`
	AffectedUsers     int64  `json:"affected_users" gorm:"not null"`
	ByCategoryJSON    string `json:"-" gorm:"type:text;not null"`
	ByRuleJSON        string `json:"-" gorm:"type:text;not null"`
	ErrorLogCount     int64  `json:"error_log_count" gorm:"not null;default:0"`
	ErrorChannelsJSON string `json:"-" gorm:"type:text;not null;default:''"`
	ErrorModelsJSON   string `json:"-" gorm:"type:text;not null;default:''"`
	CreatedAt         int64  `json:"created_at" gorm:"not null;index"`
	UpdatedAt         int64  `json:"updated_at" gorm:"not null;index"`
}

func (AssistantSecurityReviewNotice) TableName() string { return "assistant_security_review_notices" }
