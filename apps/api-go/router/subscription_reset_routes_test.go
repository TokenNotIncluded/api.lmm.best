package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionResetRoutesExposeOnlyPreviewedRootBatchMutations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	routes := make(map[string]struct{}, len(engine.Routes()))
	for _, route := range engine.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, expected := range []string{
		http.MethodGet + " /api/subscription/admin/records",
		http.MethodGet + " /api/subscription/root/reset-targets",
		http.MethodPost + " /api/subscription/root/reset/preview",
		http.MethodPost + " /api/subscription/root/reset",
		http.MethodGet + " /api/subscription/self/reset-vouchers",
		http.MethodPost + " /api/subscription/self/reset-vouchers/:id/redeem",
	} {
		_, registered := routes[expected]
		require.True(t, registered, expected)
	}
	for _, retired := range []string{
		http.MethodPost + " /api/subscription/admin/plans/:id/subscriptions/reset",
		http.MethodPost + " /api/subscription/admin/users/:id/subscriptions/reset",
	} {
		_, registered := routes[retired]
		require.False(t, registered, retired)
	}
}

func TestSubscriptionResetRoutesRequireRootRole(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	paymentSetting := operation_setting.GetPaymentSetting()
	previousPaymentSetting := *paymentSetting
	paymentSetting.ComplianceConfirmed = false
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, previousLogDatabaseType)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB, model.LOG_DB = db, db
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Log{},
		&model.SubscriptionPlan{},
		&model.UserSubscription{},
		&model.SubscriptionResetVoucher{},
	))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		*paymentSetting = previousPaymentSetting
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	adminToken := "subscription-reset-admin-token"
	rootToken := "subscription-reset-root-token"
	require.NoError(t, db.Create(&model.User{
		Username: "subscription-reset-admin", Password: "password", AffCode: "reset-admin-aff",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled, AccessToken: &adminToken,
	}).Error)
	require.NoError(t, db.Create(&model.User{
		Username: "subscription-reset-root", Password: "password", AffCode: "reset-root-aff",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled, AccessToken: &rootToken,
	}).Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader("{}"))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(recorder, req)
		return recorder
	}

	for _, route := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/subscription/root/reset-targets?page=1&page_size=20"},
		{method: http.MethodPost, path: "/api/subscription/root/reset/preview"},
		{method: http.MethodPost, path: "/api/subscription/root/reset"},
	} {
		require.Equal(t, http.StatusForbidden, request(route.method, route.path, adminToken).Code)
		require.Equal(t, http.StatusOK, request(route.method, route.path, rootToken).Code)
	}
}
