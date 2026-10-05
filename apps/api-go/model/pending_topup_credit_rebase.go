package model

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
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
