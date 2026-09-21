package model

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func setupPriceLockTest(t *testing.T) {
	t.Helper()
	useSingleConnectionTestDB(t, &Option{}, &Ability{}, &Channel{}, &RatioNotification{}, &RatioDelivery{})
	previousPricing := priceOptionSnapshot()
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		for key, value := range previousPricing {
			require.NoError(t, updateOptionMap(key, value))
		}
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
}

func persistedPriceOption(t *testing.T, key string) string {
	t.Helper()
	var option Option
	require.NoError(t, DB.First(&option, &Option{Key: key}).Error)
	return option.Value
}

func TestPriceLocksProtectAllPricingMapsBeforeValidation(t *testing.T) {
	setupPriceLockTest(t)
	require.False(t, IsModelPriceLocked("locked"))
	initial, changed, expected := map[string]string{}, map[string]string{}, map[string]string{}
	for _, key := range modelPriceOptionKeys {
		initial[key] = `{"locked":1,"free":2}`
		changed[key] = `{"locked":null,"free":3,"locked-absent":"invalid"}`
		expected[key] = `{"locked":1,"free":3}`
		if key == "billing_setting.billing_mode" {
			initial[key] = `{"locked":"ratio","free":"ratio"}`
			changed[key] = `{"locked":null,"free":"tiered_expr","locked-absent":"invalid"}`
			expected[key] = `{"locked":"ratio","free":"tiered_expr"}`
		} else if key == "billing_setting.billing_expr" {
			initial[key] = `{"locked":"1","free":"1"}`
			changed[key] = `{"locked":null,"free":"2","locked-absent":"invalid"}`
			expected[key] = `{"locked":"1","free":"2"}`
		}
	}
	require.NoError(t, UpdateOptionsBulk(initial))
	require.NoError(t, UpdateOption(ModelPriceLocksOptionKey, `{"locked":true,"locked-absent":true}`))
	result, err := UpdateOptionsBulkWithWarnings(changed)
	require.NoError(t, err)
	require.Equal(t, []string{"locked", "locked-absent"}, result.LockedModels)
	require.Len(t, result.Warnings, 1)
	for key, value := range expected {
		require.JSONEq(t, value, persistedPriceOption(t, key))
	}
	price, _ := ratio_setting.GetModelPrice("locked", false)
	require.Equal(t, float64(1), price)
	for key := range changed {
		changed[key] = `{}`
	}
	result, err = UpdateOptionsBulkWithWarnings(changed)
	require.NoError(t, err)
	require.Equal(t, []string{"locked"}, result.LockedModels)
	for _, key := range modelPriceOptionKeys {
		var remaining map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(persistedPriceOption(t, key)), &remaining))
		require.Len(t, remaining, 1)
		require.Contains(t, remaining, "locked")
	}
}

func TestPriceLockBatchProtectsOldAndNewLocks(t *testing.T) {
	setupPriceLockTest(t)
	require.NoError(t, UpdateOption("ModelPrice", `{"old":1,"new":2,"free":3}`))
	require.NoError(t, UpdateOption(ModelPriceLocksOptionKey, `{"old":true}`))
	result, err := UpdateOptionsBulkWithWarnings(map[string]string{
		ModelPriceLocksOptionKey: `{"new":true}`,
		"ModelPrice":             `{"old":10,"new":20,"free":30}`,
	})
	require.NoError(t, err)
	require.Equal(t, []string{"new", "old"}, result.LockedModels)
	require.JSONEq(t, `{"old":1,"new":2,"free":30}`, persistedPriceOption(t, "ModelPrice"))
	require.NoError(t, UpdateOption("ModelPrice", `{"old":10,"new":2,"free":30}`))
	require.JSONEq(t, `{"old":10,"new":2,"free":30}`, persistedPriceOption(t, "ModelPrice"))
}

