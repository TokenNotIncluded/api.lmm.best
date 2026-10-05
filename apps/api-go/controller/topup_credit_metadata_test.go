package controller

import (
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func preserveTopUpCreditMetadataConfig(t *testing.T) {
	t.Helper()
	previousAnchor, previousAnchorErr := common.CreditsPerUSD()
	previousLegacy, previousLegacyErr := common.LegacyPricingQuotaPerUnit()
	previousRuntimeQ := common.QuotaPerUnit
	general := operation_setting.GetGeneralSetting()
	previousDisplay := general.QuotaDisplayType
	previousFX := operation_setting.USDExchangeRate
	previousBonus := operation_setting.TopUpPlatformUnitsPerCNY
	payment := operation_setting.GetPaymentSetting()
	previousOptions, previousDiscounts := payment.AmountOptions, payment.AmountDiscount
	previousMethods := operation_setting.PayMethods
	previousMinimum := operation_setting.MinTopUp
	previousStripeMinimum := setting.StripeMinTopUp
	previousWaffoMinimum := setting.WaffoMinTopUp
	previousPancakeMinimum := setting.WaffoPancakeMinTopUp
	t.Cleanup(func() {
		common.QuotaPerUnit = previousRuntimeQ
		if previousAnchorErr != nil || previousLegacyErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(previousAnchor, previousLegacy))
		}
		general.QuotaDisplayType = previousDisplay
		operation_setting.USDExchangeRate = previousFX
		operation_setting.TopUpPlatformUnitsPerCNY = previousBonus
		payment.AmountOptions, payment.AmountDiscount = previousOptions, previousDiscounts
		operation_setting.PayMethods = previousMethods
		operation_setting.MinTopUp = previousMinimum
		setting.StripeMinTopUp = previousStripeMinimum
		setting.WaffoMinTopUp = previousWaffoMinimum
		setting.WaffoPancakeMinTopUp = previousPancakeMinimum
	})

	common.QuotaPerUnit = 500000
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
	general.QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.USDExchangeRate = 7
	operation_setting.TopUpPlatformUnitsPerCNY = 1
	payment.AmountOptions = []int{1, 10}
	payment.AmountDiscount = map[int]float64{10: 0.9}
	operation_setting.PayMethods = []map[string]string{}
	operation_setting.MinTopUp = 1
	setting.StripeMinTopUp = 2
	setting.WaffoMinTopUp = 3
	setting.WaffoPancakeMinTopUp = 4
}

func cloneTopUpCreditMetadataMethods(methods []map[string]string) []map[string]string {
	clones := make([]map[string]string, len(methods))
	for index, method := range methods {
		clones[index] = make(map[string]string, len(method))
		for key, value := range method {
			clones[index][key] = value
		}
	}
	return clones
}

func requireTopUpCreditMetadataMinima(t *testing.T, metadata gin.H, online, stripe, waffo, pancake int64) {
	t.Helper()
	require.Equal(t, online, metadata["credit_min_topup"])
	require.Equal(t, stripe, metadata["stripe_credit_min_topup"])
	require.Equal(t, waffo, metadata["waffo_credit_min_topup"])
	require.Equal(t, pancake, metadata["pancake_credit_min_topup"])
}

func TestTopUpCreditMetadataConvertsLegacyCatalogOnce(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	for _, display := range []string{operation_setting.QuotaDisplayTypeUSD, operation_setting.QuotaDisplayTypeCNY} {
		t.Run(display, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = display
			metadata, err := topUpCreditMetadata(nil)
			require.NoError(t, err)
			require.Equal(t, 1, metadata["credit_metadata_version"])
			require.Equal(t, []int64{500000, 5000000}, metadata["credit_amount_options"])
			require.Equal(t, map[string]float64{"5000000": 0.9}, metadata["credit_discount"])
			requireTopUpCreditMetadataMinima(t, metadata, 500000, 1000000, 1500000, 2000000)
			require.NotContains(t, metadata, "pay_methods")
			require.Equal(t, []int{1, 10}, operation_setting.GetPaymentSetting().AmountOptions)
			require.Equal(t, map[int]float64{10: 0.9}, operation_setting.GetPaymentSetting().AmountDiscount)
		})
	}
}

