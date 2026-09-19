// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package model

import (
	"context"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestModelRuntimeConfigurationAndNotices(t *testing.T) {
	useSingleConnectionTestDB(t, &Model{}, &Channel{}, &Ability{})
	for _, m := range []Model{{ModelName: "ready", Status: 1}, {ModelName: "maintenance", Status: 1, OperationalStatus: "maintenance", OperationalUntil: 200, OperationalNotice: "Scheduled update"}, {ModelName: "expired", Status: 1, OperationalStatus: "congested", OperationalUntil: 99}, {ModelName: "private", Status: 1}, {ModelName: "offline", Status: 1}, {ModelName: "hidden", Status: 1}, {ModelName: "pool", Status: 1}} {
		row := m
		require.NoError(t, DB.Create(&row).Error)
	}
	require.NoError(t, DB.Model(&Model{}).Where("model_name = ?", "hidden").Update("status", 0).Error)
	enabled := Channel{Name: "not-public", Status: common.ChannelStatusEnabled, Key: "not-public-key"}
	require.NoError(t, DB.Create(&enabled).Error)
	disabled := Channel{Name: "off", Status: common.ChannelStatusManuallyDisabled}
	require.NoError(t, DB.Create(&disabled).Error)
	pool := Channel{Name: "pool", Status: common.ChannelStatusEnabled, ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeySize: 2, MultiKeyStatusList: map[int]int{0: 2, 1: 2}}}
	require.NoError(t, DB.Create(&pool).Error)
	for _, name := range []string{"ready", "maintenance", "expired", "hidden"} {
		require.NoError(t, DB.Create(&Ability{Model: name, Group: "default", ChannelId: enabled.Id, Enabled: true}).Error)
	}
	require.NoError(t, DB.Create(&Ability{Model: "private", Group: "vip", ChannelId: enabled.Id, Enabled: true}).Error)
	require.NoError(t, DB.Create(&Ability{Model: "offline", Group: "default", ChannelId: disabled.Id, Enabled: true}).Error)
	require.NoError(t, DB.Create(&Ability{Model: "pool", Group: "default", ChannelId: pool.Id, Enabled: true}).Error)
	names := []string{"ready", "maintenance", "expired", "private", "offline", "hidden", "pool", "missing"}
	states, err := ModelRuntimeStates(context.Background(), names, map[string]string{"default": ""}, true, true, 100)
	require.NoError(t, err)
	for name, want := range map[string]string{"ready": "available", "maintenance": "maintenance", "expired": "available", "private": "no_access", "offline": "unavailable", "hidden": "not_listed", "pool": "unavailable", "missing": "not_listed"} {
		require.Equal(t, want, states[name].Status, name)
	}
	require.Equal(t, "administrator_notice", states["maintenance"].Source)
	require.Equal(t, int64(200), states["maintenance"].ExpiresAt)
	guest, err := ModelRuntimeStates(context.Background(), []string{"private"}, nil, false, false, 100)
	require.NoError(t, err)
	require.Equal(t, "available", guest["private"].Status)
	limited, err := ModelRuntimeStates(context.Background(), []string{"ready"}, map[string]string{"default": ""}, true, false, 100)
	require.NoError(t, err)
	require.Equal(t, "no_access", limited["ready"].Status)
}
func TestModelOperationalNoticeValidation(t *testing.T) {
	for _, m := range []Model{{OperationalStatus: "healthy"}, {OperationalStatus: "maintenance", OperationalUntil: 100}, {OperationalStatus: "congested", OperationalUntil: 100 + 31*86400}} {
		require.Error(t, ValidateModelOperationalNotice(&m, 100))
	}
	m := Model{OperationalStatus: "maintenance", OperationalUntil: 200, OperationalNotice: "Public notice"}
	require.NoError(t, ValidateModelOperationalNotice(&m, 100))
	m.OperationalStatus = "auto"
	require.NoError(t, ValidateModelOperationalNotice(&m, 100))
	require.Empty(t, m.OperationalNotice)
	require.Zero(t, m.OperationalUntil)
}
