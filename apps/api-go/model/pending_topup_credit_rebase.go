package model

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// pendingTopUpSettlementQuota uses only an order-specific migration snapshot.
// New orders have no snapshot and retain their quoted credits. The original
// monetary quote and payment evidence never change; CreditedQuota becomes the
// actual effective grant when the pending order completes, which also gives
// subsequent refunds the correct current-unit basis.
func pendingTopUpSettlementQuota(topUp *TopUp) (int64, error) {
	if topUp == nil {
		return 0, ErrInvalidTopUpQuota
	}
	quota := normalizedTopUpCreditedQuota(topUp)
	if topUp.PendingCreditRebaseKey == "" {
		if topUp.PendingCreditRebaseOriginalQuota != 0 || topUp.PendingCreditRebaseEffectiveQuota != 0 {
			return 0, ErrInvalidTopUpQuota
		}
	} else {
		if strings.TrimSpace(topUp.PendingCreditRebaseKey) == "" || topUp.Status == common.TopUpStatusSuccess ||
			quota != topUp.PendingCreditRebaseOriginalQuota ||
			topUp.PendingCreditRebaseEffectiveQuota <= 0 ||
			topUp.PendingCreditRebaseEffectiveQuota > topUp.PendingCreditRebaseOriginalQuota {
			return 0, ErrInvalidTopUpQuota
		}
		quota = topUp.PendingCreditRebaseEffectiveQuota
	}
	if quota <= 0 || quota > int64(common.MaxWalletQuota) {
		return 0, ErrInvalidTopUpQuota
	}
	return quota, nil
}

func pendingTopUpSettlementQuotaInt(topUp *TopUp) (int, error) {
	quota, err := pendingTopUpSettlementQuota(topUp)
	if err != nil {
		return 0, err
	}
	value := int(quota)
	if int64(value) != quota || common.ValidateWalletQuota(value) != nil {
		return 0, ErrInvalidTopUpQuota
	}
	return value, nil
}

// The pure helper above remains usable by read-only snapshot exporters. A
// settlement must also bind the order metadata to its durable migration plan;
// losing all three fields must never turn an old quote into a new order.
func pendingTopUpSettlementQuotaTx(tx *gorm.DB, topUp *TopUp) (int64, error) {
	if tx == nil || topUp == nil || topUp.Id <= 0 || topUp.UserId <= 0 {
		return 0, ErrInvalidTopUpQuota
	}
	quota, err := pendingTopUpSettlementQuota(topUp)
	if err != nil {
		return 0, err
	}
	exists, err := walletCreditAuditTableExists(tx, "wallet_credit_rebases")
	if err != nil {
		return 0, err
	}
	if !exists {
		if topUp.PendingCreditRebaseKey != "" {
			return 0, ErrInvalidTopUpQuota
		}
		return quota, nil
	}
	var audits []struct {
		MigrationID string `gorm:"column:migration_id"`
		Plan        string
	}
	if err := lockForUpdate(tx.Table("wallet_credit_rebases")).Select("migration_id, plan").Find(&audits).Error; err != nil {
		return 0, err
	}
	matched := false
	for _, audit := range audits {
		var plan struct {
			MigrationID    string                    `json:"migration_id"`
			IncludePending bool                      `json:"include_pending_topups"`
			Bases          *[]pendingTopUpCreditBase `json:"pending_bases"`
			Blocked        []struct {
				pendingTopUpCreditBase
				Reason           string `json:"reason"`
				FutureSettlement string `json:"future_settlement"`
			} `json:"blocked_pending_bases"`
		}
		if json.Unmarshal([]byte(audit.Plan), &plan) != nil || audit.MigrationID == "" || plan.MigrationID != audit.MigrationID {
			return 0, ErrInvalidTopUpQuota
		}
		if plan.IncludePending && plan.Bases == nil {
			return 0, ErrInvalidTopUpQuota
		}
		for _, blocked := range plan.Blocked {
			if blocked.TopUpID <= 0 || blocked.UserID <= 0 || blocked.Source.ID != blocked.TopUpID ||
				blocked.Source.UserID != blocked.UserID || blocked.OriginalQuota != 0 || blocked.EffectiveQuota != 0 ||
				blocked.Source.EffectiveQuota != 0 || blocked.FutureSettlement != "blocked_until_separate_audited_payment_reconciliation" ||
				(blocked.Reason != "legacy_noncash_without_immutable_grant" && blocked.Reason != "authority_zero_not_settleable") {
				return 0, ErrInvalidTopUpQuota
			}
			if blocked.TopUpID == topUp.Id {
				return 0, ErrInvalidTopUpQuota
			}
		}
		var bases []pendingTopUpCreditBase
		if plan.Bases != nil {
			bases = *plan.Bases
		}
		for _, base := range bases {
			if !base.valid() {
				return 0, ErrInvalidTopUpQuota
			}
			if base.TopUpID != topUp.Id {
				continue
			}
			if matched || topUp.PendingCreditRebaseKey != audit.MigrationID || base.UserID != topUp.UserId ||
				topUp.PendingCreditRebaseOriginalQuota != base.OriginalQuota || topUp.PendingCreditRebaseEffectiveQuota != base.EffectiveQuota ||
				!base.matchesSource(topUp) {
				return 0, ErrInvalidTopUpQuota
			}
			matched = true
		}
	}
	if topUp.PendingCreditRebaseKey != "" && !matched {
		return 0, ErrInvalidTopUpQuota
	}
	return quota, nil
}

func pendingTopUpSettlementQuotaIntTx(tx *gorm.DB, topUp *TopUp) (int, error) {
	quota, err := pendingTopUpSettlementQuotaTx(tx, topUp)
	if err != nil {
		return 0, err
	}
	value := int(quota)
	if int64(value) != quota || common.ValidateWalletQuota(value) != nil {
		return 0, ErrInvalidTopUpQuota
	}
	return value, nil
}

