package dto

// SecurityRiskCategory is safe to return to unauthenticated users. It does
// not contain matcher patterns or administrator-only configuration.
type SecurityRiskCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Layer       string `json:"layer"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

type SecurityRuleSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Layer       string `json:"layer"`
	Severity    string `json:"severity"`
	Source      string `json:"source"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Historical  bool   `json:"historical,omitempty"`
}

type SecurityViolationFeeRule struct {
	Code              string    `json:"code"`
	Provider          string    `json:"provider,omitempty"` // deprecated; policy is no longer provider-specific
	Groups            []string  `json:"groups,omitempty"`
	Trigger           string    `json:"trigger"`
	Enabled           bool      `json:"enabled"`
	AmountUSD         float64   `json:"amount_usd"`
	AmountsUSD        []float64 `json:"amounts_usd,omitempty"`
	Multiplier        float64   `json:"multiplier,omitempty"`
	MaxAmountUSD      float64   `json:"max_amount_usd,omitempty"`
	PeriodSeconds     int64     `json:"period_seconds,omitempty"`
	ChargeUnit        string    `json:"charge_unit"`
	Retryable         bool      `json:"retryable"`
	Description       string    `json:"description"`
	ChargingNotes     string    `json:"charging_notes"`
	LocalGuardrailFee bool      `json:"local_guardrail_fee"`
	Historical        bool      `json:"historical,omitempty"`
}

type SecurityViolationFeePolicy struct {
	Name                  string    `json:"name,omitempty"`
	Groups                []string  `json:"groups"`
	Enabled               bool      `json:"enabled"`
	AmountsUSD            []float64 `json:"amounts_usd,omitempty"`
	InitialAmountUSD      float64   `json:"initial_amount_usd"`
	Multiplier            float64   `json:"multiplier"`
	MaxAmountUSD          float64   `json:"max_amount_usd"`
	PeriodSeconds         int64     `json:"period_seconds"`
	DrainBalanceWhenShort bool      `json:"drain_balance_when_short"`
}

type SecurityViolationFeeSettings struct {
	Enabled  bool                         `json:"enabled"`
	Policies []SecurityViolationFeePolicy `json:"policies"`
}

type PublicSecurityPolicy struct {
	PolicyVersion          string           `json:"policy_version"`
	ReferenceEffectiveDate string           `json:"reference_effective_date"`
	ReferenceURL           string           `json:"reference_url"`
	Alignment              string           `json:"alignment"`
	Enforcement            SecuritySettings `json:"enforcement"`
	// ProtectedGroups is the explicit union of groups covered by enabled
	// rules.  It is safe to publish because it contains no matcher text or
	// request/user data and makes the group boundary visible to operators.
	ProtectedGroups []string                   `json:"protected_groups"`
	RiskCategories  []SecurityRiskCategory     `json:"risk_categories"`
	Rules           []SecurityRuleSummary      `json:"rules"`
	ViolationFees   []SecurityViolationFeeRule `json:"violation_fees"`
	Moderation      SecurityModerationPolicy   `json:"moderation"`
}

// SecurityModerationPolicy publishes account-group rules only. Review routing
// groups, channel metadata and credentials remain administrator-only.
type SecurityModerationPolicy struct {
	Enabled          bool                                     `json:"enabled"`
	AssistantEnabled bool                                     `json:"assistant_enabled"`
	Engine           string                                   `json:"engine"`
	Async            bool                                     `json:"async"`
	GroupPolicies    map[string]SecurityModerationGroupPolicy `json:"group_policies"`
	SupportedInputs  []string                                 `json:"supported_inputs"`
	// NoticeOnly describes in-site delivery, not whether a strict policy fines.
	NoticeOnly bool `json:"notice_only"`
}

type SecurityModerationGroupPolicy struct {
	Mode             string             `json:"mode"`
	AmountCurrency   string             `json:"amount_currency"`
	CategoryFinesUSD map[string]float64 `json:"category_fines_usd"`
}

type SecuritySettings struct {
	Enabled  bool   `json:"enabled"`
	OnPrompt bool   `json:"on_prompt"`
	Action   string `json:"action"`
	Retired  bool   `json:"retired,omitempty"`
}

type SecurityAdminRule struct {
	SecurityRuleSummary
	Enabled  bool     `json:"enabled"`
	Groups   []string `json:"groups"`
	Patterns []string `json:"patterns"`
}

type AdminSecurityPolicy struct {
	Public       PublicSecurityPolicy         `json:"public"`
	Settings     SecuritySettings             `json:"settings"`
	Rules        []SecurityAdminRule          `json:"rules"`
	ViolationFee SecurityViolationFeeSettings `json:"violation_fee"`
}

type SecurityStatBucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

