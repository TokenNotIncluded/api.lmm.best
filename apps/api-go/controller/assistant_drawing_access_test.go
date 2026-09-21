package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDrawingAccessTest(t *testing.T) (*gorm.DB, model.User) {
	t.Helper()
	oldDB, oldLogDB, oldRedis := model.DB, model.LOG_DB, common.RedisEnabled
	oldMainType, oldLogType := common.MainDatabaseType(), common.LogDatabaseType()
	oldDrawing := common.DrawingEnabled
	oldGroups := setting.UserUsableGroups2JSONString()
	oldRatios := ratio_setting.GroupRatio2JSONString()
	oldWarnings := ratio_setting.GroupWarnings2JSONString()
	t.Cleanup(func() {
		model.DB, model.LOG_DB, common.RedisEnabled = oldDB, oldLogDB, oldRedis
		common.SetDatabaseTypes(oldMainType, oldLogType)
		common.DrawingEnabled = oldDrawing
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(oldGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios))
		require.NoError(t, ratio_setting.UpdateGroupWarningsByJSONString(oldWarnings))
	})
	initModelListColumnNames(t)
	require.NoError(t, i18n.Init())
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	common.DrawingEnabled = true
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","image-2":"Images"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"image-2":1}`))
	require.NoError(t, ratio_setting.UpdateGroupWarningsByJSONString(`{}`))
	level := model.TrustLevelMinUser + 1
	user := model.User{Username: "drawing-owner", Password: "password", Group: "default", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, TrustLevelOverride: &level, Quota: int(10 * common.QuotaPerUnit)}
	require.NoError(t, db.Create(&user).Error)
	return db, user
}

func TestDrawingWebBalanceFloorExactBoundaryAndTrustedMCPOnly(t *testing.T) {
	oldGetter := drawingUserQuota
	t.Cleanup(func() { drawingUserQuota = oldGetter })
	quota, reads := int(10*common.QuotaPerUnit)-1, 0
	drawingUserQuota = func(id int, fromDB bool) (int, error) {
		assert.Equal(t, 17, id)
		assert.True(t, fromDB, "the durable wallet balance is authoritative")
		reads++
		return quota, nil
	}
	request := func(ctx context.Context) (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/pg/images/generations?origin=mcp", nil).WithContext(ctx)
		c.Request.Header.Set("X-Drawing-Origin", "mcp")
		return c, w
	}
	c, w := request(context.Background())
	assert.False(t, requireDrawingWebBalance(c, 17))
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"WEB_DRAWING_MINIMUM_BALANCE"`)
	assert.Contains(t, w.Body.String(), `"drawing_web_access"`)
	assert.Contains(t, w.Body.String(), "API or MCP remain available and are billed normally")
	quota++
	c, _ = request(context.Background())
	assert.True(t, requireDrawingWebBalance(c, 17))
	assert.Equal(t, common.GetTrustQuota(), common.GetContextKeyInt(c, constant.ContextKeyWebDrawingMinimumQuota))
	quota = 0
	c, _ = request(context.WithValue(context.Background(), drawingMCPRelayIdentityKey{}, drawingMCPRelayIdentity{UserID: 17}))
	assert.True(t, requireDrawingWebBalance(c, 17))
	assert.Equal(t, 2, reads, "MCP must not acquire the web-only floor")
	assert.Zero(t, common.GetContextKeyInt(c, constant.ContextKeyWebDrawingMinimumQuota))
	c, _ = request(context.WithValue(context.Background(), drawingMCPRelayIdentityKey{}, drawingMCPRelayIdentity{UserID: 18}))
	assert.False(t, requireDrawingWebBalance(c, 17), "another identity is not a bypass")
}

func TestDrawingWebBalanceUnavailableFailsClosedAndStatusReturnsNull(t *testing.T) {
	_, user := setupDrawingAccessTest(t)
	oldGetter := drawingUserQuota
	t.Cleanup(func() { drawingUserQuota = oldGetter })
	drawingUserQuota = func(int, bool) (int, error) { return 0, errors.New("sensitive database detail") }
	c, w := newAuthenticatedContext(t, http.MethodPost, "/pg/images/generations", nil, user.Id)
	assert.False(t, requireDrawingWebBalance(c, user.Id))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Contains(t, w.Body.String(), `"balance_usd":null`)
	assert.Contains(t, w.Body.String(), `"allowed":false`)
	assert.NotContains(t, w.Body.String(), "sensitive database detail")
	status := performAssistantStatusTestRequest(t, user.Id)
	assert.Equal(t, http.StatusOK, status.Code)
	assert.Contains(t, status.Body.String(), `"drawing_web_access":{"minimum_balance_usd":10,"balance_usd":null,"allowed":false}`)
}

func TestPreparePlaygroundImageAuthBindsRealKeyBeforeDistribution(t *testing.T) {
	_, user := setupDrawingAccessTest(t)
	engine := gin.New()
	_ = engine.SetTrustedProxies(nil)
	engine.POST("/pg/images/generations", func(c *gin.Context) { c.Set("id", user.Id) }, PreparePlaygroundImageAuth, func(c *gin.Context) {
		assert.Positive(t, common.GetContextKeyInt(c, constant.ContextKeyTokenId))
		assert.Equal(t, user.Id, common.GetContextKeyInt(c, constant.ContextKeyUserId))
		assert.Equal(t, "image-2", common.GetContextKeyString(c, constant.ContextKeyTokenGroup))
		assert.Equal(t, "image-2", common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
		assert.Equal(t, "default", common.GetContextKeyString(c, constant.ContextKeyUserGroup))
		assert.True(t, common.GetContextKeyBool(c, constant.ContextKeyDrawingRealToken))
		for _, secretHeader := range []string{"Authorization", "Cookie", "X-Api-Key", "Sec-WebSocket-Protocol"} {
			assert.Empty(t, c.Request.Header.Get(secretHeader))
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/pg/images/generations?group=image-2", strings.NewReader(`{"model":"gpt-image-2","prompt":"draw"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer dashboard-secret")
	request.Header.Set("Cookie", "session=dashboard-cookie")
	request.Header.Set("X-Api-Key", "unrelated-api-secret")
	request.Header.Set("Sec-WebSocket-Protocol", "openai-insecure-api-key.attacker-key")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, request)
	assert.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
}

