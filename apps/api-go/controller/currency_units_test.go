package controller

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func installControllerCreditAnchor(t *testing.T, anchor int64) {
	t.Helper()
	previous, err := common.CreditsPerUSD()
	t.Cleanup(func() {
		if err != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(previous))
		}
	})
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(anchor)))
}

func installStatusCurrencyFixture(t *testing.T) {
	t.Helper()
	installControllerCreditAnchor(t, 500000)
	previous := assistantConfiguredRouteResolver
	previousPricing := getPricingCache
	getPricingCache = func() []model.Pricing { return nil }
	assistantConfiguredRouteResolver = func(settings setting.AssistantSettings) (string, string, error) {
		return settings.Group, settings.Model, nil
	}
	t.Cleanup(func() { assistantConfiguredRouteResolver = previous; getPricingCache = previousPricing })
	setupTokenControllerTestDB(t)
}

func TestStatusCreditMetadataAndUnavailableFailsClosed(t *testing.T) {
	installStatusCurrencyFixture(t)
	installControllerCreditAnchor(t, 500000)
	persistCreditDenominationFixture(t, model.DB)
	preserveCacheRuntimeHooks(t)
	cacheReadinessError = func() error { return nil }
	oldQ, oldFX, oldB := common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 500000, 7, 1.25
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = oldQ, oldFX, oldB
	})
	query := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/status", nil)
		GetStatus(c)
		return w
	}
	w := query()
	require.Equal(t, 200, w.Code)
	var body struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "credit", body.Data["currency_unit"])
	require.Equal(t, float64(500000), body.Data["credits_per_usd"])
	require.Equal(t, float64(500000), body.Data["quota_per_usd"])
	require.Equal(t, float64(7), body.Data["cny_per_usd"])
	require.Equal(t, 1.0, body.Data["legacy_pricing_units_per_usd"])
	operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 8, 3
	w = query()
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, float64(500000), body.Data["credits_per_usd"])
	require.Equal(t, 1.0, body.Data["price"], "legacy compatibility field is fixed bridge, not future recharge rate")
	require.Equal(t, float64(8), body.Data["cny_per_usd"])
	for _, bad := range []float64{0, math.NaN(), math.Inf(1)} {
		operation_setting.USDExchangeRate = bad
		w = query()
		require.Equal(t, 503, w.Code)
		require.NotContains(t, w.Body.String(), "credits_per_usd")
	}
	operation_setting.USDExchangeRate = 7
	common.ClearCreditsPerUSD()
	w = query()
	require.Equal(t, 503, w.Code)
	require.NotContains(t, w.Body.String(), "quota_per_usd")
}

func TestWalletDisplayPreferenceOwnerScopedAndIndependent(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, i18n.Init())
	user := model.User{Username: "wallet-display-owner", Quota: 123456, Status: common.UserStatusEnabled, Setting: `{"language":"zh","settlement_currency":"CNY","future_setting":{"keep":1}}`}
	other := model.User{Username: "wallet-display-other", AffCode: "other-wallet-display", Status: common.UserStatusEnabled, Setting: `{"wallet_display_currency":"CNY"}`}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&other).Error)
	router := gin.New()
	router.PUT("/api/user/self", func(c *gin.Context) { c.Set("id", user.Id); c.Next() }, UpdateSelf)
	patch := func(body string) bool {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		var result struct {
			Success bool `json:"success"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		return result.Success
	}
	load := func() model.User {
		var current model.User
		require.NoError(t, db.First(&current, user.Id).Error)
		return current
	}
	require.True(t, patch(`{"wallet_display_currency":" credit ","user_id":`+common.GetJsonString(other.Id)+`}`))
	current := load()
	require.Equal(t, "CREDIT", current.GetSetting().EffectiveWalletDisplayCurrency(""))
	require.Equal(t, "CNY", current.GetSetting().SettlementCurrency)
	require.True(t, patch(`{"language":"en"}`))
	current = load()
	require.Equal(t, "CREDIT", current.GetSetting().WalletDisplayCurrency)
	require.Contains(t, current.Setting, `"future_setting":{"keep":1}`)
	require.Equal(t, user.Quota, current.Quota)
	before := current.Setting
	for _, invalid := range []string{`{"wallet_display_currency":"EUR"}`, `{"wallet_display_currency":null}`, `{"wallet_display_currency":123}`, `{"language":"zh","wallet_display_currency":"TOKENS"}`, `{"settlement_currency":"CREDIT","wallet_display_currency":"USD"}`} {
		require.False(t, patch(invalid))
		require.Equal(t, before, load().Setting)
	}
	require.NoError(t, model.UpdateUserSettingPreservingLocale(user.Id, dto.UserSetting{WalletDisplayCurrency: "USD", Language: "zh", SettlementCurrency: "USD", RecordIpLog: true}))
	current = load()
	require.Equal(t, "CREDIT", current.GetSetting().WalletDisplayCurrency)
	require.Equal(t, "en", current.GetSetting().Language)
	require.Equal(t, "CNY", current.GetSetting().SettlementCurrency)
	require.True(t, patch(`{"wallet_display_currency":"","language":"zh-TW"}`))
	current = load()
	require.Equal(t, "CNY", current.GetSetting().EffectiveWalletDisplayCurrency("en"))
	require.NoError(t, db.First(&other, other.Id).Error)
	require.Equal(t, `{"wallet_display_currency":"CNY"}`, other.Setting)
}

func TestBillingQueriesAlwaysTrueUSDIndependentOfDisplay(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	installControllerCreditAnchor(t, 500000)
	oldQ, oldFX, oldToken := common.QuotaPerUnit, operation_setting.USDExchangeRate, common.DisplayTokenStatEnabled
	oldDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	common.QuotaPerUnit, common.DisplayTokenStatEnabled = 500000, true
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate, common.DisplayTokenStatEnabled = oldQ, oldFX, oldToken
		operation_setting.GetGeneralSetting().QuotaDisplayType = oldDisplay
	})
	user := model.User{Username: "true-usd-billing", Status: common.UserStatusEnabled, Quota: 999999}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "true-usd-billing", RemainQuota: 4375000, UsedQuota: 8750000, Status: common.TokenStatusEnabled, ExpiredTime: -1}
	require.NoError(t, db.Create(&token).Error)
	query := func(handler gin.HandlerFunc) map[string]any {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/billing", nil)
		c.Set("id", user.Id)
		c.Set("token_id", token.Id)
		handler(c)
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return body
	}
	for _, display := range []string{"USD", "CNY", "TOKENS", "CUSTOM"} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = display
		for _, fx := range []float64{7, 8, 99} {
			operation_setting.USDExchangeRate = fx
			require.Equal(t, float64(26.25), query(GetSubscription)["hard_limit_usd"])
			require.Equal(t, float64(1750), query(GetUsage)["total_usage"], "OpenAI usage uses real USD cents")
			c := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(c)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
			ctx.Set("quota_query_token", &token)
			GetQuotaQuery(ctx)
			var body map[string]any
			require.NoError(t, json.Unmarshal(c.Body.Bytes(), &body))
			require.Equal(t, float64(8.75), body["remaining"])
			require.Equal(t, float64(17.5), body["used_total"])
		}
	}
	var after model.Token
	require.NoError(t, db.First(&after, token.Id).Error)
	require.Equal(t, token, after)
	common.ClearCreditsPerUSD()
	require.Contains(t, query(GetUsage), "error")
	require.Contains(t, query(GetSubscription), "error")
}
