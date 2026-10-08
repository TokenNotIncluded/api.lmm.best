package model

import "errors"

var ErrMerchantStoreRefundUnsupported = errors.New("verified refund amount is unavailable for this payment method")

type MerchantStoreRefund struct {
	ID                      string   `json:"id" gorm:"primaryKey;size:64"`
	OrderID                 string   `json:"order_id" gorm:"size:64;not null;index"`
	RequestKey              string   `json:"request_key" gorm:"size:64;not null"`
	InputDigest             string   `json:"-" gorm:"size:64;not null"`
	Mode                    string   `json:"mode" gorm:"size:16;not null"`
	Quantity                int      `json:"quantity"`
	StockIDs                []string `json:"stock_ids" gorm:"-"`
	PrincipalQuota          int      `json:"amount_quota" gorm:"type:bigint;not null"`
	AmountMinor             int64    `json:"amount_minor" gorm:"type:bigint"`
	Currency                string   `json:"currency" gorm:"size:16"`
	Reason                  string   `json:"reason" gorm:"type:text"`
	Status                  string   `json:"status" gorm:"size:32;not null;index"`
	RequestedBy             int      `json:"requested_by"`
	RequestedRole           string   `json:"requested_role" gorm:"size:16"`
	DecisionBy              int      `json:"decision_by"`
	DecisionReason          string   `json:"decision_reason" gorm:"type:text"`
	RetainedFeeQuota        int      `json:"retained_fee_quota" gorm:"type:bigint"`
	CreatedAt               int64    `json:"created_at"`
	DecidedAt               int64    `json:"decided_at"`
	CompletedAt             int64    `json:"completed_at"`
	ProviderRefundReference *string  `json:"-" gorm:"size:128;uniqueIndex"`
	ProviderEvidenceHash    string   `json:"-" gorm:"size:64"`
}

type MerchantStoreRefundItem struct {
	ID        string `json:"-" gorm:"primaryKey;size:100"`
	RefundID  string `json:"-" gorm:"size:64;not null;index"`
	OrderID   string `json:"-" gorm:"size:64;not null;index"`
	StockID   string `json:"-" gorm:"size:36;not null;index"`
	CreatedAt int64  `json:"-"`
}

// This is the original *charged* native principal attested by a trusted payment
// adapter. The checkout quote (which may exclude tax) is never substituted.
type MerchantStoreRefundPaymentBasis struct {
	OrderID          string `json:"-" gorm:"primaryKey;size:64"`
	ReceiptReference string `json:"-" gorm:"size:128;not null"`
	PaymentReference string `json:"-" gorm:"size:128;not null"`
	AmountMinor      int64  `json:"-" gorm:"type:bigint;not null"`
	Currency         string `json:"-" gorm:"size:16;not null"`
	EvidenceHash     string `json:"-" gorm:"size:64;not null"`
	CreatedAt        int64  `json:"-"`
}

type MerchantStoreRefundInput struct {
	RequestKey  string   `json:"request_key"`
	Reason      string   `json:"reason"`
	Mode        string   `json:"mode"`
	Quantity    int      `json:"quantity,omitempty"`
	StockIDs    []string `json:"stock_ids,omitempty"`
	AmountQuota int      `json:"amount_quota,omitempty"`
	AmountMinor int64    `json:"amount_minor,omitempty"`
}
type MerchantStoreRefundDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}
type MerchantStoreRefundPickupProof struct {
	OrderID string `json:"order_id"`
	Token   string `json:"token"`
	Code    string `json:"code,omitempty"`
}
type MerchantStoreRefundEligibleItem struct {
	StockID  string `json:"stock_id"`
	Position int64  `json:"position"`
}
type MerchantStoreRefundView struct {
	DeliveryTemplate         string                            `json:"delivery_template"`
	ExternalRedemptionStatus string                            `json:"external_redemption_status,omitempty"`
	OrderID                  string                            `json:"order_id"`
	ProductTitle             string                            `json:"product_title"`
	VariantName              string                            `json:"variant_name"`
	PaymentMethod            string                            `json:"payment_method"`
	Currency                 string                            `json:"currency"`
	PrincipalQuota           int                               `json:"principal_quota"`
	RefundedQuota            int                               `json:"refunded_quota"`
	ReservedQuota            int                               `json:"reserved_quota"`
	RemainingQuota           int                               `json:"remaining_quota"`
	Quantity                 int                               `json:"quantity"`
	RefundedQuantity         int64                             `json:"refunded_quantity"`
	EligibleItems            []MerchantStoreRefundEligibleItem `json:"eligible_items"`
	MaxQuantity              int                               `json:"max_quantity"`
	SupportsQuantity         bool                              `json:"supports_quantity"`
	SupportsAmount           bool                              `json:"supports_amount"`
	NativeBasisVerified      bool                              `json:"native_basis_verified"`
	AmountMinor              *int64                            `json:"amount_minor,omitempty"`
	RefundedAmountMinor      *int64                            `json:"refunded_amount_minor,omitempty"`
	RemainingAmountMinor     *int64                            `json:"remaining_amount_minor,omitempty"`
	Refunds                  []MerchantStoreRefund             `json:"refunds"`
}