type SecurityStats struct {
	StartTimestamp   int64                  `json:"start_timestamp"`
	EndTimestamp     int64                  `json:"end_timestamp"`
	TotalMatches     int64                  `json:"total_matches"`
	BlockedMatches   int64                  `json:"blocked_matches"`
	AuditedMatches   int64                  `json:"audited_matches"`
	AffectedRequests int64                  `json:"affected_requests"`
	AffectedUsers    int64                  `json:"affected_users"`
	ByCategory       []SecurityStatBucket   `json:"by_category"`
	ByRule           []SecurityStatBucket   `json:"by_rule,omitempty"`
	AIReview         *AISecurityReviewStats `json:"ai_review,omitempty"`
	// Moderation is an all-time queue aggregate, independent of the legacy
	// event window above. No user, group or request metadata is public here.
	Moderation *ModerationSecurityStats `json:"moderation,omitempty"`
}

type ModerationSecurityStats struct {
	Pending      int64 `json:"pending"`
	Running      int64 `json:"running"`
	Completed    int64 `json:"completed"`
	Failed       int64 `json:"failed"`
	Cancelled    int64 `json:"cancelled"`
	Flagged      int64 `json:"flagged"`
	Fined        int64 `json:"fined"`
	ChargedQuota int64 `json:"charged_quota"`
}

// SecurityModerationReview is a bounded administrative projection. It never
// includes submitted text, provider bodies, leases or captured policy data.
type SecurityModerationReview struct {
	ID             int64    `json:"id"`
	SourceKind     string   `json:"source_kind"`
	Source         string   `json:"source"`
	UserID         int      `json:"user_id,omitempty"`
	RequestID      string   `json:"request_id,omitempty"`
	Group          string   `json:"group,omitempty"`
	ReviewModel    string   `json:"review_model"`
	Mode           string   `json:"mode"`
	Status         string   `json:"status"`
	Attempts       int      `json:"attempts"`
	InputTruncated bool     `json:"input_truncated"`
	Flagged        bool     `json:"flagged"`
	Categories     []string `json:"categories"`
	ResponseModel  string   `json:"response_model"`
	ReviewID       int64    `json:"review_id,omitempty"`
	FeeRecordID    uint     `json:"fee_record_id,omitempty"`
	FeeCategory    string   `json:"fee_category,omitempty"`
	FeeStatus      string   `json:"fee_status"`
	RequestedQuota int      `json:"requested_quota"`
	ChargedQuota   int      `json:"charged_quota"`
	CreatedAt      int64    `json:"created_at"`
	UpdatedAt      int64    `json:"updated_at"`
	CompletedAt    int64    `json:"completed_at"`
}

// AISecurityReviewStats summarizes the asynchronous assistant review lane.
// It is deliberately separate from literal-rule match counts so the two
// detection mechanisms are not presented as if they were the same signal.
type AISecurityReviewStats struct {
	Total      int64                `json:"total"`
	Completed  int64                `json:"completed"`
	Violations int64                `json:"violations"`
	Abuses     int64                `json:"abuses"`
	Failed     int64                `json:"failed"`
	ByGroup    []SecurityStatBucket `json:"by_group,omitempty"`
}

type AdvancedSecurityEvent struct {
	ID            uint   `json:"id"`
	CreatedAt     int64  `json:"created_at"`
	RequestID     string `json:"request_id"`
	UserID        int    `json:"user_id"`
	Username      string `json:"username"`
	TokenID       int    `json:"token_id"`
	ChannelID     int    `json:"channel_id"`
	ModelName     string `json:"model_name"`
	Group         string `json:"group"`
	Endpoint      string `json:"endpoint"`
	Decision      string `json:"decision"`
	RuleID        string `json:"rule_id"`
	RuleName      string `json:"rule_name"`
	Category      string `json:"category"`
	Layer         string `json:"layer"`
	Severity      string `json:"severity"`
	Source        string `json:"source"`
	RuleVersion   string `json:"rule_version"`
	PatternDigest string `json:"pattern_digest"`
	InputDigest   string `json:"input_digest"`
	MatchCount    int    `json:"match_count"`
}

// AdvancedSecurityAIReview is a safe, administrator-facing projection of an
// asynchronous AI review.  It intentionally omits request/response previews;
// the explanation and rule labels are already bounded and redacted at write
// time, while raw conversation content belongs in the separate history ACL.
type AdvancedSecurityAIReview struct {
	ID          int64    `json:"id"`
	CreatedAt   int64    `json:"created_at"`
	RequestID   string   `json:"request_id,omitempty"`
	UserID      int      `json:"user_id,omitempty"`
	Group       string   `json:"group"`
	ReviewModel string   `json:"review_model"`
	Intensity   string   `json:"intensity"`
	Status      string   `json:"status"`
	Violation   bool     `json:"violation"`
	Abuse       bool     `json:"abuse"`
	Rules       []string `json:"rules,omitempty"`
	Explanation string   `json:"explanation,omitempty"`
}
