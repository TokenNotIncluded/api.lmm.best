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

type SecurityStats struct {
	Moderation *ModerationSecurityStats `json:"moderation"`
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
	ID                int64                            `json:"id"`
	SourceKind        string                           `json:"source_kind"`
	Source            string                           `json:"source"`
	UserID            int                              `json:"user_id,omitempty"`
	RequestID         string                           `json:"request_id,omitempty"`
	Group             string                           `json:"group,omitempty"`
	ReviewModel       string                           `json:"review_model"`
	Mode              string                           `json:"mode"`
	Status            string                           `json:"status"`
	Attempts          int                              `json:"attempts"`
	InputTruncated    bool                             `json:"input_truncated"`
	Flagged           bool                             `json:"flagged"`
	Categories        []string                         `json:"categories"`
	ResponseModel     string                           `json:"response_model"`
	ReviewID          int64                            `json:"review_id,omitempty"`
	FeeRecordID       uint                             `json:"fee_record_id,omitempty"`
	FeeCategory       string                           `json:"fee_category,omitempty"`
	FeeStatus         string                           `json:"fee_status"`
	RequestedQuota    int                              `json:"requested_quota"`
	ChargedQuota      int                              `json:"charged_quota"`
	CreatedAt         int64                            `json:"created_at"`
	UpdatedAt         int64                            `json:"updated_at"`
	CompletedAt       int64                            `json:"completed_at"`
	SubjectIdentifier string                           `json:"subject_identifier,omitempty"`
	ProviderCalls     []SecurityModerationProviderCall `json:"provider_calls"`
}

type SecurityModerationProviderCall struct {
	Attempt    int    `json:"attempt"`
	BatchIndex int    `json:"batch_index"`
	ResponseID string `json:"response_id"`
	RequestID  string `json:"request_id"`
}
