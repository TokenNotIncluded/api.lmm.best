package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAssistantToolPolicyBulkSaveRejectsStaleBaselineWithoutPartialWrites(t *testing.T) {
	db := openTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.Log{}, &model.RatioNotification{}, &model.RatioDelivery{}))
	previous := setting.GetAssistantSettings().ToolPolicy
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAssistantToolPolicy(previous))
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	remote := `{"version":1,"groups":{"drawing":false},"tools":{}}`
	local := `{"version":1,"groups":{},"tools":{"search_web":false}}`
	require.NoError(t, db.Create(&model.Option{Key: setting.AssistantToolPolicyOptionKey, Value: remote}).Error)
	require.NoError(t, setting.UpdateAssistantToolPolicy(remote))
	body, err := json.Marshal(map[string]any{
		"values":          map[string]string{setting.AssistantToolPolicyOptionKey: local, "Notice": "must not save"},
		"expected_values": map[string]string{setting.AssistantToolPolicyOptionKey: setting.DefaultAssistantToolPolicy},
	})
	require.NoError(t, err)
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/option/bulk", strings.NewReader(string(body)))
	UpdateOptionsBulk(c)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "ASSISTANT_TOOL_POLICY_CONFLICT")
	var stored model.Option
	require.NoError(t, db.Where("key = ?", setting.AssistantToolPolicyOptionKey).First(&stored).Error)
	require.JSONEq(t, remote, stored.Value)
	require.False(t, setting.AssistantToolEnabled("prepare_image_generation"))
	require.True(t, setting.AssistantToolEnabled("search_web"))
	var noticeCount int64
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", "Notice").Count(&noticeCount).Error)
	require.Zero(t, noticeCount)
}

func TestAssistantToolPolicyBulkSaveRejectsUnsupportedExpectations(t *testing.T) {
	for _, body := range []string{
		`{"values":{"Notice":"x"},"expected_values":{"Notice":"old"}}`,
		`{"values":{"Notice":"x"},"expected_values":{"AssistantToolPolicy":""}}`,
		`{"values":{"AssistantToolPolicy":""},"expected_values":{"AssistantToolPolicy":"","Notice":"old"}}`,
		`{"values":{"AssistantToolPolicy":""},"expected_values":{"AssistantToolPolicy":false}}`,
	} {
		t.Run(body, func(t *testing.T) {
			response := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(response)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/option/bulk", strings.NewReader(body))
			UpdateOptionsBulk(c)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		})
	}
}
