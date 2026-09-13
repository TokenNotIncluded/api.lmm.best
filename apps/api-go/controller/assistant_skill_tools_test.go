package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExplicitProfileForgetRequestRequiresAProfileTarget(t *testing.T) {
	for _, test := range []struct {
		message string
		want    bool
	}{
		{message: "请删除我的 AI 用户标签", want: true},
		{message: "forget my personalization profile", want: true},
		{message: "请告诉我画像数据如何删除", want: false},
		{message: "我想了解隐私和数据删除", want: false},
		{message: "不要删除我的标签", want: false},
		{message: "delete my account", want: false},
	} {
		assert.Equal(t, test.want, assistantExplicitProfileForgetRequest(test.message), test.message)
	}
}

func TestExplicitProfileForgetNegativeRequestDoesNotTrigger(t *testing.T) {
	if assistantExplicitProfileForgetRequest("我不想删除我的 AI 画像和标签") {
		t.Fatal("negative profile request was treated as consent")
	}
	if !assistantExplicitProfileForgetRequest("不要解释，帮我删除我的 AI 画像和标签") {
		t.Fatal("explicit deletion after explanation refusal was not recognized")
	}
}

func TestExecuteForgetProfileRequiresCurrentUserRequest(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AssistantUserProfile{}))
	user := &model.User{
		Username: "forget-current-request-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffCode: "forget-current-request-user",
	}
	require.NoError(t, db.Create(user).Error)
	_, err := model.SaveProfile(user.Id, user.Id, model.ProfileInput{
		Key: model.AssistantProfileGuided, Tags: []string{"guided"}, Strategy: "short steps",
		Source: model.AssistantProfileSourceAI, Enabled: true,
	})
	require.NoError(t, err)

	for _, message := range []string{"隐私数据怎么保留？", "不要删除我的标签", "我不想删除我的 AI 画像和标签", "Please do not clear my AI profile", "只读检查：查看我的标签"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", user.Id)
		c.Set(assistantActorUserIDKey, user.Id)
		c.Set("assistant_history_latest_message", message)
		result := executeAssistantTool(c, assistantOpenAIToolCall{
			Function: assistantOpenAIToolCallFunction{Name: forgetProfileTool, Arguments: `{"confirm":true}`},
		})
		assert.Equal(t, "explicit_request_required", result["status"], message)
	}
	profile, err := model.GetAssistantUserProfile(user.Id)
	require.NoError(t, err)
	assert.NotNil(t, profile)
}

func TestForgetProfileSkillRequiresExplicitConfirmation(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AssistantUserProfile{}))
	user := &model.User{
		Username: "forget-skill-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffCode: "forget-skill-user",
	}
	require.NoError(t, db.Create(user).Error)
	_, err := model.SaveProfile(user.Id, user.Id, model.ProfileInput{
		Key: model.AssistantProfileGuided, Tags: []string{"guided"}, Strategy: "short steps",
		Source: model.AssistantProfileSourceAI, Enabled: true,
	})
	require.NoError(t, err)

	result, handled := runSkillTool(forgetProfileTool, user.Id, map[string]any{}, false)
	require.True(t, handled)
	assert.False(t, result["ok"].(bool))
	assert.Equal(t, "explicit_request_required", result["status"])

	result, handled = runSkillTool(forgetProfileTool, user.Id, map[string]any{"confirm": true}, false)
	require.True(t, handled)
	assert.False(t, result["ok"].(bool))
	assert.Equal(t, "explicit_request_required", result["status"])

	result, handled = runSkillTool(forgetProfileTool, user.Id, map[string]any{"confirm": true}, true)
	require.True(t, handled)
	assert.True(t, result["ok"].(bool))
	assert.Equal(t, "profile_skill_forgotten", result["status"])
	profile, err := model.GetAssistantUserProfile(user.Id)
	require.NoError(t, err)
	assert.Nil(t, profile)
}

func TestForgetProfileSkillRefusesAdministratorManagedProfile(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AssistantUserProfile{}))
	user := &model.User{
		Username: "forget-managed-skill-user", Password: "password", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", AffCode: "forget-managed-skill-user",
	}
	require.NoError(t, db.Create(user).Error)
	_, err := model.SaveProfile(user.Id, 99, model.ProfileInput{
		Key: model.AssistantProfileOperator, Tags: []string{"production"}, Strategy: "admin",
		Source: model.AssistantProfileSourceAdmin, Enabled: true,
	})
	require.NoError(t, err)

	result, handled := runSkillTool(forgetProfileTool, user.Id, map[string]any{"confirm": true}, true)
	require.True(t, handled)
	assert.False(t, result["ok"].(bool))
	assert.Equal(t, "administrator_managed", result["status"])
	profile, err := model.GetAssistantUserProfile(user.Id)
	require.NoError(t, err)
	assert.NotNil(t, profile)
}
