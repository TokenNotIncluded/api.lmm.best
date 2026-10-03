package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupHeroSMSSMSOrderLookupControllerTest(t *testing.T) (*gorm.DB, *gin.Engine, *atomic.Int32) {
	t.Helper()
	db := setupHeroSMSControllerTestDB(t)
	users := []model.User{
		{Id: 9021, Username: "sms-lookup-caller", Password: "fixture-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: common.GetTrustQuota(), Group: "default", AffCode: "sms-lookup-caller"},
		{Id: 9022, Username: "sms-lookup-owner", Password: "fixture-password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: common.GetTrustQuota(), Group: "default", AffCode: "sms-lookup-owner"},
	}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, model.UpdateHeroSMSSettings(model.HeroSMSSettingsUpdate{
		Enabled: ptrBoolForController(true), SMSEnabled: ptrBoolForController(true), APIKey: "sms-lookup-fixture-key",
	}))
	providerID := "sms-lookup-private-provider-id"
	order := model.HeroSMSSMSOrder{
		ID: "sms-lookup-other-user-order", UserID: users[1].Id,
		IdempotencyKeyHash: "sms-lookup-idempotency", RequestPayloadHash: "sms-lookup-payload",
		Status: model.HeroSMSSMSOrderStatusActive, Service: "tg", CustomerPriceUSD: "1", ChargeQuota: 1,
		ProviderID: &providerID, CreatedAt: time.Now().Add(-3 * time.Minute).Unix(),
	}
	require.NoError(t, db.Create(&order).Error)
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(provider.Close)
	t.Cleanup(model.SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(provider.URL, "fixture") }, provider.URL,
	))
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("id", users[0].Id) })
	engine.GET("/sms/orders/:id", GetHeroSMSSMSOrder)
	engine.POST("/sms/orders/:id/cancel", CancelHeroSMSSMSOrder)
	engine.POST("/sms/orders/:id/complaints", SubmitHeroSMSSMSComplaint)
	engine.DELETE("/sms/history/:id", HideHeroSMSSMSOrderFromHistory)
	return db, engine, &providerCalls
}

func heroSMSSMSOrderLookupRequests() []struct {
	name   string
	method string
	path   string
	body   string
} {
	return []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "refresh", method: http.MethodGet, path: "/sms/orders/%s"},
		{name: "cancel", method: http.MethodPost, path: "/sms/orders/%s/cancel"},
		{name: "complaint", method: http.MethodPost, path: "/sms/orders/%s/complaints", body: `{"reason":"SMS_NOT_RECEIVED"}`},
		{name: "hide_history", method: http.MethodDelete, path: "/sms/history/%s"},
	}
}

func TestHeroSMSSMSOrderLookupNotFoundDoesNotExposeOtherOrders(t *testing.T) {
	db, engine, providerCalls := setupHeroSMSSMSOrderLookupControllerTest(t)
	var beforeUsers []model.User
	var beforeOrders []model.HeroSMSSMSOrder
	require.NoError(t, db.Order("id").Find(&beforeUsers).Error)
	require.NoError(t, db.Order("id").Find(&beforeOrders).Error)
	for _, endpoint := range heroSMSSMSOrderLookupRequests() {
		for _, orderID := range []string{"sms-lookup-missing-order", beforeOrders[0].ID} {
			t.Run(endpoint.name+"/"+orderID, func(t *testing.T) {
				path := fmt.Sprintf(endpoint.path, orderID)
				request := httptest.NewRequest(endpoint.method, path, strings.NewReader(endpoint.body))
				request.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, request)

				var afterUsers []model.User
				var afterOrders []model.HeroSMSSMSOrder
				require.NoError(t, db.Order("id").Find(&afterUsers).Error)
				require.NoError(t, db.Order("id").Find(&afterOrders).Error)
				require.Equal(t, beforeUsers, afterUsers)
				require.Equal(t, beforeOrders, afterOrders)
				var ledgerCount int64
				require.NoError(t, db.Model(&model.HeroSMSSMSQuotaLedger{}).Count(&ledgerCount).Error)
				require.Zero(t, ledgerCount)
				require.Zero(t, providerCalls.Load())
				require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
				require.JSONEq(t, `{"success":false,"code":"ORDER_NOT_FOUND","message":"HeroSMS SMS order not found"}`, response.Body.String())
			})
		}
	}
}

func TestHeroSMSSMSOrderLookupDatabaseFailureRemainsServerError(t *testing.T) {
	db, engine, providerCalls := setupHeroSMSSMSOrderLookupControllerTest(t)
	var beforeUsers []model.User
	require.NoError(t, db.Order("id").Find(&beforeUsers).Error)
	require.NoError(t, db.Migrator().DropTable(&model.HeroSMSSMSOrder{}))
	for _, endpoint := range heroSMSSMSOrderLookupRequests() {
		t.Run(endpoint.name, func(t *testing.T) {
			path := fmt.Sprintf(endpoint.path, "sms-lookup-other-user-order")
			request := httptest.NewRequest(endpoint.method, path, strings.NewReader(endpoint.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
			require.JSONEq(t, `{"success":false,"code":"INTERNAL_ERROR","message":"HeroSMS operation failed"}`, response.Body.String())
			require.Zero(t, providerCalls.Load())
			var afterUsers []model.User
			require.NoError(t, db.Order("id").Find(&afterUsers).Error)
			require.Equal(t, beforeUsers, afterUsers)
			var ledgerCount int64
			require.NoError(t, db.Model(&model.HeroSMSSMSQuotaLedger{}).Count(&ledgerCount).Error)
			require.Zero(t, ledgerCount)
		})
	}
}
