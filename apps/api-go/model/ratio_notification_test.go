package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
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
	preservePricingTestState(t)
	pricingCache.Store(&pricingSnapshot{pricing: []Pricing{{ModelName: "visible", EnableGroup: []string{"default"}}, {ModelName: "extra-model", EnableGroup: []string{"extra"}}, {ModelName: "public-model", EnableGroup: []string{"all"}}, {ModelName: "hidden", EnableGroup: []string{"private"}}}, generation: pricingInvalidation.Load(), refreshedAt: time.Now()})
	channel := Channel{Status: common.ChannelStatusEnabled}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "visible", ChannelId: channel.Id, Enabled: true}).Error)
	all := []RatioChange{{Model: "visible"}, {Model: "hidden"}, {Group: "default"}, {Group: "private"}, {Group: "default", UserGroup: "private"}, {Model: "extra-model"}, {Model: "public-model"}, {Group: "extra", UserGroup: "default"}}
	raw, _ := json.Marshal(all)
	visible, err := VisibleRatioChanges(RatioNotification{Changes: string(raw)}, User{Group: "default", Status: common.UserStatusEnabled, Role: common.RoleAdminUser}, map[string]string{"default": "", "extra": ""})
	require.NoError(t, err)
	require.Len(t, visible, 5)
	changes, err := ratioChanges("GroupGroupRatio", `{"default":{"default":1,"private":2}}`, `{"default":{"default":3}}`)
	require.NoError(t, err)
	require.Len(t, changes, 2)
	require.Equal(t, "default", changes[0].UserGroup)
	require.Nil(t, changes[1].New)
}

func TestRatioNotificationGroupAliasesAreAtomicAndDeduplicated(t *testing.T) {
	setupPriceLockTest(t)
	oldGroup, oldOverride := ratio_setting.GroupRatio2JSONString(), ratio_setting.GroupGroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldGroup))
		require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(oldOverride))
	})
	values := map[string]string{"group_ratio_setting.group_ratio": `{"default":2}`, "group_ratio_setting.group_group_ratio": `{"default":{"default":3}}`}
	require.NoError(t, UpdateOptionsBulk(values))
	var events []RatioNotification
	require.NoError(t, DB.Find(&events).Error)
	require.Len(t, events, 1)
	require.NoError(t, UpdateOptionsBulk(values))
	require.NoError(t, DB.Find(&events).Error)
	require.Len(t, events, 1)
	require.JSONEq(t, persistedPriceOption(t, "GroupRatio"), persistedPriceOption(t, "group_ratio_setting.group_ratio"))
	require.JSONEq(t, persistedPriceOption(t, "GroupGroupRatio"), persistedPriceOption(t, "group_ratio_setting.group_group_ratio"))
	require.Error(t, UpdateOptionsBulk(map[string]string{"GroupRatio": `{"default":4}`, "group_ratio_setting.group_ratio": `{"default":5}`}))
	require.NoError(t, DB.Find(&events).Error)
	require.Len(t, events, 1)
}

func TestRatioNotificationMigrationInventoryIncludesOutboxAndUniqueRecipient(t *testing.T) {
	useSingleConnectionTestDB(t, &RatioNotification{}, &RatioDelivery{})
	inventory, err := buildPostgresSchemaInventory(DB, "public", mainMigrationModels())
	require.NoError(t, err)
	requirePostgresIndexSpec(t, inventory, "ratio_deliveries", "ratio_recipient", true, []string{"event_id", "user_id"})
	requirePostgresIndexSpec(t, inventory, "ratio_notifications", "idx_ratio_notifications_expanded", false, []string{"expanded"})
	snapshot := catalogSnapshotForInventory(inventory)
	require.NoError(t, verifyPostgresCatalogSnapshot(inventory, snapshot))
	delete(snapshot.Indexes, postgresCatalogKey{Table: "ratio_deliveries", Name: "ratio_recipient"})
	require.Error(t, verifyPostgresCatalogSnapshot(inventory, snapshot))
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
