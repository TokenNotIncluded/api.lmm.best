// Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
package controller

import (
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestModelRuntimePublicProjectionAndInputBounds(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Model{}, &model.Ability{}, &model.Channel{}))
	channel := model.Channel{Name: "private-internal-name", Key: "private-upstream-credential", Status: 1}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Model: "public-model", Group: "private-group", ChannelId: channel.Id, Enabled: true}).Error)
	c, w := newAuthenticatedContext(t, http.MethodPost, "/api/pricing/runtime", map[string]any{"models": []string{"public-model"}}, 0)
	GetModelRuntimeStates(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"status":"available"`)
	for _, secret := range []string{channel.Name, channel.Key, "private-group", "channel_id"} {
		require.NotContains(t, w.Body.String(), secret)
	}
	for _, names := range [][]string{nil, {""}, make([]string, 201)} {
		c, w := newAuthenticatedContext(t, http.MethodPost, "/api/pricing/runtime", map[string]any{"models": names}, 0)
		GetModelRuntimeStates(c)
		require.False(t, decodeAPIResponse(t, w).Success)
	}
}
