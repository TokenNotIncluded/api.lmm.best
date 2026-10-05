package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func setupAssistantCurrencyTest(t *testing.T) {
	t.Helper()
	oldK, oldKErr := common.CreditsPerUSD()
	oldQ, oldQErr := common.LegacyPricingQuotaPerUnit()
	oldRuntimeQ := common.QuotaPerUnit
	oldFX, oldBonus := operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	t.Cleanup(func() {
		common.QuotaPerUnit = oldRuntimeQ
		operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = oldFX, oldBonus
		if oldKErr != nil || oldQErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(oldK, oldQ))
		}
	})
	common.QuotaPerUnit = 500000
	operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 7, 1
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
}
