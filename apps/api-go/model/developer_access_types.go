package model

type AssistantDeveloperAccessGrant struct {
	Request        *DeveloperAccessRequest
	ConversationID int64
	CompletedTurns int
	Activated      bool
}

// DeveloperAccessRequest records the non-payment path to L1 access. The
// request is deliberately separate from User.TrustLevelOverride: approving a
// request unlocks L1 without freezing later paid progression at that level.
type DeveloperAccessRequest struct {
	Id               int    `json:"id" gorm:"primaryKey"`
	UserId           int    `json:"user_id" gorm:"not null;index"`
	Status           string `json:"status" gorm:"type:varchar(20);not null;index"`
	Source           string `json:"source" gorm:"type:varchar(40);not null;default:legacy;index"`
	Reason           string `json:"reason" gorm:"type:text"`
	AIRecommendation string `json:"ai_recommendation" gorm:"type:text"`
	AdminUserId      int    `json:"admin_user_id" gorm:"index"`
	AdminNote        string `json:"admin_note" gorm:"type:text"`
	CreatedAt        int64  `json:"created_at" gorm:"not null;index"`
	ReviewedAt       int64  `json:"reviewed_at" gorm:"not null;default:0"`
	Revision         int64  `json:"-" gorm:"not null;default:0"`
}

func (DeveloperAccessRequest) TableName() string { return "developer_access_requests" }

type DeveloperAccessRequestView struct {
	DeveloperAccessRequest
	Username string `json:"username"`
	Email    string `json:"email"`
}