func TestDrawingRealKeyRestrictionsFailWithoutReplacement(t *testing.T) {
	for _, restriction := range []string{"disabled", "expired", "exhausted", "ip", "model"} {
		t.Run(restriction, func(t *testing.T) {
			db, user := setupDrawingAccessTest(t)
			key := model.Token{UserId: user.Id, Key: strings.Repeat("r", 48), Group: "image-2", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
			switch restriction {
			case "disabled":
				key.Status = common.TokenStatusDisabled
			case "expired":
				key.ExpiredTime = 1
			case "exhausted":
				key.UnlimitedQuota = false
			case "ip":
				allowed := "198.51.100.1"
				key.AllowIps = &allowed
			case "model":
				key.ModelLimitsEnabled = true
				key.ModelLimits = "another-model"
			}
			require.NoError(t, db.Create(&key).Error)
			engine := gin.New()
			_ = engine.SetTrustedProxies(nil)
			engine.POST("/pg/images/generations", middleware.BodyStorageCleanup(), func(c *gin.Context) { c.Set("id", user.Id) },
				PreparePlaygroundImageAuth, middleware.Distribute(), func(c *gin.Context) { t.Error("restricted key reached relay") })
			request := httptest.NewRequest(http.MethodPost, "/pg/images/generations?group=image-2", strings.NewReader(`{"model":"gpt-image-2","prompt":"draw"}`))
			request.Header.Set("Content-Type", "application/json")
			request.RemoteAddr = "203.0.113.1:1234"
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, request)
			assert.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, w.Code, w.Body.String())
			assert.NotContains(t, w.Body.String(), key.Key)
			var count int64
			require.NoError(t, db.Model(&model.Token{}).Where("user_id = ?", user.Id).Count(&count).Error)
			assert.EqualValues(t, 1, count)
		})
	}
}

func TestEnsureDrawingKeyBelowFloorReturnsOnlyMetadata(t *testing.T) {
	db, user := setupDrawingAccessTest(t)
	require.NoError(t, db.Model(&user).Update("quota", 0).Error)
	for _, created := range []bool{true, false} {
		c, w := newAuthenticatedContext(t, http.MethodPost, "/api/assistant/drawing/key", map[string]any{}, user.Id)
		c.Set("session_id", "browser-session")
		EnsureAssistantDrawingKey(c)
		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		assert.Equal(t, "no-store", w.Header().Get("Cache-Control"))
		var body struct {
			Success bool           `json:"success"`
			Data    map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.True(t, body.Success)
		assert.Len(t, body.Data, 4)
		assert.Equal(t, created, body.Data["created"])
		assert.Equal(t, "image-2", body.Data["group"])
		var token model.Token
		require.NoError(t, db.First(&token).Error)
		assert.NotContains(t, w.Body.String(), token.Key)
	}
}

func TestPrepareAssistantDrawingChecksBalanceBeforeConfirmationConsumption(t *testing.T) {
	db, user := setupDrawingAccessTest(t)
	require.NoError(t, db.Model(&user).Update("quota", int(10*common.QuotaPerUnit)-1).Error)
	// There is deliberately no auth_flows table: the balance rejection must
	// happen before any attempt to consume the confirmation (or create a key).
	c, w := newAuthenticatedContext(t, http.MethodPost, "/api/assistant/drawing/generate", map[string]any{"confirmation_token": "not-consumed"}, user.Id)
	c.Set("session_id", "browser-session")
	PrepareAssistantDrawing(c)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "WEB_DRAWING_MINIMUM_BALANCE")
	var count int64
	require.NoError(t, db.Model(&model.Token{}).Count(&count).Error)
	assert.Zero(t, count)
}
