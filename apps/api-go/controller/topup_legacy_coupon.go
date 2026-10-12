package controller

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
)

// topUpCouponAmountForCredits is the read adapter for existing coupon minimums.
// Both preview and checkout derive qualification from the same actual credit
// amount, never from resolvedTopUpAmount.LegacyBatch. Retain this adapter until
// persisted coupon terms have an audited, versioned unit migration.
func topUpCouponAmountForCredits(credits int64) (decimal.Decimal, error) {
	if credits <= 0 || credits > common.MaxWalletQuota || common.ValidateWalletQuota(int(credits)) != nil || !validQuotaPerUnit() {
		return decimal.Zero, errors.New("invalid coupon credit amount")
	}
	amount := decimal.NewFromInt(credits)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return amount, nil
	}
	return amount.Div(decimal.NewFromFloat(common.QuotaPerUnit)), nil
}
