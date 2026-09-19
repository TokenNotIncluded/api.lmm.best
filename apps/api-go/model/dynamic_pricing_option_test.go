package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestRetiredDynamicPricingOptionsCannotBeLoadedOrWritten(t *testing.T) {
	oldDB, oldOptions := DB, common.OptionMap
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Option{}))
	DB = db
	common.OptionMap = map[string]string{"dynamic_pricing_setting.enabled": "true"}
	t.Cleanup(func() { DB = oldDB; common.OptionMap = oldOptions; sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	for _, key := range []string{"dynamic_pricing_setting", "dynamic_pricing_setting.enabled", "dynamic_pricing_setting.min_factor", "dynamic_pricing_setting.any_future_field"} {
		require.NoError(t, db.Create(&Option{Key: key, Value: "true"}).Error)
		require.ErrorContains(t, ValidateOptionValue(key, "true"), "removed")
		require.Error(t, UpdateOption(key, "false"))
		require.Error(t, UpdateOptionsBulk(map[string]string{key: "false", "GroupRatio": `{"default":1}`}))
		require.NoError(t, updateOptionMap(key, "true"))
		require.NotContains(t, common.OptionMap, key)
	}
	options, err := AllOption()
	require.NoError(t, err)
	require.Empty(t, options)
	var persisted []Option
	require.NoError(t, db.Find(&persisted).Error)
	require.Len(t, persisted, 4)
	for _, option := range persisted {
		require.Equal(t, "true", option.Value)
	}
	require.Nil(t, config.GlobalConfig.Get("dynamic_pricing_setting"))
	require.NoError(t, ValidateOptionValue("GroupRatio", `{"default":1.5}`))
}
