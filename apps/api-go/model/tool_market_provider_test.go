package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/marketprovider"
	"github.com/stretchr/testify/require"
)

func TestToolMarketProviderQuotaCeilingAndFee(t *testing.T) {
	units, err := common.LedgerQuotaPerUSD()
	require.NoError(t, err)
	q := &marketprovider.Quote{Provider: "agentkey", Unit: "call", AmountUSD: "0.004"}
	expected, err := q.Quota("1.2", units.String())
	require.NoError(t, err)
	tool := ToolMarketToolVersion{PriceQuota: expected, ProviderPricing: &marketprovider.Pricing{Provider: "agentkey", Multiplier: "1.2"}}
	price, err := toolMarketProviderQuota(tool, q, 1000)
	require.NoError(t, err)
	require.Equal(t, expected, price)
	_, err = toolMarketProviderQuota(tool, nil, 1000)
	require.ErrorIs(t, err, ErrToolMarketInput)
	tool.PriceQuota = expected - 1
	_, err = toolMarketProviderQuota(tool, q, 1000)
	require.ErrorIs(t, err, ErrToolMarketBudget)
	tool.PriceQuota = expected
	tool.ProviderPricing.Multiplier = "1"
	_, err = toolMarketProviderQuota(tool, q, 1000)
	require.ErrorIs(t, err, marketprovider.ErrPrice, "do not sell below quoted cost after fee")
	q.Provider = "monid"
	_, err = toolMarketProviderQuota(tool, q, 0)
	require.ErrorIs(t, err, ErrToolMarketInput)
}

func TestToolMarketProviderPricingCannotBeFreeOrMixed(t *testing.T) {
	for _, tool := range []ToolMarketToolInput{
		{PriceQuota: 0}, {PriceQuota: 10, BillingMode: "input_tokens", MaxInputTokens: 1, InputTokenPriceQuota: 1}, {PriceQuota: 10, BillingRules: []ToolMarketBillingRule{{Metric: "requests", RateQuota: 1, MaxQuantity: 1}}},
	} {
		tool.ProviderPricing = &marketprovider.Pricing{Provider: "monid", Multiplier: "1.2"}
		require.ErrorIs(t, normalizeToolMarketPricing(&tool), ErrToolMarketInput)
	}
}
