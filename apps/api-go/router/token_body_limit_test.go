package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestProtectedMutationRoutesRejectOversizedJSONBeforeBinding(t *testing.T) {
	previousDB := model.DB
	previousRedisEnabled := common.RedisEnabled
	previousMainDatabaseType, previousLogDatabaseType := common.MainDatabaseType(), common.LogDatabaseType()
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, previousLogDatabaseType)
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		if sqlDB, sqlErr := db.DB(); sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	levelOne := model.TrustLevelMinUser + 1
	accessToken := "token-limit-test-pat"
	user := model.User{
		Username:           "token-limit-user",
		Password:           "password-placeholder",
		AffCode:            "token-limit-aff",
		Group:              "default",
		Role:               common.RoleCommonUser,
		Status:             common.UserStatusEnabled,
		AccessToken:        &accessToken,
		TrustLevelOverride: &levelOne,
	}
	require.NoError(t, db.Create(&user).Error)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	body := `{"name":"` + strings.Repeat("x", tokenMutationRequestMaxBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/token/", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response := httptest.NewRecorder()

	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)

	subscriptionBody := `{"plan_id":1,"padding":"` + strings.Repeat("x", subscriptionMutationRequestMaxBytes) + `"}`
	subscriptionRequest := httptest.NewRequest(http.MethodPost, "/api/subscription/balance/pay", strings.NewReader(subscriptionBody))
	subscriptionRequest.Header.Set("Authorization", "Bearer "+accessToken)
	subscriptionResponse := httptest.NewRecorder()

	engine.ServeHTTP(subscriptionResponse, subscriptionRequest)

	require.Equal(t, http.StatusRequestEntityTooLarge, subscriptionResponse.Code)

	resetBody := `{"mode":"hard","targets":[],"padding":"` + strings.Repeat("x", subscriptionResetMutationRequestMaxBytes) + `"}`
	resetRequest := httptest.NewRequest(http.MethodPost, "/api/subscription/root/reset/preview", strings.NewReader(resetBody))
	resetRequest.Header.Set("Authorization", "Bearer "+accessToken)
	resetResponse := httptest.NewRecorder()

	engine.ServeHTTP(resetResponse, resetRequest)

	require.Equal(t, http.StatusRequestEntityTooLarge, resetResponse.Code)

	topUpBody := `{"amount":1,"padding":"` + strings.Repeat("x", topUpMutationRequestMaxBytes) + `"}`
	topUpRequest := httptest.NewRequest(http.MethodPost, "/api/user/stripe/pay", strings.NewReader(topUpBody))
	topUpRequest.Header.Set("Authorization", "Bearer "+accessToken)
	topUpResponse := httptest.NewRecorder()

	engine.ServeHTTP(topUpResponse, topUpRequest)

	require.Equal(t, http.StatusRequestEntityTooLarge, topUpResponse.Code)

	affTransferBody := `{"aff_code":"` + strings.Repeat("x", topUpMutationRequestMaxBytes) + `"}`
	affTransferRequest := httptest.NewRequest(http.MethodPost, "/api/user/aff_transfer", strings.NewReader(affTransferBody))
	affTransferRequest.Header.Set("Authorization", "Bearer "+accessToken)
	affTransferResponse := httptest.NewRecorder()

	engine.ServeHTTP(affTransferResponse, affTransferRequest)

	require.Equal(t, http.StatusRequestEntityTooLarge, affTransferResponse.Code)

	inviteBody := `{"email":"` + strings.Repeat("x", affiliateInvitationRequestMaxBytes) + `"}`
	inviteRequest := httptest.NewRequest(http.MethodPost, "/api/user/aff/invite", strings.NewReader(inviteBody))
	inviteRequest.Header.Set("Authorization", "Bearer "+accessToken)
	inviteResponse := httptest.NewRecorder()

	engine.ServeHTTP(inviteResponse, inviteRequest)

	require.Equal(t, http.StatusRequestEntityTooLarge, inviteResponse.Code)
}

func TestEmailBindRouteRejectsOversizedJSONBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	body := `{"email":"` + strings.Repeat("x", userSelfMutationRequestMaxBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/oauth/email/bind", strings.NewReader(body))
	response := httptest.NewRecorder()

	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
}

func TestSelfAccountActionMutationsRejectOversizedJSONBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	body := `{"reason":"` + strings.Repeat("x", userSelfMutationRequestMaxBytes) + `"}`

	for _, path := range []string{"/api/user/account-action-requests", "/api/user/violation-fee-appeals"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			response := httptest.NewRecorder()

			engine.ServeHTTP(response, request)

			require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
		})
	}
}

func TestAdvancedSecuritySettingsRejectOversizedPolicyBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	body := `{"rules":"` + strings.Repeat("x", rawOptionMutationRequestMaxBytes) + `"}`
	request := httptest.NewRequest(http.MethodPut, "/api/security/admin/settings", strings.NewReader(body))
	response := httptest.NewRecorder()

	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
}

