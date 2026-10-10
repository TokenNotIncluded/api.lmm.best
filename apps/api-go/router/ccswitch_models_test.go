package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCCSwitchModelsUsesRealTokenAuthGroupsLimitsAndExpiry(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	t.Cleanup(func() { model.DB, model.LOG_DB = previousDB, previousLogDB })
	setupRelayRouterTestDB(t)
	previousGroups := setting.UserUsableGroups2JSONString()
	previousGroupRatios := ratio_setting.GroupRatio2JSONString()
	previousModelRatios := ratio_setting.ModelRatio2JSONString()
	previousSelfUse := operation_setting.SelfUseModeEnabled.Load()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previousGroups))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroupRatios))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatios))
		operation_setting.SelfUseModeEnabled.Store(previousSelfUse)
		model.InvalidatePricingCache()
	})
	operation_setting.SelfUseModeEnabled.Store(false)
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP","auto":"Auto"}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":2}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"ccswitch-default":1,"ccswitch-vip-allowed":1,"ccswitch-vip-denied":1}`))
	require.NoError(t, model.DB.AutoMigrate(&model.Channel{}, &model.Model{}, &model.Vendor{}))
	require.NoError(t, model.DB.Create(&model.Channel{Id: 1, Name: "ccswitch", Key: "unused", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, model.DB.Create(&[]model.Ability{
		{Group: "default", Model: "ccswitch-default", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "ccswitch-vip-allowed", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "ccswitch-vip-denied", ChannelId: 1, Enabled: true},
		{Group: "vip", Model: "ccswitch-unpriced", ChannelId: 1, Enabled: true},
	}).Error)
	require.NoError(t, model.RefreshPricing())
	levelOne := model.TrustLevelMinUser + 1
	user := model.User{Username: "ccswitch-models", Status: common.UserStatusEnabled, Group: "default", Quota: 100,
		TrustLevelOverride: &levelOne, ConsoleActivatedAt: 1}
	require.NoError(t, model.DB.Create(&user).Error)
	ordinary := model.Token{UserId: user.Id, Key: "ccswitchordinary", Status: common.TokenStatusEnabled,
		Group: "default", ExpiredTime: -1, UnlimitedQuota: true}
	auto := model.Token{UserId: user.Id, Key: "ccswitchauto", Status: common.TokenStatusEnabled,
		Group: "auto", AutoGroups: `["vip"]`, ModelLimitsEnabled: true,
		ModelLimits:   "ccswitch-vip-allowed,ccswitch-default,ccswitch-unpriced",
		OneTimeReveal: true, ExpiredTime: -1, UnlimitedQuota: true}
	expired := model.Token{UserId: user.Id, Key: "ccswitchexpired", Status: common.TokenStatusEnabled,
		Group: "default", ExpiredTime: time.Now().Unix() - 10, UnlimitedQuota: true}
	disabled := model.Token{UserId: user.Id, Key: "ccswitchdisabled", Status: common.TokenStatusDisabled,
		Group: "default", ExpiredTime: -1, UnlimitedQuota: true}
	for _, token := range []*model.Token{&ordinary, &auto, &expired, &disabled} {
		require.NoError(t, model.DB.Create(token).Error)
	}
	engine := gin.New()
	SetRelayRouter(engine)
	list := func(key string, status int) []string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
		request.Header.Set("Authorization", "Bearer sk-"+key)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		require.Equal(t, status, recorder.Code)
		if status != http.StatusOK {
			return nil
		}
		var payload struct {
			Object string `json:"object"`
			Data   []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &payload))
		require.Equal(t, "list", payload.Object)
		ids := make([]string, 0, len(payload.Data))
		for _, item := range payload.Data {
			ids = append(ids, item.ID)
		}
		return ids
	}
	require.Equal(t, []string{"ccswitch-default"}, list(ordinary.Key, http.StatusOK))
	require.Equal(t, []string{"ccswitch-vip-allowed"}, list(auto.Key, http.StatusOK),
		"custom Auto groups, model restrictions and configured pricing all constrain the real relay catalogue")
	auto.AutoGroups = `["default"]`
	require.NoError(t, auto.Update())
	require.Equal(t, []string{"ccswitch-default"}, list(auto.Key, http.StatusOK))
	list(expired.Key, http.StatusNotFound)
	list(disabled.Key, http.StatusNotFound)
	require.NoError(t, model.DB.First(&user, user.Id).Error)
	require.Equal(t, 100, user.Quota)
	require.Zero(t, user.UsedQuota)
	require.Zero(t, user.RequestCount)
}
