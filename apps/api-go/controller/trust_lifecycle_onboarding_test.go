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

func TestTrustLifecycleSelfUsesTheActivationPolicyThatGrantsAccess(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(common.FixedCreditsPerUSD), decimal.NewFromInt(common.FixedCreditsPerUSD)))
	legacy := operation_setting.GetDeveloperAccessSetting()
	oldLegacy := *legacy
	legacy.PaidActivationEnabled, legacy.PaidActivationMinAmount = true, 999
	t.Cleanup(func() { *legacy = oldLegacy })
	common.OptionMapRWMutex.Lock()
	oldOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = oldOptions
		common.OptionMapRWMutex.Unlock()
	})
	config := model.GetTrustLevelConfiguration()
	for level, credits := range []int64{0, 500001, 1000001, 1500001, 2000001} {
		config.Tiers[level].MinPaidCredits = credits
	}
	config.DecayPeriodDays = 0
	publish := func(enabled bool) {
		t.Helper()
		config.PaidActivationEnabled = enabled
		raw, err := json.Marshal(config)
		require.NoError(t, err)
		common.OptionMapRWMutex.Lock()
		common.OptionMap[model.TrustLevelBenefitsOptionKey] = string(raw)
		common.OptionMapRWMutex.Unlock()
	}
	user := model.User{Username: "trust-self-policy", AffCode: "trust-self-policy", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.TopUp{UserId: user.Id, TradeNo: "trust-self-below", CreditedQuota: 500000,
		PaymentProvider: model.PaymentProviderStripe, Money: 1, Status: common.TopUpStatusSuccess}).Error)
	router := gin.New()
	router.GET("/api/user/self", func(c *gin.Context) { c.Set("id", user.Id); GetSelf(c) })
	readSelf := func() map[string]any {
		t.Helper()
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/user/self", nil))
		require.Equal(t, http.StatusOK, w.Code)
		var response struct {
			Success bool           `json:"success"`
			Data    map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		require.True(t, response.Success)
		return response.Data
	}
	for _, enabled := range []bool{false, true} {
		publish(enabled)
		data := readSelf()
		onboarding := data["onboarding"].(map[string]any)
		require.Equal(t, enabled, onboarding["paid_activation_enabled"])
		require.Equal(t, "500001", onboarding["paid_activation_min_credits"])
		require.Equal(t, 1.000002, onboarding["paid_activation_min_amount"])
		require.Equal(t, 1.000002, onboarding["paid_activation_min_amount_usd"])
		require.Equal(t, false, data["developer_access_granted"])
	}
	require.NoError(t, db.Create(&model.TopUp{UserId: user.Id, TradeNo: "trust-self-boundary", CreditedQuota: 1,
		PaymentProvider: model.PaymentProviderStripe, Money: 0.01, Status: common.TopUpStatusSuccess}).Error)
	data := readSelf()
	require.Equal(t, true, data["developer_access_granted"])
	require.Equal(t, float64(1), data["trust_level_info"].(map[string]any)["level"])
	onboarding := data["onboarding"].(map[string]any)
	require.Equal(t, true, onboarding["paid_activation_complete"])
	require.Equal(t, "500001", onboarding["paid_activation_min_credits"])
	require.Equal(t, float64(999), legacy.PaidActivationMinAmount, "the new policy must not rewrite the legacy setting")
}
