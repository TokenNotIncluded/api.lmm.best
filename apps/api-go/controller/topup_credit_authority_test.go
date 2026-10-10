package controller

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func preserveCanonicalCreditPricing(t *testing.T) {
	t.Helper()
	oldQuota := common.QuotaPerUnit
	oldAnchor, anchorErr := common.CreditsPerUSD()
	oldBasis, basisErr := common.LegacyPricingQuotaPerUnit()
	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	oldDiscount := operation_setting.GetPaymentSetting().AmountDiscount
	oldRatios := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuota
		if anchorErr == nil && basisErr == nil {
			require.NoError(t, common.SetCreditCurrencyBasis(oldAnchor, oldBasis))
		} else {
			common.ClearCreditsPerUSD()
		}
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay
		operation_setting.GetPaymentSetting().AmountDiscount = oldDiscount
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(oldRatios))
	})
	common.QuotaPerUnit = 500000
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"ldc":0.14}`))
}

func TestCanonicalTopUpGrantOwnsQuoteAndDiscount(t *testing.T) {
	preserveCanonicalCreditPricing(t)
	for _, mode := range []string{"USD", "CNY", "TOKENS"} {
		t.Run(mode, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = mode
			key, other := "1", "2"
			if mode == "TOKENS" {
				key, other = "500000", "1000000"
			}
			operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{key: 0.8, other: 0.25}
			for _, projection := range []string{"0", "1", "2", "6.8", "999999"} {
				amount := resolvedTopUpAmount{CreditedQuota: 500000, LegacyBatch: decimal.RequireFromString(projection)}
				configured, err := topUpConfigAmountFromCredits(amount.CreditedQuota)
				require.NoError(t, err)
				require.Equal(t, key, configured.String())
				original, paid, err := quoteTopUpResolvedSettlementAmounts(amount, "default", payMethodSettlementPricing{
					settlementCurrency: "USD", usesFixedCreditDenomination: true,
					settlementUnitsPerUSD: decimal.NewFromInt(1),
				}, decimal.NewFromInt(1))
				require.NoError(t, err)
				require.Equal(t, "1", original.String())
				require.Equal(t, "0.8", paid.String(), "projection=%s", projection)
				// A deliberately different compatibility projection cannot
				// change the configured group, channel or preset discount.
				original, paid, err = quoteTopUpResolvedSettlementAmounts(amount, "ldc", payMethodSettlementPricing{
					settlementCurrency: "LDC", usesSettlementUnitsPerPlatformUnit: true,
					settlementUnitsPerPlatformUnit: decimal.NewFromInt(10),
				}, decimal.RequireFromString("0.5"))
				require.NoError(t, err)
				require.Equal(t, "0.7", original.String())
				require.Equal(t, "0.56", paid.String())
			}
		})
	}
}

func TestCanonicalTopUpPresetRetainsExactCreditAmounts(t *testing.T) {
	preserveCanonicalCreditPricing(t)
	operation_setting.GetGeneralSetting().QuotaDisplayType = "USD"
	for _, tc := range []struct {
		credits int64
		want    string
	}{
		{1, "0.000002"}, {1750000, "3.5"},
		{9007199254740991, "18014398509.481982"},
	} {
		amount, err := topUpConfigAmountFromCredits(tc.credits)
		require.NoError(t, err)
		require.Equal(t, tc.want, amount.String())
	}
	// Historical-basis arithmetic remains covered without treating this
	// fixture as a production-startup configuration or forcing the ratio to 1.
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3400000), decimal.NewFromInt(500000)))
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{"6.8": 0.8}
	original, paid, err := quoteTopUpResolvedSettlementAmounts(resolvedTopUpAmount{
		CreditedQuota: 3400000, LegacyBatch: decimal.NewFromInt(1),
	}, "default", payMethodSettlementPricing{
		settlementCurrency: "USD", usesFixedCreditDenomination: true,
		settlementUnitsPerUSD: decimal.NewFromInt(1),
	}, decimal.NewFromInt(1))
	require.NoError(t, err)
	require.Equal(t, "1", original.String())
	require.Equal(t, "0.8", paid.String())
}

func TestCanonicalTopUpPresetRejectsInvalidCreditBasis(t *testing.T) {
	preserveCanonicalCreditPricing(t)
	for _, credits := range []int64{0, -1, common.MaxWalletQuota + 1} {
		_, err := topUpConfigAmountFromCredits(credits)
		require.Error(t, err)
	}
	for _, mode := range []string{"USD", "TOKENS"} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = mode
		for _, basis := range []float64{0, -1, math.NaN(), math.Inf(1)} {
			common.QuotaPerUnit = basis
			require.NotPanics(t, func() {
				_, err := topUpConfigAmountFromCredits(500000)
				require.Error(t, err)
			})
		}
	}
}
