package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHeroSMSPageEntryAndPurchaseWithInsufficientBalance(t *testing.T) {
	db := setupHeroSMSControllerTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	user := model.User{Id: 771, Username: "sms-low-balance", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 0, Group: "default", AffCode: "sms-low-balance"}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, model.UpdateHeroSMSSettings(model.HeroSMSSettingsUpdate{Enabled: ptrBoolForController(true), SMSEnabled: ptrBoolForController(true), APIKey: "local-fixture-key", PriceMultiplier: "1"}))
	var purchases atomic.Int32
	var providerCalls atomic.Int32
	var providerOffline atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		if providerOffline.Load() {
			http.Error(w, "provider offline", http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/api/v1/activations/offers/sms" {
			_, _ = w.Write([]byte(`{"data":{"tg":{"6":{"counts":{"total":5,"defaultPrice":5},"prices":{"default":1},"map":{"1":5}}}}}`))
			return
		}
		if r.URL.Query().Get("action") == "getNumberV2" {
			purchases.Add(1)
		}
		http.Error(w, "unexpected provider operation", http.StatusBadRequest)
	}))
	t.Cleanup(provider.Close)
	t.Cleanup(model.SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(provider.URL+"/api/v1", "fixture") }, provider.URL+"/api/v1"))
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", user.Id); c.Next() })
	engine.GET("/api/hero-sms/sms/orders/current-list", ListCurrentHeroSMSSMSOrders)
	engine.GET("/api/hero-sms/sms/orders", ListHeroSMSSMSOrders)
	engine.GET("/api/hero-sms/sms/offer", GetHeroSMSSMSOffer)
	engine.POST("/api/hero-sms/sms/orders", CreateHeroSMSSMSOrder)
	engine.GET("/api/hero-sms/sms/countries", ListHeroSMSSMSCountries)
	engine.GET("/api/hero-sms/sms/services", ListHeroSMSSMSServices)
	engine.GET("/api/hero-sms/sms/operators", ListHeroSMSSMSOperators)
	for _, path := range []string{"/api/hero-sms/sms/orders/current-list", "/api/hero-sms/sms/orders?page=1&size=50"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), `"items":[]`)
	}

	for _, path := range []string{"/api/hero-sms/sms/countries", "/api/hero-sms/sms/services", "/api/hero-sms/sms/operators?country=6", "/api/hero-sms/sms/offer?country=6&service=tg"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusPaymentRequired, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "TEMPORARY_SMS_MINIMUM_BALANCE")
	}
	require.Zero(t, providerCalls.Load())
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", common.GetTrustQuota()).Error)
	quote := httptest.NewRecorder()
	engine.ServeHTTP(quote, httptest.NewRequest(http.MethodGet, "/api/hero-sms/sms/offer?country=6&service=tg", nil))
	require.Equal(t, http.StatusOK, quote.Code, quote.Body.String())
	var data struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(quote.Body.Bytes(), &data))
	require.NotEmpty(t, data.Data.ID)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", 0).Error)
	providerOffline.Store(true)
	callsBeforePurchase := providerCalls.Load()
	request := httptest.NewRequest(http.MethodPost, "/api/hero-sms/sms/orders", strings.NewReader(fmt.Sprintf(`{"offer_id":%q}`, data.Data.ID)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "low-balance-http")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusPaymentRequired, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "TEMPORARY_SMS_MINIMUM_BALANCE")
	require.Contains(t, response.Body.String(), "USD 10")
	require.NotContains(t, response.Body.String(), "INTERNAL_ERROR")
	require.Zero(t, purchases.Load())
	require.Equal(t, callsBeforePurchase, providerCalls.Load())
	var current model.User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Zero(t, current.Quota)
	var ledgerCount int64
	require.NoError(t, db.Model(&model.HeroSMSSMSQuotaLedger{}).Count(&ledgerCount).Error)
	require.Zero(t, ledgerCount)

	// Sufficient funds must expose an upstream problem as such, without charging.
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", user.Id).Update("quota", common.GetTrustQuota()).Error)
	upstreamFailure := httptest.NewRecorder()
	engine.ServeHTTP(upstreamFailure, httptest.NewRequest(http.MethodGet, "/api/hero-sms/sms/offer?country=6&service=tg", nil))
	require.Equal(t, http.StatusBadGateway, upstreamFailure.Code, upstreamFailure.Body.String())
	require.Contains(t, upstreamFailure.Body.String(), "UPSTREAM_BUSY")
	require.NotContains(t, upstreamFailure.Body.String(), "local-fixture-key")
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, common.GetTrustQuota(), current.Quota)
	require.Zero(t, purchases.Load())
}
