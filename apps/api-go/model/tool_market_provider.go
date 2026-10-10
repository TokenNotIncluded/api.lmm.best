package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/marketprovider"
)

// Only the trusted adapter supplies a fresh quote. The browser cannot supply a
// price override. Review, grant and wallet caps still bound every reservation.
func toolMarketProviderQuota(tool ToolMarketToolVersion, quote *marketprovider.Quote, feeBPS int) (int, error) {
	if tool.ProviderPricing == nil || quote == nil || quote.Provider != tool.ProviderPricing.Provider || tool.BillingMode != "" {
		return 0, ErrToolMarketInput
	}
	units, err := common.LedgerQuotaPerUSD()
	if err != nil {
		return 0, err
	}
	price, err := quote.Quota(tool.ProviderPricing.Multiplier, units.String())
	if err != nil {
		return 0, err
	}
	cost, err := quote.Quota("1", units.String())
	if err != nil || !marketQuotaValid(price) || !marketQuotaValid(cost) || feeBPS < 0 || feeBPS > 10000 {
		return 0, ErrToolMarketInput
	}
	if price > tool.PriceQuota {
		return 0, ErrToolMarketBudget
	}
	// Refuse a configuration whose author proceeds do not cover the quoted USD
	// cost after this call's platform fee. Never silently increase a multiplier.
	if price-marketFee(price, feeBPS) < cost {
		return 0, marketprovider.ErrPrice
	}
	return price, nil
}
