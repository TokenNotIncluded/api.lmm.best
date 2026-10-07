package model

const (
	AdvancedSecurityDecisionBlocked = "blocked"
	AdvancedSecurityDecisionAudited = "audited"
)

// AdvancedSecurityEvent is a structured audit row for one matched rule. It
// intentionally stores digests instead of prompt text or matcher patterns.
// The table lives in the primary database so it remains available even when
// LOG_SQL_DSN points at a ClickHouse/isolated log database.
type AdvancedSecurityEvent struct {
	ID            uint   `json:"id" gorm:"primaryKey"`
	CreatedAt     int64  `json:"created_at" gorm:"index"`
	RequestID     string `json:"request_id" gorm:"index"`
	UserID        int    `json:"user_id" gorm:"index"`
	Username      string `json:"username" gorm:"index"`
	TokenID       int    `json:"token_id" gorm:"index"`
	ChannelID     int    `json:"channel_id" gorm:"index"`
	ModelName     string `json:"model_name" gorm:"index"`
	Group         string `json:"group" gorm:"index"`
	Endpoint      string `json:"endpoint"`
	Decision      string `json:"decision" gorm:"index"`
	RuleID        string `json:"rule_id" gorm:"index"`
	RuleName      string `json:"rule_name"`
	Category      string `json:"category" gorm:"index"`
	Layer         string `json:"layer" gorm:"index"`
	Severity      string `json:"severity" gorm:"index"`
	Source        string `json:"source"`
	RuleVersion   string `json:"rule_version"`
	PatternDigest string `json:"pattern_digest"`
	InputDigest   string `json:"input_digest"`
	MatchCount    int    `json:"match_count"`
}
