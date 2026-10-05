package controller

import "github.com/LIghtJUNction/api.lmm.best/common"

// Keep the wallet unit conversion server-side so a model cannot mistake raw
// quota or a zero-cost usage window for the account's USD wallet balance.
func assistantWalletBalanceFields(quota int) map[string]any {
	fields := map[string]any{
		"wallet_balance_quota":  quota,
		"currency_unit":         "credit",
		"credits_per_usd":       nil,
		"quota_per_usd":         nil,
		"wallet_balance_usd":    nil,
		"wallet_balance_status": "unavailable",
		"wallet_balance_note":   "Wallet balance is separate from remaining subscription quota and usage totals. Zero used_quota or zero cost in a usage window does not mean the wallet balance is zero.",
	}
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return fields
	}
	balanceUSD, err := common.CreditsToUSD(int64(quota))
	if err != nil {
		return fields
	}
	// Retain the old field name for clients, with the actual USD denomination.
	fields["quota_per_usd"] = anchor.InexactFloat64()
	fields["credits_per_usd"] = anchor.InexactFloat64()
	fields["wallet_balance_status"] = "available"
	fields["wallet_balance_usd"] = balanceUSD.InexactFloat64()
	return fields
}
