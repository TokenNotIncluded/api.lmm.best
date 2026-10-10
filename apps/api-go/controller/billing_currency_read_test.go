package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func billingReadRequest(t *testing.T, token *model.Token, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/dashboard/billing", nil)
	c.Set("id", token.UserId)
	c.Set("token_id", token.Id)
	handler(c)
	require.Equal(t, http.StatusOK, w.Code)
	return w
}

func TestSDKBillingExactUSDZeroSignedAndSingleRounding(t *testing.T) {
	installPublicMoneyCurrencyFixture(t)
	db := setupTokenControllerTestDB(t)
	oldTokenStat, oldDisplay := common.DisplayTokenStatEnabled, operation_setting.GetGeneralSetting().QuotaDisplayType
	common.DisplayTokenStatEnabled = true
	t.Cleanup(func() {
		common.DisplayTokenStatEnabled = oldTokenStat
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay
	})
	user := model.User{Username: "sdk-usd-owner", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "sdk-usd-token", Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, db.Create(&token).Error)
	for _, tc := range []struct {
		anchor         string
		remain, used   int
		dollars, cents float64
	}{
		{"500000", 0, 3500000, 7, 700}, {"500000", 0, 0, 0, 0},
		{"500000", -7000000, 3500000, -7, 700}, {"500000", 0, -3500000, -7, -700},
		{"500000", 0, 1, 0.000002, 0.0002},
		{"500000", 0, -1, -0.000002, -0.0002},
	} {
		require.NoError(t, common.SetCreditCurrencyBasis(decimal.RequireFromString(tc.anchor), decimal.NewFromInt(500000)))
		require.NoError(t, db.Model(&token).UpdateColumns(map[string]any{"remain_quota": tc.remain, "used_quota": tc.used}).Error)
		if tc.used < 0 {
			for _, handler := range []gin.HandlerFunc{GetSubscription, GetUsage} {
				var body map[string]any
				require.NoError(t, json.Unmarshal(billingReadRequest(t, &token, handler).Body.Bytes(), &body))
				require.Equal(t, "billing_unavailable", body["error"].(map[string]any)["type"])
			}
			continue
		}
		for _, display := range []string{"USD", "CNY", "TOKENS", "CUSTOM"} {
			operation_setting.GetGeneralSetting().QuotaDisplayType = display
			for _, fx := range []float64{7, 9.9, 99, 0} {
				operation_setting.USDExchangeRate = fx
				var subscription, usage map[string]any
				require.NoError(t, json.Unmarshal(billingReadRequest(t, &token, GetSubscription).Body.Bytes(), &subscription))
				require.NoError(t, json.Unmarshal(billingReadRequest(t, &token, GetUsage).Body.Bytes(), &usage))
				require.Equal(t, tc.dollars, subscription["soft_limit_usd"])
				require.Equal(t, tc.dollars, subscription["hard_limit_usd"])
				require.Equal(t, tc.dollars, subscription["system_hard_limit_usd"])
				require.Equal(t, tc.cents, usage["total_usage"])
			}
		}
		var unchanged model.Token
		require.NoError(t, db.First(&unchanged, token.Id).Error)
		require.Equal(t, tc.remain, unchanged.RemainQuota)
		require.Equal(t, tc.used, unchanged.UsedQuota)
	}
}

func TestSDKBillingUnavailableBasisAndNonFiniteMoneyKeepErrorShape(t *testing.T) {
	installPublicMoneyCurrencyFixture(t)
	db := setupTokenControllerTestDB(t)
	oldTokenStat := common.DisplayTokenStatEnabled
	common.DisplayTokenStatEnabled = true
	t.Cleanup(func() { common.DisplayTokenStatEnabled = oldTokenStat })
	user := model.User{Username: "sdk-unavailable-owner", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "sdk-unavailable-token", Status: common.TokenStatusEnabled, UsedQuota: 1, ExpiredTime: -1}
	require.NoError(t, db.Create(&token).Error)
	errorJSON := `{"error":{"message":"credit currency units are unavailable","type":"billing_unavailable","param":"","code":null}}`
	common.ClearCreditsPerUSD()
	for _, handler := range []gin.HandlerFunc{GetSubscription, GetUsage} {
		require.JSONEq(t, errorJSON, billingReadRequest(t, &token, handler).Body.String())
	}
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	common.QuotaPerUnit = 700000
	for _, handler := range []gin.HandlerFunc{GetSubscription, GetUsage} {
		require.JSONEq(t, errorJSON, billingReadRequest(t, &token, handler).Body.String())
	}
	common.QuotaPerUnit = 500000
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.RequireFromString("1e-500"), decimal.NewFromInt(500000)))
	for _, handler := range []gin.HandlerFunc{GetSubscription, GetUsage} {
		require.JSONEq(t, errorJSON, billingReadRequest(t, &token, handler).Body.String())
	}
	// USD itself is finite here; converting the usage to SDK cents overflows.
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.RequireFromString("1e-307"), decimal.NewFromInt(500000)))
	require.JSONEq(t, errorJSON, billingReadRequest(t, &token, GetUsage).Body.String())
	// The unlimited sentinel does not depend on an intermediate amount conversion.
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.RequireFromString("1e-500"), decimal.NewFromInt(500000)))
	require.NoError(t, db.Model(&token).Update("unlimited_quota", true).Error)
	var subscription map[string]any
	require.NoError(t, json.Unmarshal(billingReadRequest(t, &token, GetSubscription).Body.Bytes(), &subscription))
	require.Equal(t, float64(100000000), subscription["hard_limit_usd"])
	var unchanged model.Token
	require.NoError(t, db.First(&unchanged, token.Id).Error)
	require.Equal(t, 1, unchanged.UsedQuota)
	require.Zero(t, unchanged.RemainQuota)
}

func TestSDKBillingUSDConversionRejectsLostAmountButAllowsExplicitZero(t *testing.T) {
	for _, quota := range []decimal.Decimal{decimal.RequireFromString("1e-500"), decimal.RequireFromString("-1e-500")} {
		_, err := billingUSDFromCredits(quota, decimal.NewFromInt(3500000))
		require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	}
	zero, err := billingUSDFromCredits(decimal.Zero, decimal.NewFromInt(3500000))
	require.NoError(t, err)
	require.Zero(t, zero)
}

// The conversion helper accepts explicit precision fixtures; HTTP contracts
// always install the platform denomination of 500000 credits per USD.
func TestBillingUSDFromCreditsExplicitPrecisionFixture(t *testing.T) {
	for _, tc := range []struct {
		quota, anchor string
		expected      float64
	}{
		{"866666666666668", "8666666666666667", 0.1000000000000001},
		{"-866666666666668", "8666666666666667", -0.1000000000000001},
	} {
		amount, err := billingUSDFromCredits(decimal.RequireFromString(tc.quota), decimal.RequireFromString(tc.anchor))
		require.NoError(t, err)
		require.Equal(t, tc.expected, amount)
	}
}
