package model

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setPriceLockTestOptions(t *testing.T, options map[string]string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = options
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
}

func TestModelPricingLocksDefaultUnlockedAndValidateBooleans(t *testing.T) {
	setPriceLockTestOptions(t, map[string]string{})
	assert.False(t, IsModelPricingLocked("example"))
	for _, value := range []string{"", "null", "[]", `{"example":1}`, `{"example":null}`, `{"":true}`} {
		assert.Error(t, ValidateOptionValue(ModelPriceLockOptionKey, value), value)
	}
	for _, value := range []string{"{}", `{"example":false}`, `{"example":true}`} {
		assert.NoError(t, ValidateOptionValue(ModelPriceLockOptionKey, value), value)
	}
}

func TestFilterLockedModelPricingProtectsEveryPricingMap(t *testing.T) {
	for key := range modelPricingOptionKeys {
		t.Run(key, func(t *testing.T) {
			setPriceLockTestOptions(t, map[string]string{
				ModelPriceLockOptionKey: `{"locked":true,"defaulted":true}`,
				key:                     `{"locked":1,"editable":2}`,
			})
			for _, candidate := range []string{
				`{"locked":"invalid attempted price","editable":3,"defaulted":4}`,
				`{"editable":3,"defaulted":4}`,
			} {
				values := map[string]string{key: candidate, "SystemName": "kept"}
				filtered, warnings, err := FilterLockedModelPricing(values)
				require.NoError(t, err)
				assert.JSONEq(t, `{"locked":1,"editable":3}`, filtered[key])
				assert.Equal(t, "kept", filtered["SystemName"])
				assert.Len(t, warnings, 2)
				assert.Equal(t, candidate, values[key], "do not mutate the caller's proposal")
			}
		})
	}
}

func TestLockedModelPricingFiltersBeforeValidation(t *testing.T) {
	setPriceLockTestOptions(t, map[string]string{
		ModelPriceLockOptionKey:        `{"locked":true}`,
		"ModelPrice":                   `{"locked":1,"editable":2}`,
		"billing_setting.billing_mode": `{"locked":"ratio"}`,
		"billing_setting.billing_expr": `{"locked":""}`,
	})
	warnings, err := ValidateOptionValuesWithWarnings(map[string]string{
		"ModelPrice":                   `{"locked":null,"editable":3}`,
		"billing_setting.billing_mode": `{"locked":"invalid"}`,
		"billing_setting.billing_expr": `{"locked":"broken("}`,
	})
	require.NoError(t, err)
	assert.Len(t, warnings, 1)
	_, err = ValidateOptionValuesWithWarnings(map[string]string{"ModelPrice": `{"locked":null,"editable":"invalid"}`})
	assert.Error(t, err)
}

func TestModelPricingLocksFollowRuntimeModelAliases(t *testing.T) {
	for key, normalize := range modelPricingOptionKeys {
		if !normalize {
			continue
		}
		t.Run(key, func(t *testing.T) {
			setPriceLockTestOptions(t, map[string]string{
				ModelPriceLockOptionKey: `{"gemini-2.5-pro-thinking-1024":true}`,
				key:                     `{"gemini-2.5-pro-thinking-*":2}`,
			})
			assert.True(t, IsModelPricingLocked("gemini-2.5-pro-thinking-2048"))
			filtered, warnings, err := FilterLockedModelPricing(map[string]string{
				key: `{"gemini-2.5-pro-thinking-*":9,"gemini-2.5-pro-thinking-2048":7,"other":1}`,
			})
			require.NoError(t, err)
			assert.JSONEq(t, `{"gemini-2.5-pro-thinking-*":2,"other":1}`, filtered[key])
			assert.NotEmpty(t, warnings)
		})
	}
}

func TestUnchangedLockedModelPricingDoesNotWarn(t *testing.T) {
	setPriceLockTestOptions(t, map[string]string{
		ModelPriceLockOptionKey: `{"locked":true}`,
		"ModelPrice":            `{"locked":1}`,
	})
	_, warnings, err := FilterLockedModelPricing(map[string]string{"ModelPrice": `{"locked":1.0}`})
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

func TestClearPricingMapRetainsLockedEntries(t *testing.T) {
	setPriceLockTestOptions(t, map[string]string{
		ModelPriceLockOptionKey: `{"locked":true}`,
		"ModelPrice":            `{"locked":1,"editable":2}`,
	})
	for _, clearValue := range []string{"{}", "null"} {
		filtered, warnings, err := FilterLockedModelPricing(map[string]string{"ModelPrice": clearValue})
		require.NoError(t, err)
		assert.JSONEq(t, `{"locked":1}`, filtered["ModelPrice"])
		assert.Len(t, warnings, 1)
	}
}

func TestUpdateLockedModelPricingPersistsWarningsAndRequiresSeparateUnlock(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "price-lock.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&Option{}))
	previousDB := DB
	DB = database
	previousPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		DB = previousDB
		_ = ratio_setting.UpdateModelPriceByJSONString(previousPrices)
		sqlDB, err := database.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	setPriceLockTestOptions(t, map[string]string{})
	require.NoError(t, UpdateOptionsBulk(map[string]string{
		"ModelPrice":            `{"locked":1,"editable":2}`,
		ModelPriceLockOptionKey: `{"locked":true}`,
	}))
	warnings, err := UpdateOptionsBulkWithWarnings(map[string]string{
		"ModelPrice":            `{"locked":null,"editable":3}`,
		ModelPriceLockOptionKey: "{}",
	})
	require.NoError(t, err)
	assert.Len(t, warnings, 1)
	var option Option
	require.NoError(t, database.First(&option, "key = ?", "ModelPrice").Error)
	assert.JSONEq(t, `{"locked":1,"editable":3}`, option.Value)
	assert.JSONEq(t, option.Value, ratio_setting.ModelPrice2JSONString())
	assert.False(t, IsModelPricingLocked("locked"))
	warnings, err = UpdateOptionWithWarnings("ModelPrice", `{"locked":5,"editable":3}`)
	require.NoError(t, err)
	assert.Empty(t, warnings)
	var prices map[string]float64
	require.NoError(t, json.Unmarshal([]byte(ratio_setting.ModelPrice2JSONString()), &prices))
	assert.Equal(t, float64(5), prices["locked"])

	_, err = UpdateOptionsBulkWithWarnings(map[string]string{
		ModelPriceLockOptionKey: `{"locked":null}`,
		"ModelPrice":            `{"locked":99}`,
	})
	require.Error(t, err)
	require.NoError(t, database.First(&option, "key = ?", "ModelPrice").Error)
	assert.JSONEq(t, `{"locked":5,"editable":3}`, option.Value)
}
