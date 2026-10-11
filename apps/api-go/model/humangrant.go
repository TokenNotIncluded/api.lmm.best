package model

// HumanGrantRecord stores encrypted-link metadata, consent, and local mail state.
// All access is scoped through the HumanGrant service; application endpoints
// never interpret this JSON as a human login or a billing permission.
type HumanGrantRecord struct {
	Kind      string `gorm:"primaryKey;size:32"`
	ID        string `gorm:"primaryKey;size:64"`
	Owner     string `gorm:"size:80;index:idx_hg_owner"`
	ExpiresAt int64  `gorm:"index:idx_hg_expiry"`
	Payload   string `gorm:"type:text;not null"`
}

func (HumanGrantRecord) TableName() string { return "human_grant_records" }

// HumanGrantGate serializes grants, revocations, settings, and claims across nodes.
type HumanGrantGate struct {
	ID       int `gorm:"primaryKey;autoIncrement:false"`
	Revision int64
}

func (HumanGrantGate) TableName() string { return "human_grant_gate" }
