package service

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRatioWebhookRejectsInternalAndUnsafeTargets(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.169.254", "100.64.1.1", "192.168.0.1", "fc00::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "2002:7f00:1::", "0.0.0.0"} {
		require.False(t, ratioPublicIP(net.ParseIP(address)), address)
	}
	require.True(t, ratioPublicIP(net.ParseIP("8.8.8.8")))
	for _, target := range []string{"http://example.com", "https://127.0.0.1", "https://[::1]", "https://user:pass@example.com", "https://example.com:8443"} {
		require.Error(t, sendRatioWebhook(context.Background(), target, "test-secret", []byte(`{}`), "event"))
	}
	require.Error(t, sendRatioWebhook(context.Background(), "https://example.com", "", []byte(`{}`), "event"))
}

func TestRatioVisibilityUsesPricingGroupResolverAndL0Boundary(t *testing.T) {
	previous := setting.UserUsableGroups2JSONString()
	special := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
	previousSpecial := special.ReadAll()
	previousOverrides := ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(previous))
		special.Clear()
		special.AddAll(previousSpecial)
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(previousOverrides))
	})
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"default","removed":"removed"}`))
	special.Clear()
	special.AddAll(map[string]map[string]string{"default": {"+:extra": "extra", "-:removed": "removed"}})
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`))
	e := model.RatioNotification{Changes: `[{"option":"GroupRatio","group":"extra","old":1,"new":2},{"option":"GroupRatio","group":"removed","old":1,"new":2},{"option":"GroupGroupRatio","group":"extra","user_group":"private","old":1,"new":2},{"option":"GroupGroupRatio","group":"extra","user_group":"default","old":1,"new":2}]`}
	u := model.User{Group: "default", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	changes, err := VisibleRatioChanges(e, u)
	require.NoError(t, err)
	require.Len(t, changes, 2)
	special.Clear()
	changes, err = VisibleRatioChanges(e, u)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.Equal(t, "removed", changes[0].Group)
	l0 := 0
	u.Role = common.RoleCommonUser
	u.TrustLevelOverride = &l0
	changes, err = VisibleRatioChanges(e, u)
	require.NoError(t, err)
	require.Empty(t, changes)
}

func TestRatioDeliveryRedactsDatabaseErrorsAndSkipsEmail(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	require.NoError(t, model.DB.AutoMigrate(&model.RatioNotification{}, &model.RatioDelivery{}))
	e := model.RatioNotification{ID: "redaction", Changes: `[{"option":"GroupRatio","group":"default","old":1,"new":2}]`}
	require.NoError(t, model.DB.Create(&e).Error)
	d := model.RatioDelivery{EventID: e.ID, UserID: user.Id, Status: "pending"}
	require.NoError(t, model.DB.Create(&d).Error)
	const sensitive = "postgres://internal:secret@10.0.0.1/private_schema parameter=secret"
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register("ratio:inject", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New(sensitive))
		}
	}))
	require.NoError(t, dispatchRatioDelivery(context.Background(), d))
	require.NoError(t, model.DB.First(&d, d.ID).Error)
	require.Equal(t, "recipient, permission or event lookup failed", d.LastError)
	require.NoError(t, model.DB.Callback().Query().Remove("ratio:inject"))
	require.NoError(t, model.DB.Model(user).Updates(map[string]any{"trust_level_override": 1, "setting": `{"notify_type":"email","webhook_url":"https://127.0.0.1"}`}).Error)
	require.NoError(t, model.DB.Model(&d).Update("next_at", 0).Error)
	require.NoError(t, dispatchRatioDelivery(context.Background(), d))
	require.NoError(t, model.DB.First(&d, d.ID).Error)
	require.Equal(t, "skipped", d.Status)
	require.Empty(t, d.LastError)
}

func TestRatioDeliveryBoundedRetriesAndStaleClaim(t *testing.T) {
	user := setupAuthSessionTestDB(t)
	require.NoError(t, model.DB.Model(user).Update("trust_level_override", 1).Error)
	require.NoError(t, model.DB.AutoMigrate(&model.RatioNotification{}, &model.RatioDelivery{}, &model.Channel{}, &model.Ability{}))
	require.NoError(t, model.DB.Model(user).Update("setting", `{"notify_type":"webhook","webhook_url":"https://127.0.0.1","webhook_secret":"test-only"}`).Error)
	event := model.RatioNotification{ID: "bounded", Changes: `[{"option":"GroupRatio","group":"default","old":1,"new":2}]`}
	require.NoError(t, model.DB.Create(&event).Error)
	d := model.RatioDelivery{EventID: event.ID, UserID: user.Id, Status: "pending"}
	require.NoError(t, model.DB.Create(&d).Error)
	stale := d
	for i := 1; i <= 5; i++ {
		require.NoError(t, dispatchRatioDelivery(context.Background(), d))
		require.NoError(t, model.DB.First(&d, d.ID).Error)
		require.Equal(t, i, d.Attempts)
		require.NoError(t, model.DB.Model(&d).Update("next_at", 0).Error)
		require.NoError(t, dispatchRatioDelivery(context.Background(), stale))
		require.NoError(t, model.DB.First(&d, d.ID).Error)
		require.Equal(t, i, d.Attempts)
	}
	require.Equal(t, "failed", d.Status)
	require.NotEmpty(t, d.LastError)
	require.NoError(t, dispatchRatioDelivery(context.Background(), d))
	require.NoError(t, model.DB.First(&d, d.ID).Error)
	require.Equal(t, 5, d.Attempts)
}
