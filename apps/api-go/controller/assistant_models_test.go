/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAssistantModelsEnumeratesEnabledIDsForTheRequestedGroup(t *testing.T) {
	originalRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"premium":1}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalRatios)) })

	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Ability{}, &model.Channel{}))
	require.NoError(t, db.Create(&[]model.Channel{
		{Id: 1, Name: "assistant-models-live", Key: "sk-live", Status: common.ChannelStatusEnabled},
		{Id: 2, Name: "assistant-models-disabled", Key: "sk-disabled", Status: common.ChannelStatusManuallyDisabled},
	}).Error)
	require.NoError(t, db.Create(&[]model.Ability{
		{Group: "premium", Model: "z-model", ChannelId: 1, Enabled: true},
		{Group: "premium", Model: "a-model", ChannelId: 1, Enabled: true},
		{Group: "premium", Model: "disabled-ability-model", ChannelId: 1, Enabled: false},
		{Group: "premium", Model: "disabled-channel-model", ChannelId: 2, Enabled: true},
	}).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/assistant/models?group=premium", nil)
	GetAssistantModels(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Success bool     `json:"success"`
		Data    []string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	assert.True(t, payload.Success)
	assert.Equal(t, []string{"a-model", "z-model"}, payload.Data)
}