func TestPriceLockPreviewDoesNotWriteAndRejectedBatchRollsBack(t *testing.T) {
	setupPriceLockTest(t)
	require.NoError(t, UpdateOption("ModelPrice", `{"locked":1,"free":2}`))
	require.NoError(t, UpdateOption(ModelPriceLocksOptionKey, `{"locked":true}`))
	result, err := ValidateOptionValuesWithWarnings(map[string]string{"ModelPrice": `{"locked":"invalid","free":3}`})
	require.NoError(t, err)
	require.NotEmpty(t, result.Warnings)
	require.JSONEq(t, `{"locked":1,"free":2}`, persistedPriceOption(t, "ModelPrice"))
	_, err = UpdateOptionsBulkWithWarnings(map[string]string{
		"ModelPrice":             `{"locked":9,"free":"invalid"}`,
		ModelPriceLocksOptionKey: `{}`,
		"Notice":                 "must roll back",
	})
	require.Error(t, err)
	require.JSONEq(t, `{"locked":1,"free":2}`, persistedPriceOption(t, "ModelPrice"))
	require.True(t, IsModelPriceLocked("locked"))
	var count int64
	require.NoError(t, DB.Model(&Option{}).Where(&Option{Key: "Notice"}).Count(&count).Error)
	require.Zero(t, count)
	for _, value := range []string{`null`, `[]`, `{"locked":null}`, `{"locked":"true"}`} {
		_, err := UpdateOptionWithWarnings(ModelPriceLocksOptionKey, value)
		require.Error(t, err)
	}
	_, err = UpdateOptionWithWarnings("billing_setting.billing_expr", `{"free":"invalid syntax !"}`)
	require.Error(t, err)
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		"billing_setting.billing_mode": `{"free":""}`,
		"billing_setting.billing_expr": `{"free":""}`,
	}))
}

func TestPriceLocksUseStoredPolicyAndPreserveConcurrentToggles(t *testing.T) {
	setupPriceLockTest(t)
	require.NoError(t, UpdateOption("ModelPrice", `{"locked":1}`))
	_, err := UpdateModelPriceLock("locked", true)
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	common.OptionMap[ModelPriceLocksOptionKey] = `{}`
	common.OptionMap["ModelPrice"] = `{"locked":999}`
	common.OptionMapRWMutex.Unlock()
	result, err := UpdateOptionWithWarnings("ModelPrice", `{"locked":2}`)
	require.NoError(t, err)
	require.NotEmpty(t, result.Warnings)
	require.JSONEq(t, `{"locked":1}`, persistedPriceOption(t, "ModelPrice"))
	var wg sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := UpdateModelPriceLock(fmt.Sprintf("model-%d", i), true)
			errors <- err
		}(i)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	locks, err := parseModelPriceLocks(persistedPriceOption(t, ModelPriceLocksOptionKey))
	require.NoError(t, err)
	require.Len(t, locks, 13)
	require.True(t, locks["locked"])
	require.Equal(t, locks, GetModelPriceLocksCopy())
	_, err = UpdateModelPriceLock("locked", false)
	require.NoError(t, err)
	require.NoError(t, UpdateOption("ModelPrice", `{"locked":2}`))
	require.JSONEq(t, `{"locked":2}`, persistedPriceOption(t, "ModelPrice"))
}

func TestPriceLockMixedAssistantUpdateUsesOneDatabaseConnection(t *testing.T) {
	setupPriceLockTest(t)
	previous := setting.GetAssistantSettings()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAssistantGroup(previous.Group))
		require.NoError(t, setting.UpdateAssistantModel(previous.Model))
	})
	require.NoError(t, DB.Create(&Channel{Id: 1, Name: "price-lock-assistant", Key: "test", Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, DB.Create(&Ability{Group: "default", Model: "price-lock-assistant", Enabled: true, ChannelId: 1}).Error)
	require.NoError(t, withSingleConnectionDeadline(t, func() error {
		return UpdateOptionsBulk(map[string]string{
			setting.AssistantGroupOptionKey: "default",
			setting.AssistantModelOptionKey: "price-lock-assistant",
			"ModelPrice":                    `{"price-lock-assistant":1}`,
		})
	}))
}
