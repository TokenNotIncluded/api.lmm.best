package service

import (
	"errors"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

func TestWaffoPancakeCheckoutCurrencyProductMatrix(t *testing.T) {
	for _, tc := range []struct {
		name        string
		currency    string
		productType string
		want        string
		wantError   bool
	}{
		{name: "legacy empty defaults USD", want: "USD"},
		{name: "one-time CNY", currency: "CNY", productType: model.WaffoPancakeProductTypeOneTime, want: "CNY"},
		{name: "one-time USD", currency: "USD", productType: model.WaffoPancakeProductTypeOneTime, want: "USD"},
		{name: "recurring USD", currency: "USD", productType: model.WaffoPancakeProductTypeSubscription, want: "USD"},
		{name: "recurring CNY unsupported", currency: "CNY", productType: model.WaffoPancakeProductTypeSubscription, wantError: true},
		{name: "CNY requires known one-time product", currency: "CNY", wantError: true},
		{name: "unsupported currency", currency: "EUR", productType: model.WaffoPancakeProductTypeOneTime, wantError: true},
		{name: "unknown product type", currency: "USD", productType: "unknown", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params, err := buildWaffoPancakeSDKCheckoutParams(&WaffoPancakeCreateSessionParams{
				ProductID: "PROD_test", Currency: tc.currency, ProductType: tc.productType,
				CheckoutRegion: "china", CheckoutLanguage: "zh-Hans",
				PriceSnapshot: &WaffoPancakePriceSnapshot{Amount: "9.90", TaxCategory: "saas"},
			})
			if tc.wantError {
				require.ErrorContains(t, err, "unsupported_settlement_currency")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, params.Currency, "checkout language/region must not override server currency")
			require.Equal(t, "9.90", params.PriceSnapshot.Amount)
		})
	}
}

func TestWaffoPancakeUnsupportedCurrencyRejectedBeforeClientCreation(t *testing.T) {
	// No credentials or transport needed: local compatibility validation must
	// fail before constructing the client, much less issuing a provider call.
	_, err := CreateWaffoPancakeCheckoutSession(t.Context(), &WaffoPancakeCreateSessionParams{
		ProductID: "PROD_test", Currency: "CNY", ProductType: model.WaffoPancakeProductTypeSubscription,
		BuyerIdentity: "new-api-user-1", OrderMerchantExternalID: "test-order",
	})
	require.ErrorContains(t, err, "unsupported_settlement_currency")
}

func TestFormatWaffoPancakeError(t *testing.T) {
	require.Equal(t, "", FormatWaffoPancakeError(nil))

	plainErr := errors.New("connection reset")
	require.Equal(t, "connection reset", FormatWaffoPancakeError(plainErr))

	pErr := &pancake.Error{
		Status: 422,
		Errors: []pancake.Notice{
			{
				Message: "Payment method not configured for currency",
				Layer:   "payment_methods",
				AIHint:  "Enable WeChat pay in Pancake store",
			},
		},
	}
	formatted := FormatWaffoPancakeError(pErr)
	require.Contains(t, formatted, "status=422")
	require.Contains(t, formatted, "[payment_methods]")
	require.Contains(t, formatted, "Payment method not configured for currency")
	require.Contains(t, formatted, "hint: Enable WeChat pay in Pancake store")
}

func TestWaffoPancakeIsUnsupportedProductCurrency(t *testing.T) {
	require.True(t, WaffoPancakeIsUnsupportedProductCurrency(&pancake.Error{
		Status: 400,
		Errors: []pancake.Notice{{Message: "Currency CNY is not supported for this product"}},
	}))
	require.False(t, WaffoPancakeIsUnsupportedProductCurrency(errors.New("connection reset")))
}

func installWaffoPancakeCreditCurrencyFixture(t *testing.T) {
	t.Helper()
	originalQuotaPerUnit := common.QuotaPerUnit
	originalAnchor, originalAnchorErr := common.CreditsPerUSD()
	originalLegacy, originalLegacyErr := common.LegacyPricingQuotaPerUnit()
	originalFX := operation_setting.USDExchangeRate
	originalUnits := operation_setting.TopUpPlatformUnitsPerCNY
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		operation_setting.USDExchangeRate = originalFX
		operation_setting.TopUpPlatformUnitsPerCNY = originalUnits
		if originalAnchorErr != nil {
			common.ClearCreditsPerUSD()
			return
		}
		require.NoError(t, originalLegacyErr)
		require.NoError(t, common.SetCreditCurrencyBasis(originalAnchor, originalLegacy))
	})
	common.QuotaPerUnit = 500000
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
}

func TestWaffoPancakeOneTimePricesUseLiveFiatRates(t *testing.T) {
	installWaffoPancakeCreditCurrencyFixture(t)
	for _, tc := range []struct {
		name    string
		fx      float64
		bonus   float64
		wantCNY string
	}{
		{name: "original cash quote", fx: 7.2, bonus: 1.25, wantCNY: "14.40"},
		{name: "new FX without repricing USD or applying bonus", fx: 8, bonus: 99, wantCNY: "16.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.USDExchangeRate = tc.fx
			operation_setting.TopUpPlatformUnitsPerCNY = tc.bonus
			// A real USD 2 source price remains USD 2 when FX or bonuses change.
			prices, err := waffoPancakeOneTimePrices(decimal.NewFromInt(2))
			require.NoError(t, err)
			require.Equal(t, "2.00", prices["USD"].Amount)
			require.Equal(t, tc.wantCNY, prices["CNY"].Amount)
		})
	}
}

func TestWaffoPancakeOneTimePricesPreserveSourceCurrencyPrecision(t *testing.T) {
	installWaffoPancakeCreditCurrencyFixture(t)
	for _, tc := range []struct {
		name    string
		fx      float64
		bonus   float64
		wantUSD string
	}{
		{name: "original precise CNY charge", fx: 6.730863, bonus: 1, wantUSD: "23.62"},
		{name: "new FX retains the original CNY charge", fx: 8, bonus: 99, wantUSD: "19.88"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.USDExchangeRate = tc.fx
			operation_setting.TopUpPlatformUnitsPerCNY = tc.bonus
			prices, err := waffoPancakeOneTimePricesForCurrency(decimal.NewFromInt(159), paymentpricing.CurrencyCNY)
			require.NoError(t, err)
			require.Equal(t, "159.00", prices["CNY"].Amount)
			require.Equal(t, tc.wantUSD, prices["USD"].Amount)
		})
	}
}
