package controller

import (
	"math"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

// Keep the wallet unit conversion server-side so a model cannot mistake raw
// quota or a zero-cost usage window for the account's USD wallet balance.
func assistantWalletBalanceFields(quota int) map[string]any {
	fields := map[string]any{
		"wallet_balance_quota":  quota,
		"quota_per_usd":         nil,
		"wallet_balance_usd":    nil,
		"wallet_balance_status": "unavailable",
		"wallet_balance_note":   "Wallet balance is separate from remaining subscription quota and usage totals. Zero used_quota or zero cost in a usage window does not mean the wallet balance is zero.",
	}
	if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
		return fields
	}
	balanceUSD := float64(quota) / common.QuotaPerUnit
	if math.IsNaN(balanceUSD) || math.IsInf(balanceUSD, 0) {
		return fields
	}
	fields["quota_per_usd"] = common.QuotaPerUnit
	fields["wallet_balance_status"] = "available"
	fields["wallet_balance_usd"] = balanceUSD
	return fields
}
