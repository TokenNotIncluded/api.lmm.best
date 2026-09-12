package model

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestRatioNotificationAtomicBatchAndNoop(t *testing.T) {
	setupPriceLockTest(t)
	require.NoError(t, UpdateOptionsBulk(map[string]string{"ModelRatio": `{"notify-test":1}`, "CompletionRatio": `{"notify-test":2}`}))
	var events []RatioNotification
	require.NoError(t, DB.Find(&events).Error)
	require.Len(t, events, 1)
	var changes []RatioChange
	require.NoError(t, json.Unmarshal([]byte(events[0].Changes), &changes))
	require.Len(t, changes, 2)
	require.NoError(t, UpdateOptionsBulk(map[string]string{"ModelRatio": `{ "notify-test":1.0 }`, "CompletionRatio": `{"notify-test":2}`}))
	var count int64
	require.NoError(t, DB.Model(&RatioNotification{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Error(t, UpdateOption("ModelRatio", `{"notify-test":"invalid"}`))
	require.NoError(t, DB.Model(&RatioNotification{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Failure after outbox insert must roll back both the event and option.
	require.NoError(t, DB.Exec(`CREATE TRIGGER reject_ratio BEFORE UPDATE ON options WHEN NEW.key = 'ModelRatio' BEGIN SELECT RAISE(ABORT, 'injected save failure'); END`).Error)
	require.Error(t, UpdateOption("ModelRatio", `{"notify-test":4}`))
	require.NoError(t, DB.Model(&RatioNotification{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.JSONEq(t, `{"notify-test":1}`, persistedPriceOption(t, "ModelRatio"))
}

func TestRatioNotificationVisibilityAndNestedOverrides(t *testing.T) {
	useSingleConnectionTestDB(t, &Ability{}, &Channel{})
	channel := Channel{Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "visible", ChannelId: channel.Id, Enabled: true}).Error)
	all := []RatioChange{{Model: "visible"}, {Model: "hidden"}, {Group: "default"}, {Group: "private"}, {Group: "default", UserGroup: "private"}}
	raw, _ := json.Marshal(all)
	visible, err := VisibleRatioChanges(RatioNotification{Changes: string(raw)}, User{Group: "default", Status: common.UserStatusEnabled})
	require.NoError(t, err)
	require.Len(t, visible, 2)
	changes, err := ratioChanges("GroupGroupRatio", `{"default":{"default":1,"private":2}}`, `{"default":{"default":3}}`)
	require.NoError(t, err)
	require.Len(t, changes, 2)
	require.Equal(t, "default", changes[0].UserGroup)
	require.Nil(t, changes[1].New)
}

func TestRatioNotificationFanoutIdempotentAndNoEmail(t *testing.T) {
	useSingleConnectionTestDB(t, &User{}, &RatioNotification{}, &RatioDelivery{})
	users := []User{{Username: "hook", AffCode: "hook", Status: common.UserStatusEnabled, Setting: `{"notify_type":"webhook","webhook_url":"https://example.com"}`}, {Username: "email", AffCode: "email", Status: common.UserStatusEnabled, Setting: `{"notify_type":"email"}`}}
	for i := range users {
		require.NoError(t, DB.Create(&users[i]).Error)
	}
	event := RatioNotification{ID: "event", Changes: "[]"}
	require.NoError(t, DB.Create(&event).Error)
	require.NoError(t, ExpandRatioNotification(event))
	require.NoError(t, ExpandRatioNotification(event))
	var deliveries []RatioDelivery
	require.NoError(t, DB.Find(&deliveries).Error)
	require.Len(t, deliveries, 1)
	require.Equal(t, users[0].Id, deliveries[0].UserID)
	require.NoError(t, DB.First(&event, "id = ?", event.ID).Error)
	require.True(t, event.Expanded)
}
