package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSharedSettlementPricingPreservesCanonicalRechargeRatios(t *testing.T) {
	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQPU })
	preserveChannelPricing(t)
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeCNY
	operation_setting.PayMethods = []map[string]string{{
		"name": "LINUX DO Credit", "type": "epay", "settlement_unit": "LDC",
		"unit_price": "10", "topup_ratio": "0.5",
	}}
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{"1": 0.8}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"ldc":0.14}`))
	pricing, err := getPayMethodSettlementPricing("epay")
	require.NoError(t, err)
	ratio, err := getPayMethodTopupRatio("epay")
	require.NoError(t, err)
	original, paid, err := quoteTopUpResolvedSettlementAmounts(resolvedTopUpAmount{
		CreditedQuota: 500000, LegacyBatch: decimal.NewFromInt(1),
	}, "ldc", pricing, ratio)
	require.NoError(t, err)
	require.True(t, original.Equal(decimal.RequireFromString("0.70")))
	require.True(t, paid.Equal(decimal.RequireFromString("0.56")))
}

func TestSharedSettlementPricingPairedLDCIgnoresInvalidCNYFX(t *testing.T) {
	previousQPU := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previousQPU })
	preserveChannelPricing(t)
	operation_setting.USDExchangeRate = 0
	operation_setting.PayMethods = []map[string]string{{
		"name": "LINUX DO Credit", "type": "epay", "settlement_unit": "LDC",
		"settlement_units_per_usd": "1",
	}}
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	pricing, err := getPayMethodSettlementPricing("epay")
	require.NoError(t, err)
	require.True(t, pricing.platformUnitsPerUSD.Equal(decimal.RequireFromString("6.8")))
	quote, err := quoteTopUpResolvedWithSettlementPricing(resolvedTopUpAmount{
		CreditedQuota: 3400000, LegacyBatch: decimal.RequireFromString("6.8"),
	}, "default", pricing, decimal.NewFromInt(1))
	require.NoError(t, err)
	require.True(t, quote.Equal(decimal.NewFromInt(1)))
}
