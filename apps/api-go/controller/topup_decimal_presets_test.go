package controller

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestTopUpDecimalPresetsActualCatalogAndDiscount(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	payment := operation_setting.GetPaymentSetting()
	require.NoError(t, config.UpdateConfigFromMap(payment, map[string]string{
		"amount_options":  `[1,2,3.5,5,10,20,50,100]`,
		"amount_discount": `{"1":1,"2":0.99,"5":0.97,"10":0.96,"20":0.94,"50":0.92,"100":0.9,"3.5":0.98}`,
	}))
	for _, display := range []string{operation_setting.QuotaDisplayTypeUSD, operation_setting.QuotaDisplayTypeCNY} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = display
		metadata, err := topUpCreditMetadata(nil)
		require.NoError(t, err)
		require.Equal(t, []int64{500000, 1000000, 1750000, 2500000, 5000000, 10000000, 25000000, 50000000}, metadata["credit_amount_options"])
		require.Equal(t, 0.98, metadata["credit_discount"].(map[string]float64)["1750000"])
		require.NotContains(t, metadata["credit_discount"], "1500000")
		original, paid := applyTopUpSettlementRatiosWithOriginal(decimal.RequireFromString("23.500631"), decimal.RequireFromString("3.5"), "default", decimal.NewFromInt(1))
		require.Equal(t, "23.5", original.String())
		require.Equal(t, "23.03", paid.String(), "3.5 must retain its exact 2%% preset discount")
		require.NoError(t, model.ValidateOptionValue("payment_setting.amount_options", `[1,2,3.5,5]`))
		require.NoError(t, validateAssistantAdminConfigValue("payment_setting.amount_discount", `{"3.5":0.98}`))
	}
	encoded, err := json.Marshal(payment.AmountOptions)
	require.NoError(t, err)
	require.Equal(t, `[1,2,3.5,5,10,20,50,100]`, string(encoded))
	anchor, err := common.CreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "500000", anchor.String())
}

func TestTopUpDecimalPresetsRejectFractionalCreditWithoutTruncation(t *testing.T) {
	preserveTopUpCreditMetadataConfig(t)
	for _, amount := range []string{"0.000001", "3.5000000000000000001"} {
		operation_setting.GetPaymentSetting().AmountOptions = operation_setting.PaymentAmountOptions{json.Number(amount)}
		_, err := topUpCreditMetadata(nil)
		require.Error(t, err, amount)
		require.Error(t, model.ValidateOptionValue("payment_setting.amount_options", "["+amount+"]"))
	}
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	operation_setting.GetPaymentSetting().AmountOptions = operation_setting.PaymentAmountOptions{"1750000"}
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{"1750000": 0.98}
	metadata, err := topUpCreditMetadata(nil)
	require.NoError(t, err)
	require.Equal(t, []int64{1750000}, metadata["credit_amount_options"])
	require.Equal(t, []float64{3.5}, legacyTopUpPresetOptions())
	require.Equal(t, map[string]float64{"3.5": 0.98}, legacyTopUpDiscountOptions())
	_, paid := applyTopUpSettlementRatiosWithOriginal(decimal.NewFromInt(100), topUpConfigAmountFromLegacy(decimal.RequireFromString("3.5")), "default", decimal.NewFromInt(1))
	require.Equal(t, "98", paid.String())
	require.Error(t, model.ValidateOptionValue("payment_setting.amount_discount", `{"3.5":0.98}`))
}
