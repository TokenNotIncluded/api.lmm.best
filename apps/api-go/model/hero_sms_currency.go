package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
)

// Only public projections use fiat. Quotes, stored prices, debit and refunds
// keep their original legacy calibration and integer ledger values.
type HeroSMSPricingMetadata struct {
	PricingSchemaVersion int    `json:"pricing_schema_version"`
	PricingCurrency      string `json:"pricing_currency"`
	PricingAvailable     bool   `json:"pricing_available"`
}

func heroSMSPriceProjection(chargeQuota, quantity int) (string, HeroSMSPricingMetadata) {
	metadata := HeroSMSPricingMetadata{PricingSchemaVersion: 2}
	anchor, err := common.CreditsPerUSD()
	if err != nil || chargeQuota < 0 || quantity <= 0 {
		return "", metadata
	}
	price := decimal.NewFromInt(int64(chargeQuota)).DivRound(anchor.Mul(decimal.NewFromInt(int64(quantity))), 64)
	metadata.PricingCurrency, metadata.PricingAvailable = "USD", true
	return price.String(), metadata
}
