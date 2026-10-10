package operation_setting

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestPaymentDecimalCatalogLoadsPersistedFractionAtomically(t *testing.T) {
	payment := PaymentSetting{AmountOptions: PaymentAmountOptions{"10", "20"}, AmountDiscount: PaymentAmountDiscount{"10": 0.9}}
	require.NoError(t, config.UpdateConfigFromMap(&payment, map[string]string{
		"amount_options":  `[1,2,3.5,5,10,20,50,100]`,
		"amount_discount": `{"1":1,"2":0.99,"5":0.97,"10":0.96,"20":0.94,"50":0.92,"100":0.9,"3.5":0.98}`,
	}))
	require.Equal(t, PaymentAmountOptions{"1", "2", "3.5", "5", "10", "20", "50", "100"}, payment.AmountOptions)
	require.Equal(t, 0.98, payment.AmountDiscount["3.5"])
	require.NotContains(t, payment.AmountDiscount, "3")
	encoded, err := config.ConfigToMap(&payment)
	require.NoError(t, err)
	require.JSONEq(t, `[1,2,3.5,5,10,20,50,100]`, encoded["amount_options"])
	require.JSONEq(t, `{"1":1,"2":0.99,"5":0.97,"10":0.96,"20":0.94,"50":0.92,"100":0.9,"3.5":0.98}`, encoded["amount_discount"])
	before := append(PaymentAmountOptions{}, payment.AmountOptions...)
	for _, invalid := range []string{`[1,true,3.5]`, `[1,"3.5"]`, `[1,null]`, `null`, `[1,0]`, `[1,1e19]`} {
		require.Error(t, json.Unmarshal([]byte(invalid), &payment.AmountOptions), invalid)
		require.Equal(t, before, payment.AmountOptions, "invalid load must not mutate any existing entry")
	}
	require.NoError(t, config.UpdateConfigFromMap(&payment, map[string]string{"amount_options": `[1,false,3.5]`}))
	require.Equal(t, before, payment.AmountOptions, "config manager's ignored decode error must not leave partial zeros")
}

func TestPaymentDecimalCatalogExactCreditDomain(t *testing.T) {
	previous := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	for _, tc := range []struct {
		text   string
		credit int64
	}{
		{"3.5", 1750000}, {"2e-6", 1}, {"1.0", 500000}, {"2e1", 10000000},
		{"18014398509.481982", common.MaxWalletQuota},
	} {
		amount, err := ParsePaymentAmount(tc.text)
		require.NoError(t, err)
		credit, err := PaymentConfigAmountCredit(amount, false)
		require.NoError(t, err)
		require.Equal(t, tc.credit, credit)
	}
	for _, text := range []string{"0.000001", "3.5000000000000000001", "18014398509.481984", "9007199254740991.1"} {
		amount, err := ParsePaymentAmount(text)
		if err == nil {
			_, err = PaymentConfigAmountCredit(amount, false)
		}
		require.Error(t, err, text)
	}
	require.NoError(t, ValidatePaymentCatalogJSON("payment_setting.amount_options", `[500000,1750000]`, true))
	require.Error(t, ValidatePaymentCatalogJSON("payment_setting.amount_options", `[3.5]`, true))
	require.Error(t, ValidatePaymentCatalogJSON("payment_setting.amount_options", `[9007199254740991.1]`, true))
	require.NoError(t, ValidatePaymentCatalogJSON("payment_setting.amount_options", `[1,2,3.5]`, false))
	require.Error(t, ValidatePaymentCatalogJSON("payment_setting.amount_options", `[1,1.0]`, false))
	require.NoError(t, ValidatePaymentCatalogJSON("payment_setting.amount_options", `[]`, false))
	require.True(t, decimal.NewFromInt(500000).Equal(decimal.NewFromFloat(common.QuotaPerUnit)))
}

func TestPaymentDecimalDiscountCanonicalAliasesAndAtomicLoad(t *testing.T) {
	discounts := PaymentAmountDiscount{"10": 0.9}
	require.NoError(t, json.Unmarshal([]byte(`{"3.50":0.98,"3.5":0.98,"+01":1,"1":1}`), &discounts))
	require.Equal(t, PaymentAmountDiscount{"3.5": 0.98, "1": 1}, discounts)
	for _, invalid := range []string{
		`{"3.5":0.98,"3.50":0.97}`, `{"3.5":0.98,"3.5":0.97}`,
		`{"3.5":"0.98"}`, `{"3.5":null}`, `{"3.5":0}`, `{"3.5":1.01}`, `null`, `{} true`,
	} {
		require.Error(t, json.Unmarshal([]byte(invalid), &discounts), invalid)
		require.Equal(t, PaymentAmountDiscount{"3.5": 0.98, "1": 1}, discounts)
	}
	require.NoError(t, json.Unmarshal([]byte(`{}`), &discounts))
	require.Empty(t, discounts, "explicit removal must clear prior discount keys")
}
