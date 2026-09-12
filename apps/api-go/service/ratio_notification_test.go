package service

import (
	"context"
	"net"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
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

func TestRatioDeliveryBoundedRetriesAndStaleClaim(t *testing.T) {
	user := setupAuthSessionTestDB(t)
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