func TestTopUpCreditMetadataTokensCatalogAlreadyContainsRawCredits(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	operation_setting.GetPaymentSetting().AmountOptions = []int{500000, 5000000, 500000}
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{5000000: 0.9}
	metadata, err := topUpCreditMetadata(nil)
	require.NoError(t, err)
	require.Equal(t, []int64{500000, 5000000, 500000}, metadata["credit_amount_options"])
	require.Equal(t, map[string]float64{"5000000": 0.9}, metadata["credit_discount"])
	// Gateway minimum settings still name legacy batches in TOKENS display.
	requireTopUpCreditMetadataMinima(t, metadata, 500000, 1000000, 1500000, 2000000)
}

func TestTopUpCreditMetadataUsesConfiguredUSDLimits(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	operation_setting.PayMethods = []map[string]string{{
		"name": "Configured", "type": "custom", "min_topup": "1", "max_topup": "2.5",
	}}
	methods := []map[string]string{{
		"name": "Configured", "type": "custom", "min_topup": "1", "max_topup": "2.5",
		"min_topup_unit": "USD", "legacy_min_topup": "7", "legacy_max_topup_amount": "17.5",
		"description": "Keep existing metadata",
	}}
	before := cloneTopUpCreditMetadataMethods(methods)
	configuredBefore := cloneTopUpCreditMetadataMethods(operation_setting.PayMethods)
	metadata, err := topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.NotContains(t, metadata, "pay_methods")
	require.Equal(t, "3500000", methods[0]["min_topup_credit"])
	require.Equal(t, "8750000", methods[0]["max_topup_credit"])
	for key, value := range before[0] {
		require.Equal(t, value, methods[0][key], key)
	}
	require.Equal(t, configuredBefore, operation_setting.PayMethods)

	operation_setting.USDExchangeRate = 8
	operation_setting.TopUpPlatformUnitsPerCNY = 99
	_, err = topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.Equal(t, "3500000", methods[0]["min_topup_credit"])
	require.Equal(t, "8750000", methods[0]["max_topup_credit"])
}

func TestTopUpCreditMetadataUsesStrictestDuplicatePolicy(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	operation_setting.PayMethods = []map[string]string{
		{"name": "First", "type": "custom", "min_topup": "1", "max_topup": "5"},
		{"name": "Hidden duplicate", "type": "custom", "min_topup": "2", "max_topup": "2.5"},
	}
	methods := []map[string]string{{
		"name": "Visible", "type": "custom", "min_topup": "1", "min_topup_unit": "USD",
		"legacy_min_topup": "7", "legacy_max_topup_amount": "35",
	}}
	_, err := topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.Equal(t, "7000000", methods[0]["min_topup_credit"])
	require.Equal(t, "8750000", methods[0]["max_topup_credit"])
}

func TestTopUpCreditMetadataRoundsUSDDirectlyToCredits(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	// Permit tiny grants here to isolate USD rounding from the ordinary rail's
	// minimum; complete provider minima are verified separately below.
	operation_setting.MinTopUp = 0
	common.QuotaPerUnit = 300000
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(300000)))
	operation_setting.PayMethods = []map[string]string{
		{"name": "One-credit minimum", "type": "min-one", "min_topup": "0.0000002"},
		{"name": "Two-credit minimum", "type": "min-two", "min_topup": "0.0000003"},
		{"name": "One-credit maximum", "type": "max-one", "max_topup": "0.000000285714285715"},
	}
	methods := sanitizedPaymentMethods(operation_setting.PayMethods)
	require.Len(t, methods, 3)
	require.Equal(t, "0.0000033333333333", methods[2]["legacy_max_topup_amount"])
	_, err := topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.Equal(t, "1", methods[0]["min_topup_credit"])
	require.Equal(t, "2", methods[1]["min_topup_credit"])
	// Reusing the rounded legacy alias would incorrectly produce zero.
	require.Equal(t, "0", methods[2]["min_topup_credit"])
	require.Equal(t, "1", methods[2]["max_topup_credit"])
}

func TestTopUpCreditMetadataRoundsFractionalLegacyScaleAtLedgerBoundary(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	common.QuotaPerUnit = 1.25
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.RequireFromString("1.25")))
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1: 0.9, 10: 1.2}
	methods := []map[string]string{{
		"name": "Automatic", "type": "stripe", "min_topup": "1.1",
		"min_topup_unit": "LEGACY", "legacy_min_topup": "1.1", "legacy_max_topup_amount": "8",
	}}
	metadata, err := topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.Equal(t, []int64{1, 12}, metadata["credit_amount_options"])
	require.Equal(t, map[string]float64{"1": 0.9, "12": 1.2}, metadata["credit_discount"])
	requireTopUpCreditMetadataMinima(t, metadata, 2, 3, 4, 5)
	require.Equal(t, "3", methods[0]["min_topup_credit"])
	require.NotContains(t, methods[0], "max_topup_credit", "an automatic method has no configured USD maximum")
}

