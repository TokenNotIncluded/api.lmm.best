package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountBalanceConsentAndScope(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	oldUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	user := model.User{Username: "balance-owner", Status: common.UserStatusEnabled, Quota: 2361}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "balance-fixture", ExpiredTime: -1, UnlimitedQuota: true, UsedQuota: 98765}
	require.NoError(t, db.Create(&token).Error)
	router := gin.New()
	router.GET("/v1/balance", middleware.QuotaQueryAuth(), GetAccountBalance)
	query := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/balance", nil)
		r.Header.Set("Authorization", "Bearer sk-"+token.Key)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	grant := func(owner int, payload string) *httptest.ResponseRecorder {
		r := gin.New()
		r.PUT("/token/:id", func(c *gin.Context) { c.Set("id", owner) }, SetAccountBalanceAccess)
		request := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/token/%d", token.Id), strings.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, request)
		return w
	}
	require.Equal(t, 403, query().Code)
	ownerRouter := gin.New()
	ownerRouter.PUT("/token/:id", middleware.UserAuth(), SetAccountBalanceAccess)
	selfGrant := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/token/%d", token.Id), strings.NewReader(`{"enabled":true}`))
	selfGrant.Header.Set("Authorization", "Bearer sk-"+token.Key)
	selfGrant.Header.Set("Content-Type", "application/json")
	selfGrantResult := httptest.NewRecorder()
	ownerRouter.ServeHTTP(selfGrantResult, selfGrant)
	require.NotEqual(t, http.StatusOK, selfGrantResult.Code, selfGrantResult.Body.String())
	require.Equal(t, 403, query().Code)
	require.Equal(t, 400, grant(user.Id, `{}`).Code)
	require.Equal(t, 404, grant(user.Id+1, `{"enabled":true}`).Code)
	require.Equal(t, 403, query().Code)
	require.Equal(t, 200, grant(user.Id, `{"enabled":true}`).Code)
	w := query()
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "account", body["scope"])
	require.Equal(t, "USD", body["currency"])
	require.Equal(t, 23.61, body["remaining"])
	require.NotContains(t, body, "used_total")
	require.NotContains(t, w.Body.String(), token.Key)
	for _, quota := range []int{0, -123} {
		require.NoError(t, db.Model(&user).Update("quota", quota).Error)
		w = query()
		require.Equal(t, 200, w.Code)
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Equal(t, float64(quota)/100, body["remaining"])
	}
	require.Equal(t, 200, grant(user.Id, `{"enabled":false}`).Code)
	require.Equal(t, 403, query().Code)
	require.Equal(t, 200, grant(user.Id, `{"enabled":true}`).Code)
	require.NoError(t, db.Model(&token).Update("status", common.TokenStatusDisabled).Error)
	require.Equal(t, 401, query().Code)
	require.NoError(t, db.Model(&token).Update("oauth_managed", true).Error)
	require.Equal(t, 404, grant(user.Id, `{"enabled":true}`).Code)
}
