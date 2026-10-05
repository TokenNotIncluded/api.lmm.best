package controller

import (
	"errors"
	"math"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

var errAssistantCurrencyProjectionUnavailable = errors.New("assistant currency amount cannot be represented")

// These DTOs require JSON numbers. A valid decimal ledger denomination may
// underflow as a float, or its fiat projection may overflow; neither is zero.
func assistantFiatProjection(credits int64) (usd, anchorFloat float64, err error) {
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return 0, 0, err
	}
	anchorFloat = anchor.InexactFloat64()
	if anchorFloat <= 0 || math.IsNaN(anchorFloat) || math.IsInf(anchorFloat, 0) {
		return 0, 0, errAssistantCurrencyProjectionUnavailable
	}
	amount, err := common.CreditsToUSD(credits)
	if err != nil {
		return 0, 0, err
	}
	usd = amount.InexactFloat64()
	if math.IsNaN(usd) || math.IsInf(usd, 0) {
		return 0, 0, errAssistantCurrencyProjectionUnavailable
	}
	return usd, anchorFloat, nil
}

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
	balanceUSD, anchor, err := assistantFiatProjection(int64(quota))
	if err != nil {
		return fields
	}
	// Retain the old field name for clients, with the actual USD denomination.
	fields["quota_per_usd"] = anchor
	fields["credits_per_usd"] = anchor
	fields["wallet_balance_status"] = "available"
	fields["wallet_balance_usd"] = balanceUSD
	return fields
}