func TestTopUpCreditMetadataUsesCompleteMinimumForEachProvider(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	operation_setting.MinTopUp = 20
	methods := []map[string]string{
		{"name": "Ordinary", "type": "alipay"},
		{"name": "Custom Epay", "type": "custom"},
		{"name": "Stripe", "type": model.PaymentMethodStripe},
		{"name": "Waffo", "type": model.PaymentMethodWaffo},
		{"name": "Pancake", "type": model.PaymentMethodWaffoPancake},
		{"name": "Fixed product", "type": model.PaymentMethodCreem},
	}
	metadata, err := topUpCreditMetadata(methods)
	require.NoError(t, err)
	requireTopUpCreditMetadataMinima(t, metadata, 10000000, 1000000, 1500000, 2000000)
	for index, expected := range []string{"10000000", "10000000", "1000000", "1500000", "2000000", "0"} {
		require.Equal(t, expected, methods[index]["min_topup_credit"], methods[index]["type"])
	}

	// A stricter USD policy wins for its own rail, without inheriting Epay's
	// higher ordinary recharge minimum. Fixed Creem products retain their policy.
	operation_setting.PayMethods = []map[string]string{
		{"type": model.PaymentMethodStripe, "min_topup": "1", "max_topup": "2.5"},
		{"type": model.PaymentMethodCreem, "min_topup": "1", "max_topup": "2.5"},
	}
	_, err = topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.Equal(t, "3500000", methods[2]["min_topup_credit"])
	require.Equal(t, "8750000", methods[2]["max_topup_credit"])
	require.Equal(t, "3500000", methods[5]["min_topup_credit"])
	require.Equal(t, "8750000", methods[5]["max_topup_credit"])
	require.Equal(t, "10000000", methods[0]["min_topup_credit"])

	// An automatic legacy alias can be stricter than the provider setting too.
	methods[3]["min_topup_unit"], methods[3]["legacy_min_topup"] = "LEGACY", "5"
	_, err = topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.Equal(t, "2500000", methods[3]["min_topup_credit"])
}

func TestTopUpCreditMetadataAllowsZeroMinimumAndClosedMethod(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	operation_setting.MinTopUp, setting.StripeMinTopUp, setting.WaffoMinTopUp, setting.WaffoPancakeMinTopUp = 0, 0, 0, 0
	operation_setting.GetPaymentSetting().AmountOptions = []int{}
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
	operation_setting.PayMethods = []map[string]string{{
		"name": "Closed", "type": "closed", "min_topup": "0", "max_topup": "0.0000001",
	}}
	methods := []map[string]string{{"name": "Closed", "type": "closed"}}
	metadata, err := topUpCreditMetadata(methods)
	require.NoError(t, err)
	require.Empty(t, metadata["credit_amount_options"])
	require.Empty(t, metadata["credit_discount"])
	requireTopUpCreditMetadataMinima(t, metadata, 0, 0, 0, 0)
	require.Equal(t, "0", methods[0]["min_topup_credit"])
	require.Equal(t, "0", methods[0]["max_topup_credit"])
}

