package controller

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func installPublicMoneyCurrencyFixture(t *testing.T) {
	t.Helper()
	anchor, priorErr := common.CreditsPerUSD()
	legacy, _ := common.LegacyPricingQuotaPerUnit()
	oldQ, oldFX := common.QuotaPerUnit, operation_setting.USDExchangeRate
	oldGroup := operation_setting.GetPublicRelaySetting().Group
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate = oldQ, oldFX
		operation_setting.GetPublicRelaySetting().Group = oldGroup
		if priorErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(anchor, legacy))
		}
	})
	common.QuotaPerUnit, operation_setting.USDExchangeRate = 500000, 7
	operation_setting.GetPublicRelaySetting().Group = "FREE"
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
}

func TestPublicRelayConfigRealUSDAndPreservedRawThreshold(t *testing.T) {
	installPublicMoneyCurrencyFixture(t)
	invoke := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/public-relays/config", nil)
		GetPublicRelayConfig(c)
		return w
	}
	w := invoke()
	require.Equal(t, http.StatusOK, w.Code)
	var response struct {
		Data struct {
			Minimum    int64   `json:"minimum_withdrawal_quota"`
			Maximum    int64   `json:"maximum_tip_quota"`
			MinimumUSD float64 `json:"minimum_withdrawal_usd"`
			MaximumUSD float64 `json:"maximum_tip_usd"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.EqualValues(t, 5000000, response.Data.Minimum)
	require.EqualValues(t, 50000000, response.Data.Maximum)
	require.InDelta(t, 10.0/7, response.Data.MinimumUSD, 1e-14)
	require.InDelta(t, 100.0/7, response.Data.MaximumUSD, 1e-14)
	operation_setting.USDExchangeRate = 9.9
	require.JSONEq(t, w.Body.String(), invoke().Body.String())
	common.ClearCreditsPerUSD()
	require.Equal(t, http.StatusServiceUnavailable, invoke().Code)
}

func TestPublicRelayTipChargesRealUSDAndRejectsUnsafeMoney(t *testing.T) {
	installPublicMoneyCurrencyFixture(t)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.PublicRelayContribution{}, &model.PublicRelayTip{}, &model.PublicRelayPreference{}, &model.Log{}))
	owner := model.User{Username: "real-usd-relay-owner", AffCode: "real-usd-owner", Group: "default"}
	tipper := model.User{Username: "real-usd-relay-tipper", AffCode: "real-usd-tipper", Group: "default", Quota: 20000000}
	require.NoError(t, db.Create(&owner).Error)
	require.NoError(t, db.Create(&tipper).Error)
	contribution := model.PublicRelayContribution{UserId: owner.Id, Name: "real usd", Group: "FREE", Status: model.PublicRelayApproved, ChannelId: 1}
	require.NoError(t, db.Create(&contribution).Error)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set("id", tipper.Id); c.Next() })
	router.POST("/api/public-relays/:id/tip", TipPublicRelay)
	router.GET("/api/public-relays", ListPublicRelayContributions)
	router.GET("/api/public-relays/routing", GetPublicRelayRouting)
	router.GET("/api/public-relays/mine", ListMyPublicRelayContributions)
	router.GET("/api/admin/public-relays", ListAdminPublicRelayContributions)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, r)
		return w
	}
	path := "/api/public-relays/" + common.GetJsonString(contribution.Id) + "/tip"
	w := request(http.MethodPost, path, `{"amount_usd":1,"message":"thanks"}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"quota":3500000`)
	var current model.User
	require.NoError(t, db.First(&current, tipper.Id).Error)
	require.Equal(t, 16500000, current.Quota)
	operation_setting.USDExchangeRate = 9.9
	w = request(http.MethodPost, path, `{"amount_usd":1}`)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, db.First(&current, tipper.Id).Error)
	require.Equal(t, 13000000, current.Quota)
	for _, body := range []string{`{"amount_usd":1e100}`, `{"amount_usd":1e999}`, `{"amount_usd":0.0000000001}`, `{"amount_usd":-1}`, `{"amount_usd":"1"}`, `{"amount_usd":15}`} {
		require.Equal(t, http.StatusUnprocessableEntity, request(http.MethodPost, path, body).Code, body)
	}
	require.NoError(t, db.First(&current, tipper.Id).Error)
	require.Equal(t, 13000000, current.Quota)
	var rows []model.PublicRelayTip
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 2)
	require.EqualValues(t, 3500000, rows[0].Quota)
	require.EqualValues(t, 3500000, rows[1].Quota)
	list := request(http.MethodGet, "/api/public-relays", "")
	require.Equal(t, http.StatusOK, list.Code)
	require.Contains(t, list.Body.String(), `"tip_quota_usd":2`)
	routing := request(http.MethodGet, "/api/public-relays/routing", "")
	require.Equal(t, http.StatusOK, routing.Code)
	require.Contains(t, routing.Body.String(), `"tip_quota_usd":2`)
	common.ClearCreditsPerUSD()
	require.Equal(t, http.StatusServiceUnavailable, request(http.MethodPost, path, `{"amount_usd":1}`).Code)
	require.Equal(t, http.StatusServiceUnavailable, request(http.MethodGet, "/api/public-relays", "").Code)
	require.Equal(t, http.StatusServiceUnavailable, request(http.MethodGet, "/api/public-relays/routing", "").Code)
	require.Equal(t, http.StatusServiceUnavailable, request(http.MethodGet, "/api/public-relays/mine", "").Code)
	require.Equal(t, http.StatusServiceUnavailable, request(http.MethodGet, "/api/admin/public-relays", "").Code)
	require.NoError(t, db.First(&current, tipper.Id).Error)
	require.Equal(t, 13000000, current.Quota)
}

func TestProfileShareModelsMoneyUsesRealUSDAndLanguageCurrency(t *testing.T) {
	installPublicMoneyCurrencyFixture(t)
	for _, tc := range []struct{ lang, currency, displayed string }{
		{"en", "", "1.0000 USD"}, {"zh", "", "7.0000 CNY"}, {"zh-TW", "", "7.0000 CNY"},
		{"zh", "USD", "1.0000 USD"}, {"en", "CNY", "7.0000 CNY"}, {"en", "CREDIT", "3500000 Credit"},
	} {
		options, _, err := parseProfileShareModelsSVGOptions(url.Values{"layout": {"models"}, "lang": {tc.lang}, "currency": {tc.currency}})
		require.NoError(t, err)
		svg, err := renderProfileShareModelsSVG(options, model.ProfileShareModelUsage{Quota: 3500000}, 1, 86400)
		require.NoError(t, err)
		require.Contains(t, svg, tc.displayed)
		require.NotContains(t, svg, "($)")
	}
	_, _, err := parseProfileShareModelsSVGOptions(url.Values{"layout": {"models"}, "currency": {"EUR"}})
	require.Error(t, err)
	operation_setting.USDExchangeRate = 9.9
	usd, err := profileShareModelQuota(3500000, "USD")
	require.NoError(t, err)
	require.Equal(t, "1.0000 USD", usd)
	cny, err := profileShareModelQuota(3500000, "CNY")
	require.NoError(t, err)
	require.Equal(t, "9.9000 CNY", cny)
	for _, invalid := range []float64{0, math.NaN(), math.Inf(1)} {
		operation_setting.USDExchangeRate = invalid
		_, err = profileShareModelQuota(3500000, "CNY")
		require.Error(t, err)
	}
	operation_setting.USDExchangeRate = 7
	common.ClearCreditsPerUSD()
	options, _, err := parseProfileShareModelsSVGOptions(url.Values{"layout": {"models"}})
	require.NoError(t, err)
	svg, err := renderProfileShareModelsSVG(options, model.ProfileShareModelUsage{Quota: 3500000}, 1, 86400)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	require.Empty(t, svg)
}

func TestPublicRelayTipPreservesExactDecimalAtHalfCreditBoundary(t *testing.T) {
	installPublicMoneyCurrencyFixture(t)
	for _, tc := range []struct {
		amount string
		quota  int
	}{
		{"0.000000142857142857142857142857142857", 0},
		{"0.000000142857142857142857142857142858", 1},
		{"1.000000142857142857142857142857142857", 3500000},
		{"1.000000142857142857142857142857142858", 3500001},
	} {
		var input publicRelayTipInput
		require.NoError(t, json.Unmarshal([]byte(`{"amount_usd":`+tc.amount+`}`), &input))
		credits, err := common.FiatToCreditsDecimal(input.amountUSDDecimal, "USD", decimal.Zero)
		require.NoError(t, err)
		quota, err := common.WalletQuotaFromDecimalStrict(credits)
		require.NoError(t, err)
		require.Equal(t, tc.quota, quota)
	}
	for _, invalid := range []string{`0e999999999`, `1e999999999`, `1e-999999999`, `"1"`, `null`, `0`} {
		var input publicRelayTipInput
		require.Error(t, json.Unmarshal([]byte(`{"amount_usd":`+invalid+`}`), &input))
	}
}
