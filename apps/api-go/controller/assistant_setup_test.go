package controller

import (
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantChatClientSetupKeepsHostPathAndAccessBoundaries(t *testing.T) {
	const modelID = "test-chat-model"
	withAssistantModelRatios(t, modelID)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Ability{}, &model.Channel{}, &model.TopUp{}))
	previousPricing := getPricingCache
	getPricingCache = func() []model.Pricing {
		return []model.Pricing{{ModelName: modelID, EnableGroup: []string{"default"}}}
	}
	previousAddress := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.com/"
	t.Cleanup(func() {
		getPricingCache = previousPricing
		system_setting.ServerAddress = previousAddress
	})
	channel := model.Channel{Name: "setup-test", Status: common.ChannelStatusEnabled, Group: "default", Models: modelID}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: "default", Model: modelID, ChannelId: channel.Id, Enabled: true}).Error)

	for _, level := range []int{0, 1} {
		user := model.User{
			Username: fmt.Sprintf("setup-level-%d", level), Password: "password",
			AffCode: fmt.Sprintf("setup-aff-%d", level),
			Role:    common.RoleCommonUser, Status: common.UserStatusEnabled,
			Group: "default", TrustLevelOverride: &level,
		}
		require.NoError(t, db.Create(&user).Error)
		for _, platform := range []string{"windows", "macos", "linux", "android", "ios"} {
			for _, topic := range []string{"chatbox", "cherry-studio"} {
				t.Run(user.Username+"/"+platform+"/"+topic, func(t *testing.T) {
					result := executeAssistantSetupTool(user.Id, map[string]any{
						"platform": platform, "topic": topic, "model_id": modelID,
					})
					require.Equal(t, true, result["ok"])
					assert.Equal(t, level == 0, result["account_model_access_locked"])
					assert.Equal(t, "<YOUR_API_KEY>", result["api_key"])
					assert.NotContains(t, result, "install_command")
					if topic == "cherry-studio" && (platform == "android" || platform == "ios") {
						assert.Equal(t, false, result["supported"])
						assert.Equal(t, []string{"Chatbox"}, result["recommended_alternatives"])
						assert.NotContains(t, result, "steps")
						return
					}
					assert.Equal(t, true, result["supported"])
					assert.Equal(t, "https://api.example.com", result["client_api_host"])
					assert.Equal(t, "/v1/chat/completions", result["api_path"])
					assert.Equal(t, "https://api.example.com/v1", result["openai_base_url"])
					steps, ok := result["steps"].([]string)
					require.True(t, ok)
					assert.GreaterOrEqual(t, len(steps), 6)
					assert.Contains(t, strings.Join(steps, " "), modelID)
					if level == 0 {
						assert.Contains(t, strings.Join(steps, " "), "after L1 approval")
						assert.Contains(t, result["verification"], "After L1 approval")
					} else {
						assert.Contains(t, steps[len(steps)-1], "Reply with OK")
					}
					troubleshooting, ok := result["troubleshooting"].(map[string]string)
					require.True(t, ok)
					for _, status := range []string{"401", "404", "429"} {
						assert.NotEmpty(t, troubleshooting[status])
					}
					assert.Contains(t, troubleshooting["429"], "Retry-After")
				})
			}
		}
		for _, topic := range []string{"claude-code", "cc-switch", "codex", "cursor", "claude-desktop"} {
			result := executeAssistantSetupTool(user.Id, map[string]any{
				"platform": "ios", "topic": topic, "model_id": modelID,
			})
			assert.Equal(t, false, result["supported"], topic)
			assert.NotContains(t, result, "install_command", topic)
			assert.NotContains(t, result, "configuration", topic)
		}
	}
}