func TestTopUpCreditMetadataRejectsInvalidConfigurationWithoutPartialMutation(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(t *testing.T, methods []map[string]string)
	}{
		{"unavailable credit basis", func(_ *testing.T, _ []map[string]string) { common.ClearCreditsPerUSD() }},
		{"zero runtime calibration", func(_ *testing.T, _ []map[string]string) { common.QuotaPerUnit = 0 }},
		{"negative runtime calibration", func(_ *testing.T, _ []map[string]string) { common.QuotaPerUnit = -1 }},
		{"nonfinite runtime calibration", func(_ *testing.T, _ []map[string]string) { common.QuotaPerUnit = math.NaN() }},
		{"infinite runtime calibration", func(_ *testing.T, _ []map[string]string) { common.QuotaPerUnit = math.Inf(1) }},
		{"runtime calibration differs from immutable basis", func(_ *testing.T, _ []map[string]string) { common.QuotaPerUnit = 300000 }},
		{"zero catalog amount", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountOptions = []int{0}
		}},
		{"negative catalog amount", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountOptions = []int{-1}
		}},
		{"unsafe legacy catalog amount", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountOptions = []int{100000000000}
		}},
		{"unsafe raw catalog amount", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
			operation_setting.GetPaymentSetting().AmountOptions = []int{9007199254740992}
		}},
		{"negative gateway minimum", func(_ *testing.T, _ []map[string]string) { setting.WaffoPancakeMinTopUp = -1 }},
		{"unsafe gateway minimum", func(_ *testing.T, _ []map[string]string) { setting.StripeMinTopUp = 100000000000 }},
		{"zero discount threshold", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{0: 0.9}
		}},
		{"negative discount threshold", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{-1: 0.9}
		}},
		{"unsafe discount threshold", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{100000000000: 0.9}
		}},
		{"zero discount factor", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1: 0}
		}},
		{"negative discount factor", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1: -1}
		}},
		{"nonfinite discount factor", func(_ *testing.T, _ []map[string]string) {
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{1: math.NaN()}
		}},
		{"conflicting rounded discount thresholds", func(t *testing.T, _ []map[string]string) {
			common.QuotaPerUnit = 0.25
			require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.RequireFromString("0.25")))
			operation_setting.GetPaymentSetting().AmountOptions = []int{4}
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{4: 0.9, 5: 0.8}
		}},
		{"unknown minimum unit", func(_ *testing.T, methods []map[string]string) { methods[1]["min_topup_unit"] = "DOGE" }},
		{"negative automatic minimum", func(_ *testing.T, methods []map[string]string) { methods[1]["legacy_min_topup"] = "-1" }},
		{"malformed automatic minimum", func(_ *testing.T, methods []map[string]string) { methods[1]["legacy_min_topup"] = "bad" }},
		{"negative configured USD minimum", func(_ *testing.T, _ []map[string]string) {
			operation_setting.PayMethods = []map[string]string{{"type": "later", "min_topup": "-1"}}
		}},
		{"malformed configured USD maximum", func(_ *testing.T, _ []map[string]string) {
			operation_setting.PayMethods = []map[string]string{{"type": "later", "max_topup": "bad"}}
		}},
		{"negative configured USD maximum", func(_ *testing.T, _ []map[string]string) {
			operation_setting.PayMethods = []map[string]string{{"type": "later", "max_topup": "-1"}}
		}},
		{"zero configured USD maximum", func(_ *testing.T, _ []map[string]string) {
			operation_setting.PayMethods = []map[string]string{{"type": "later", "max_topup": "0"}}
		}},
		{"unsafe configured USD maximum", func(_ *testing.T, _ []map[string]string) {
			operation_setting.PayMethods = []map[string]string{{"type": "later", "max_topup": "100000000000"}}
		}},
		{"minimum exceeds maximum", func(_ *testing.T, _ []map[string]string) {
			operation_setting.PayMethods = []map[string]string{{"type": "later", "min_topup": "2.5", "max_topup": "1"}}
		}},
		{"ordinary provider minimum exceeds maximum", func(_ *testing.T, _ []map[string]string) {
			operation_setting.PayMethods = []map[string]string{{"type": "later", "max_topup": "0.1"}}
		}},
		{"dedicated provider minimum exceeds maximum", func(_ *testing.T, methods []map[string]string) {
			methods[1]["type"] = model.PaymentMethodStripe
			operation_setting.PayMethods = []map[string]string{{"type": model.PaymentMethodStripe, "max_topup": "0.1"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			preserveTopUpCreditMetadataConfig(t)
			methods := []map[string]string{
				{"name": "Earlier", "type": "earlier", "min_topup_unit": "LEGACY", "legacy_min_topup": "1", "min_topup_credit": "original"},
				{"name": "Later", "type": "later", "min_topup_unit": "LEGACY", "legacy_min_topup": "1"},
			}
			test.configure(t, methods)
			before := cloneTopUpCreditMetadataMethods(methods)
			configuredBefore := cloneTopUpCreditMetadataMethods(operation_setting.PayMethods)
			metadata, err := topUpCreditMetadata(methods)
			require.Error(t, err)
			require.Nil(t, metadata)
			require.Equal(t, before, methods, "a later failure must not publish earlier partial limits")
			require.Equal(t, configuredBefore, operation_setting.PayMethods)
		})
	}
}
