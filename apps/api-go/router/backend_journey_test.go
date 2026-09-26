package router

import (
	"encoding/json"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Uses real registered handlers, a real dashboard session and an isolated DB.
// Payment evidence is synthetic: no provider, SMTP or model request is sent.
func TestBackendJourneyL0CheckoutSettlementKeyAndDailyCheckin(t *testing.T) {
	db := setupOpenSourceBountyAccessRouterTest(t)
	require.NoError(t, db.AutoMigrate(&model.UserSession{}, &model.TopUp{}, &model.Token{}, &model.Checkin{}, &model.Log{}))
	oldLogDB, oldSecret := model.LOG_DB, common.SessionSecret
	oldGlobal, oldCritical, oldTurnstile := common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.TurnstileCheckEnabled
	oldQuota := common.QuotaPerUnit
	oldLocal := model.LocalAcceptanceDeveloperAccessEnabled()
	access := operation_setting.GetDeveloperAccessSetting()
	oldAccess := *access
	checkin := operation_setting.GetCheckinSetting()
	oldCheckin := *checkin
	payment := operation_setting.GetPaymentSetting()
	oldPayment := *payment
	model.LOG_DB, common.SessionSecret = db, "isolated-backend-journey-session-test"
	common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.TurnstileCheckEnabled = false, false, false
	common.QuotaPerUnit = 500000
	model.SetLocalAcceptanceDeveloperAccess(false)
	access.PaidActivationEnabled, access.PaidActivationMinAmount = true, 1
	payment.ComplianceConfirmed = false
	*checkin = operation_setting.CheckinSetting{Enabled: true, MinQuota: 10, MaxQuota: 10, LevelMultipliers: []float64{1, 1, 1, 1, 1}}
	t.Cleanup(func() {
		model.LOG_DB, common.SessionSecret = oldLogDB, oldSecret
		common.GlobalApiRateLimitEnable, common.CriticalRateLimitEnable, common.TurnstileCheckEnabled = oldGlobal, oldCritical, oldTurnstile
		common.QuotaPerUnit = oldQuota
		model.SetLocalAcceptanceDeveloperAccess(oldLocal)
		*access, *checkin, *payment = oldAccess, oldCheckin, oldPayment
	})
	user := model.User{Username: "journey-l0", Password: "unused", AffCode: "journey-l0-aff", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1}
	other := model.User{Username: "journey-other", Password: "unused", AffCode: "journey-other-aff", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&other).Error)
	bundle, err := service.CreateLoginSession(user.Id, "password", "192.0.2.98", "backend-journey-test")
	require.NoError(t, err)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	request := func(method, path, body string, authenticated bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.Header.Set("Authorization", "Bearer "+bundle.AccessToken)
		}
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		return res
	}
	decode := func(res *httptest.ResponseRecorder) map[string]interface{} {
		var payload map[string]interface{}
		require.NoError(t, json.Unmarshal(res.Body.Bytes(), &payload), res.Body.String())
		return payload
	}
	info := request(http.MethodGet, "/api/user/topup/info", "", true)
	require.Equal(t, http.StatusOK, info.Code, info.Body.String())
	assert.Contains(t, info.Body.String(), `"activation_required":true`)
	for _, path := range []string{"/api/user/topup/info", "/api/user/topup/self"} {
		assert.Equal(t, http.StatusUnauthorized, request(http.MethodGet, path, "", false).Code, path)
	}
	for _, path := range []string{
		"/api/user/amount", "/api/user/pay", "/api/user/stripe/amount", "/api/user/stripe/pay", "/api/user/creem/pay",
		"/api/user/waffo/amount", "/api/user/waffo/pay", "/api/user/waffo-pancake/amount", "/api/user/waffo-pancake/pay", "/api/user/discount-code/validate",
	} {
		oversized := `{"padding":"` + strings.Repeat("x", topUpMutationRequestMaxBytes) + `"}`
		res := request(http.MethodPost, path, oversized, true)
		assert.Equal(t, http.StatusRequestEntityTooLarge, res.Code, path+": "+res.Body.String())
		assert.Equal(t, http.StatusUnauthorized, request(http.MethodPost, path, `{}`, false).Code, path)
	}
	email := request(http.MethodPost, "/api/verify/email", "{}", true)
	assert.Equal(t, http.StatusUnprocessableEntity, email.Code, email.Body.String())
	assert.Equal(t, "SECURITY_EMAIL_REQUIRED", decode(email)["code"])
	for _, path := range []string{"/api/token/", "/api/models", "/api/scripts/repository", "/api/option/"} {
		assert.Equal(t, http.StatusNotFound, request(http.MethodGet, path, "", true).Code, path)
	}
	order := model.TopUp{UserId: user.Id, TradeNo: "journey-own-order", CreditedQuota: 500000, ExpectedAmountMicros: 1000000, SettlementCurrency: "USD", Money: 1, PaymentProvider: model.PaymentProviderStripe, PaymentMethod: model.PaymentMethodStripe, Status: common.TopUpStatusPending}
	foreign := model.TopUp{UserId: other.Id, TradeNo: "journey-foreign-order", PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Create(&foreign).Error)
	history := request(http.MethodGet, "/api/user/topup/self", "", true)
	require.Equal(t, http.StatusOK, history.Code, history.Body.String())
	assert.Contains(t, history.Body.String(), "journey-own-order")
	assert.NotContains(t, history.Body.String(), "journey-foreign-order")
	evidence := model.ExternalTopUpSettlement{TradeNo: order.TradeNo, PaymentProvider: model.PaymentProviderStripe, PaymentMethod: model.PaymentMethodStripe, SettlementCurrency: "USD", SettledAmountMicros: 1000000, ProviderEventId: "journey-event", ProviderTransactionId: "journey-transaction"}
	for i := 0; i < 2; i++ {
		_, err := model.CompleteExternalTopUp(evidence)
		require.NoError(t, err)
	}
	// The same still-valid session must see the committed payment immediately.
	info = request(http.MethodGet, "/api/user/topup/info", "", true)
	require.Equal(t, http.StatusOK, info.Code, info.Body.String())
	assert.Contains(t, info.Body.String(), `"developer_access_granted":true`)
	key := request(http.MethodPost, "/api/token/", `{"name":"journey-first-key","expired_time":-1,"unlimited_quota":true,"group":"default","one_time_reveal":true}`, true)
	require.Equal(t, http.StatusOK, key.Code, key.Body.String())
	require.Equal(t, true, decode(key)["success"], key.Body.String())
	var tokenCount int64
	require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&tokenCount).Error)
	assert.EqualValues(t, 1, tokenCount)
	assert.Equal(t, http.StatusForbidden, request(http.MethodGet, "/api/option/", "", true).Code, "activation must not grant administrator privileges")
	first := request(http.MethodPost, "/api/user/checkin", "{}", true)
	require.Equal(t, true, decode(first)["success"], first.Body.String())
	second := request(http.MethodPost, "/api/user/checkin", "{}", true)
	assert.Equal(t, false, decode(second)["success"], second.Body.String())
	var refreshed model.User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	assert.Equal(t, 500010, refreshed.Quota, "one settlement and one checkin despite repeated requests")
	stats := request(http.MethodGet, "/api/user/checkin", "", true)
	require.Equal(t, http.StatusOK, stats.Code, stats.Body.String())
	assert.Contains(t, stats.Body.String(), `"checked_in_today":true`)
}