type pendingTopUpCreditBase struct {
	TopUpID        int   `json:"top_up_id"`
	UserID         int   `json:"user_id"`
	OriginalQuota  int64 `json:"original_credited_quota"`
	EffectiveQuota int64 `json:"effective_credited_quota"`
	Source         struct {
		ID                           int     `json:"id"`
		UserID                       int     `json:"user_id"`
		EffectiveQuota               int64   `json:"effective_credited_quota"`
		CreditedQuota                *int64  `json:"credited_quota"`
		Amount                       *int64  `json:"amount"`
		PlatformAmountMicros         *int64  `json:"platform_amount_micros"`
		ExpectedAmountMicros         *int64  `json:"expected_amount_micros"`
		SettledAmountMicros          *int64  `json:"settled_amount_micros"`
		RefundedQuota                *int64  `json:"refunded_quota"`
		RefundedAmountMicros         *int64  `json:"refunded_amount_micros"`
		PaymentProvider              *string `json:"payment_provider"`
		PaymentMethod                *string `json:"payment_method"`
		SettlementCurrency           *string `json:"settlement_currency"`
		Money                        *string `json:"money"`
		Status                       string  `json:"status"`
		FailureReasonCode            string  `json:"failure_reason_code"`
		PendingCreditRebaseKey       *string `json:"pending_credit_rebase_key"`
		PendingCreditRebaseOriginal  *int64  `json:"pending_credit_rebase_original_quota"`
		PendingCreditRebaseEffective *int64  `json:"pending_credit_rebase_effective_quota"`
	} `json:"source"`
}

func pendingTopUpRecoverable(status, provider, failureReason string) bool {
	return status == common.TopUpStatusPending || (status == common.TopUpStatusFailed &&
		provider == PaymentProviderWaffoPancake && failureReason == string(PaymentOrderFailureCheckoutTimeout))
}

func (base *pendingTopUpCreditBase) valid() bool {
	source := &base.Source
	if base.TopUpID <= 0 || base.UserID <= 0 || source.ID != base.TopUpID || source.UserID != base.UserID ||
		base.OriginalQuota <= 0 || source.EffectiveQuota != base.OriginalQuota || base.EffectiveQuota <= 0 ||
		base.EffectiveQuota > base.OriginalQuota || source.CreditedQuota == nil || source.Amount == nil ||
		source.PlatformAmountMicros == nil || source.ExpectedAmountMicros == nil || source.SettledAmountMicros == nil ||
		source.RefundedQuota == nil || source.RefundedAmountMicros == nil || source.PaymentProvider == nil ||
		source.PaymentMethod == nil || source.SettlementCurrency == nil || source.Money == nil ||
		source.PendingCreditRebaseKey == nil || source.PendingCreditRebaseOriginal == nil || source.PendingCreditRebaseEffective == nil ||
		*source.PendingCreditRebaseKey != "" || *source.PendingCreditRebaseOriginal != 0 || *source.PendingCreditRebaseEffective != 0 ||
		!pendingTopUpRecoverable(source.Status, *source.PaymentProvider, source.FailureReasonCode) {
		return false
	}
	money, err := strconv.ParseFloat(*source.Money, 64)
	if err != nil || math.IsNaN(money) || math.IsInf(money, 0) || money < 0 || *source.CreditedQuota < 0 ||
		*source.Amount < 0 || *source.PlatformAmountMicros < 0 || *source.ExpectedAmountMicros < 0 ||
		*source.SettledAmountMicros < 0 || *source.RefundedQuota < 0 || *source.RefundedAmountMicros < 0 {
		return false
	}
	quoted := TopUp{PaymentProvider: *source.PaymentProvider, PaymentMethod: *source.PaymentMethod,
		CreditedQuota: *source.CreditedQuota, Amount: *source.Amount, PlatformAmountMicros: *source.PlatformAmountMicros,
		ExpectedAmountMicros: *source.ExpectedAmountMicros, SettledAmountMicros: *source.SettledAmountMicros,
		SettlementCurrency: *source.SettlementCurrency}
	return normalizedTopUpCreditedQuota(&quoted) == base.OriginalQuota
}

func (base *pendingTopUpCreditBase) matchesSource(topUp *TopUp) bool {
	source := &base.Source
	money, err := strconv.ParseFloat(*source.Money, 64)
	stateMatches := (source.Status == topUp.Status && source.FailureReasonCode == topUp.FailureReasonCode) ||
		(source.Status == common.TopUpStatusPending && topUp.Status == common.TopUpStatusFailed &&
			topUp.PaymentProvider == PaymentProviderWaffoPancake && topUp.FailureReasonCode == string(PaymentOrderFailureCheckoutTimeout))
	return err == nil && money == topUp.Money && *source.PaymentProvider == topUp.PaymentProvider &&
		*source.PaymentMethod == topUp.PaymentMethod &&
		*source.CreditedQuota == topUp.CreditedQuota && *source.Amount == topUp.Amount &&
		*source.PlatformAmountMicros == topUp.PlatformAmountMicros && *source.ExpectedAmountMicros == topUp.ExpectedAmountMicros &&
		*source.SettledAmountMicros == topUp.SettledAmountMicros && *source.RefundedQuota == topUp.RefundedQuota &&
		*source.RefundedAmountMicros == topUp.RefundedAmountMicros && *source.SettlementCurrency == topUp.SettlementCurrency &&
		stateMatches
	// The Epay handler validates the original quote before replacing its
	// method. A pending Waffo quote may time out after the migration snapshot.
}
