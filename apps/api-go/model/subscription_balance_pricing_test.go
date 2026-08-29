package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionBalanceQuotaUsesDynamicFiatAndRechargeRates(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	originalCNYPerUSD := operation_setting.USDExchangeRate
	originalPlatformUnitsPerCNY := operation_setting.TopUpPlatformUnitsPerCNY
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		operation_setting.USDExchangeRate = originalCNYPerUSD
		operation_setting.TopUpPlatformUnitsPerCNY = originalPlatformUnitsPerCNY
	})

	common.QuotaPerUnit = 1000
	tests := []struct {
		name                string
		price               float64
		currency            string
		cnyPerUSD           float64
		platformUnitsPerCNY float64
		wantQuota           int
	}{
		{name: "first dynamic USD rate", price: 1, currency: "USD", cnyPerUSD: 6.8, platformUnitsPerCNY: 1, wantQuota: 6800},
		{name: "second dynamic USD rate and purchase ratio", price: 2, currency: "USD", cnyPerUSD: 7.2, platformUnitsPerCNY: 1.25, wantQuota: 18000},
		{name: "CNY plan does not apply fiat FX", price: 8, currency: "CNY", cnyPerUSD: 7.2, platformUnitsPerCNY: 1.25, wantQuota: 10000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			operation_setting.USDExchangeRate = tt.cnyPerUSD
			operation_setting.TopUpPlatformUnitsPerCNY = tt.platformUnitsPerCNY
			quota, err := calcSubscriptionBalanceQuota(tt.price, tt.currency)
			require.NoError(t, err)
			require.Equal(t, tt.wantQuota, quota)
		})
	}
}
