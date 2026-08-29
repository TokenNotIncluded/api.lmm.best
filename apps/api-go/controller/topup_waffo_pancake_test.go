package controller

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestFormatWaffoPancakeAmount_UsesDisplayPriceString(t *testing.T) {
	testCases := []struct {
		name     string
		amount   float64
		expected string
	}{
		{name: "whole amount", amount: 29, expected: "29.00"},
		{name: "decimal amount", amount: 29.9, expected: "29.90"},
		{name: "round half up to cents", amount: 29.999, expected: "30.00"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, formatWaffoPancakeAmount(tc.amount))
		})
	}
}

func TestGetWaffoPancakePayMoney(t *testing.T) {
	originalUnitPrice := setting.WaffoPancakeUnitPrice
	originalUSDExchangeRate := operation_setting.USDExchangeRate
	originalPlatformUnitsPerCNY := operation_setting.TopUpPlatformUnitsPerCNY
	originalQuotaDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	originalDiscounts := make(map[int]float64, len(operation_setting.GetPaymentSetting().AmountDiscount))
	for k, v := range operation_setting.GetPaymentSetting().AmountDiscount {
		originalDiscounts[k] = v
	}
	originalTopupGroupRatio := common.TopupGroupRatio2JSONString()

	t.Cleanup(func() {
		setting.WaffoPancakeUnitPrice = originalUnitPrice
		operation_setting.USDExchangeRate = originalUSDExchangeRate
		operation_setting.TopUpPlatformUnitsPerCNY = originalPlatformUnitsPerCNY
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalQuotaDisplayType
		operation_setting.GetPaymentSetting().AmountDiscount = originalDiscounts
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(originalTopupGroupRatio))
	})

	// Provider-specific UnitPrice is deprecated and must not affect settlement.
	setting.WaffoPancakeUnitPrice = 999
	operation_setting.USDExchangeRate = 6.8
	operation_setting.TopUpPlatformUnitsPerCNY = 1
	operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{
		10:                           0.8,
		int(common.QuotaPerUnit * 3): 0.5,
		20:                           0,
	}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1,"vip":1.2}`))

	testCases := []struct {
		name             string
		amount           int64
		group            string
		quotaDisplayType string
		expected         float64
	}{
		{
			name:             "USD settlement uses global FX group ratio and discount",
			amount:           10,
			group:            "vip",
			quotaDisplayType: operation_setting.QuotaDisplayTypeUSD,
			expected:         1.41,
		},
		{
			name:             "tokens display normalizes before standard USD settlement",
			amount:           int64(common.QuotaPerUnit * 3),
			group:            "vip",
			quotaDisplayType: operation_setting.QuotaDisplayTypeTokens,
			expected:         0.26,
		},
		{
			name:             "non-positive discount falls back to no discount",
			amount:           20,
			group:            "default",
			quotaDisplayType: operation_setting.QuotaDisplayTypeUSD,
			expected:         2.94,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			operation_setting.GetGeneralSetting().QuotaDisplayType = tc.quotaDisplayType
			actual := getWaffoPancakePayMoney(tc.amount, tc.group)
			require.InDelta(t, tc.expected, actual, 0.000001)
		})
	}
}

func TestWaffoPancakeSubscriptionPeriodRequiresAuthoritativeWindow(t *testing.T) {
	start := time.Now().UTC().Truncate(time.Second)
	end := start.AddDate(0, 1, 0)
	event := &service.WaffoPancakeWebhookEvent{Data: service.WaffoPancakeWebhookData{
		CurrentPeriodStart: start.Format(time.RFC3339),
		CurrentPeriodEnd:   end.Format(time.RFC3339),
	}}

	actualStart, actualEnd, err := waffoPancakeSubscriptionPeriod(event, true)
	require.NoError(t, err)
	require.Equal(t, start.Unix(), actualStart)
	require.Equal(t, end.Unix(), actualEnd)

	event.Data.CurrentPeriodEnd = ""
	_, _, err = waffoPancakeSubscriptionPeriod(event, true)
	require.ErrorContains(t, err, "currentPeriodEnd")
}

func TestValidateWaffoPancakeSubscriptionEventBindsSettlementEvidence(t *testing.T) {
	originalStoreID := setting.WaffoPancakeStoreID
	originalUSDExchangeRate := operation_setting.USDExchangeRate
	originalPlatformUnitsPerCNY := operation_setting.TopUpPlatformUnitsPerCNY
	setting.WaffoPancakeStoreID = "STO_expected"
	operation_setting.USDExchangeRate = 6.8
	operation_setting.TopUpPlatformUnitsPerCNY = 99 // must not affect fiat subscription settlement
	t.Cleanup(func() {
		setting.WaffoPancakeStoreID = originalStoreID
		operation_setting.USDExchangeRate = originalUSDExchangeRate
		operation_setting.TopUpPlatformUnitsPerCNY = originalPlatformUnitsPerCNY
	})

	plan := &model.SubscriptionPlan{
		Id:                    42,
		PriceAmount:           6.8,
		Currency:              "CNY",
		WaffoPancakeProductId: "PROD_expected",
	}
	order := &model.SubscriptionOrder{
		PlanId:               plan.Id,
		Money:                plan.PriceAmount,
		ExpectedAmountMicros: 1_000_000,
		SettlementCurrency:   "USD",
		ProviderProductId:    "PROD_expected",
		ProviderStoreId:      "STO_expected",
	}
	valid := &service.WaffoPancakeWebhookEvent{
		StoreID: "STO_expected",
		Data: service.WaffoPancakeWebhookData{
			Amount:   "1.00",
			Currency: "usd",
			OrderMetadata: map[string]string{
				service.WaffoPancakeOrderMetadataProductID: "PROD_expected",
				service.WaffoPancakeOrderMetadataPlanID:    "42",
			},
		},
	}
	require.NoError(t, validateWaffoPancakeSubscriptionEvent(valid, order, plan))

	tests := []struct {
		name   string
		mutate func(*service.WaffoPancakeWebhookEvent)
		want   string
	}{
		{name: "amount", mutate: func(event *service.WaffoPancakeWebhookEvent) { event.Data.Amount = "0.01" }, want: "amount mismatch"},
		{name: "currency", mutate: func(event *service.WaffoPancakeWebhookEvent) { event.Data.Currency = "EUR" }, want: "currency mismatch"},
		{name: "store", mutate: func(event *service.WaffoPancakeWebhookEvent) { event.StoreID = "STO_other" }, want: "store mismatch"},
		{name: "product", mutate: func(event *service.WaffoPancakeWebhookEvent) {
			event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataProductID] = "PROD_other"
		}, want: "product metadata mismatch"},
		{name: "plan", mutate: func(event *service.WaffoPancakeWebhookEvent) {
			event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataPlanID] = "7"
		}, want: "plan metadata mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := *valid
			event.Data = valid.Data
			event.Data.OrderMetadata = map[string]string{}
			for key, value := range valid.Data.OrderMetadata {
				event.Data.OrderMetadata[key] = value
			}
			tt.mutate(&event)
			err := validateWaffoPancakeSubscriptionEvent(&event, order, plan)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestValidateWaffoPancakeTopUpEventBindsStoreAndProductMetadata(t *testing.T) {
	topUp := &model.TopUp{
		ProviderStoreId:   "STO_expected",
		ProviderProductId: "PROD_expected",
	}
	valid := &service.WaffoPancakeWebhookEvent{
		StoreID: "STO_expected",
		Data: service.WaffoPancakeWebhookData{
			OrderMetadata: map[string]string{
				service.WaffoPancakeOrderMetadataProductID: "PROD_expected",
			},
		},
	}
	require.NoError(t, validateWaffoPancakeTopUpEvent(valid, topUp))

	tests := []struct {
		name   string
		mutate func(*service.WaffoPancakeWebhookEvent)
		want   string
	}{
		{name: "store", mutate: func(event *service.WaffoPancakeWebhookEvent) { event.StoreID = "STO_other" }, want: "store mismatch"},
		{name: "product", mutate: func(event *service.WaffoPancakeWebhookEvent) {
			event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataProductID] = "PROD_other"
		}, want: "product metadata mismatch"},
		{name: "empty product metadata", mutate: func(event *service.WaffoPancakeWebhookEvent) {
			event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataProductID] = ""
		}, want: "product metadata mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := *valid
			event.Data = valid.Data
			event.Data.OrderMetadata = map[string]string{}
			for key, value := range valid.Data.OrderMetadata {
				event.Data.OrderMetadata[key] = value
			}
			tt.mutate(&event)
			require.ErrorContains(t, validateWaffoPancakeTopUpEvent(&event, topUp), tt.want)
		})
	}

	// Legacy orders created before metadata binding remain processable.
	legacy := *valid
	legacy.Data.OrderMetadata = nil
	require.NoError(t, validateWaffoPancakeTopUpEvent(&legacy, topUp))
}