func TestL1OnboardingProofRejectsOversizedJSONBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	body := `{"step":"client_heartbeat","client":"` + strings.Repeat("x", userSelfMutationRequestMaxBytes) + `"}`
	request := httptest.NewRequest(http.MethodPost, "/api/onboarding/todo/proof", strings.NewReader(body))
	response := httptest.NewRecorder()

	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
}

func TestCompactOAuthRoutesRejectOversizedJSONBeforeAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	body := `{"provider":"` + strings.Repeat("x", compactOAuthRequestMaxBytes) + `"}`

	for _, path := range []string{"/api/oauth/state", "/api/oauth/wechat/bind", "/api/oauth/telegram/bind/start"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		response := httptest.NewRecorder()

		engine.ServeHTTP(response, request)

		require.Equalf(t, http.StatusRequestEntityTooLarge, response.Code, "path=%s", path)
	}
}

func TestAuthenticatedSecurityAndBountyMutationsRejectOversizedJSON(t *testing.T) {
	db := setupOpenSourceBountyAccessRouterTest(t)
	levelOne := model.TrustLevelMinUser + 1
	userToken := "authenticated-mutation-limit-user"
	adminToken := "authenticated-mutation-limit-admin"
	users := []model.User{
		{
			Username: "authenticated-mutation-limit-user", Password: "password-placeholder",
			AffCode: "authenticated-mutation-limit-user", Group: "default",
			Role: common.RoleCommonUser, Status: common.UserStatusEnabled,
			AccessToken: &userToken, TrustLevelOverride: &levelOne,
		},
		{
			Username: "authenticated-mutation-limit-admin", Password: "password-placeholder",
			AffCode: "authenticated-mutation-limit-admin", Group: "default",
			Role: common.RoleAdminUser, Status: common.UserStatusEnabled,
			AccessToken: &adminToken,
		},
	}
	for index := range users {
		require.NoError(t, db.Create(&users[index]).Error)
	}

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)

	securityRoutes := []struct {
		method string
		path   string
		limit  int
		token  string
	}{
		{method: http.MethodPost, path: "/api/verify", limit: securityFactorMutationRequestMaxBytes, token: userToken},
		{method: http.MethodPut, path: "/api/user/setting", limit: userSelfMutationRequestMaxBytes, token: userToken},
		{method: http.MethodPost, path: "/api/user/passkey/register/begin", limit: passkeyBeginRequestMaxBytes, token: userToken},
		{method: http.MethodPost, path: "/api/user/passkey/register/finish", limit: passkeyFinishRequestMaxBytes, token: userToken},
		{method: http.MethodPost, path: "/api/user/passkey/verify/begin", limit: passkeyBeginRequestMaxBytes, token: userToken},
		{method: http.MethodPost, path: "/api/user/passkey/verify/finish", limit: passkeyFinishRequestMaxBytes, token: userToken},
		{method: http.MethodPost, path: "/api/user/2fa/setup", limit: securityFactorMutationRequestMaxBytes, token: userToken},
		{method: http.MethodPost, path: "/api/user/2fa/enable", limit: securityFactorMutationRequestMaxBytes, token: userToken},
		{method: http.MethodPost, path: "/api/user/2fa/disable", limit: securityFactorMutationRequestMaxBytes, token: userToken},
		{method: http.MethodPost, path: "/api/user/2fa/backup_codes", limit: securityFactorMutationRequestMaxBytes, token: userToken},
	}
	for _, route := range securityRoutes {
		t.Run(route.path, func(t *testing.T) {
			body := `{"padding":"` + strings.Repeat("x", route.limit) + `"}`
			request := httptest.NewRequest(route.method, route.path, strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+route.token)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			engine.ServeHTTP(response, request)

			require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
		})
	}

	bountyRoutes := []struct {
		method string
		path   string
		token  string
	}{
		{method: http.MethodPost, path: "/api/open-source-bounties", token: userToken},
		{method: http.MethodPut, path: "/api/open-source-bounties/projects/1", token: userToken},
		{method: http.MethodPost, path: "/api/open-source-bounties/projects/1/accept", token: userToken},
		{method: http.MethodPost, path: "/api/open-source-bounties/projects/1/submit", token: userToken},
		{method: http.MethodPost, path: "/api/open-source-bounties/challenges/1/approve", token: userToken},
		{method: http.MethodPost, path: "/api/open-source-bounties/challenges/1/reject", token: userToken},
		{method: http.MethodPost, path: "/api/open-source-bounties/challenges/1/tip", token: userToken},
		{method: http.MethodPost, path: "/api/open-source-bounties/challenges/1/rate-owner", token: userToken},
		{method: http.MethodPost, path: "/api/open-source-bounties/challenges/1/disputes", token: userToken},
		{method: http.MethodPost, path: "/api/open-source-bounties/disputes/1/resolve", token: adminToken},
	}
	for _, route := range bountyRoutes {
		t.Run(route.path, func(t *testing.T) {
			body := `{"padding":"` + strings.Repeat("x", openSourceBountyMutationRequestMaxBytes) + `"}`
			request := httptest.NewRequest(route.method, route.path, strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+route.token)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			engine.ServeHTTP(response, request)

			require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
		})
	}
}
