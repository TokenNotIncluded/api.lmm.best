package relay

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTaskModerationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	originalSettings := setting.GetAdvancedSecuritySettings()
	originalDB := model.DB
	t.Cleanup(func() {
		model.DB = originalDB
		setting.SetAdvancedSecurityEnabled(originalSettings.Enabled)
		setting.SetAdvancedSecurityOnPrompt(originalSettings.OnPrompt)
		require.NoError(t, setting.UpdateAdvancedSecurityAction(originalSettings.Action))
		require.NoError(t, setting.UpdateAdvancedSecurityRules(advancedSecurityRulesJSONForTest(originalSettings)))
	})

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.ModerationJob{}, &model.AdvancedSecurityEvent{}))
	require.NoError(t, db.Create(&model.User{Id: 42, Username: "task-moderation", Password: "offline-test-password", Group: "default", Status: common.UserStatusEnabled}).Error)
	model.DB = db
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			require.NoError(t, sqlDB.Close())
		}
	})
	return db
}

func enableLegacyTaskLiteralRule(t *testing.T) {
	t.Helper()
	setting.SetAdvancedSecurityEnabled(true)
	setting.SetAdvancedSecurityOnPrompt(true)
	require.NoError(t, setting.UpdateAdvancedSecurityAction(setting.AdvancedSecurityActionBlock))
	require.NoError(t, setting.UpdateAdvancedSecurityRules(`[{"id":"task-rule","category":"computer_network_compromise","enabled":true,"groups":["default"],"patterns":["blocked task prompt"]}]`))
}

func newTaskModerationTestContext() (*gin.Context, *relaycommon.RelayInfo) {
	const prompt = "please use a blocked task prompt now"
	body := []byte(`{"prompt":"please use a blocked task prompt now","system":"private system context","history":["earlier user input"],"tool_result":"private tool result"}`)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", bytes.NewReader(body))
	c.Set("id", 42)
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: prompt})
	return c, &relaycommon.RelayInfo{
		RequestId: "task-current-user-turn", UserId: 42,
		UserGroup: "default", UsingGroup: "task-routing-group",
	}
}

func TestCheckAdvancedSecurityTaskPromptQueuesCurrentInputWithoutBlocking(t *testing.T) {
	for _, legacyEnabled := range []bool{false, true} {
		name := "legacy disabled"
		if legacyEnabled {
			name = "legacy literal block enabled"
		}
		t.Run(name, func(t *testing.T) {
			db := setupTaskModerationTestDB(t)
			enableLegacyTaskLiteralRule(t)
			setting.SetAdvancedSecurityEnabled(legacyEnabled)
			settings := setting.DefaultModerationSettings()
			settings.Enabled = true
			settings.Group = "moderation-review-group"
			settings.GroupPolicies = map[string]setting.ModerationGroupPolicy{
				"default":            {Mode: setting.ModerationModeStrict, CategoryFinesUSD: map[string]float64{"violence": 0.5}},
				"task-routing-group": {Mode: setting.ModerationModeOff},
			}
			for key, value := range settings.OptionValues() {
				require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
			}
			c, info := newTaskModerationTestContext()
			require.Nil(t, checkAdvancedSecurityTaskPrompt(c, info))
			require.False(t, c.IsAborted())

			var jobs []model.ModerationJob
			require.NoError(t, db.Find(&jobs).Error)
			require.Len(t, jobs, 1)
			job := jobs[0]
			assert.Equal(t, info.RequestId, job.RequestID)
			assert.Equal(t, info.UserId, job.UserID)
			assert.Equal(t, model.ModerationSourceRelayInput, job.Source)
			assert.Equal(t, "default", job.Group)
			assert.Equal(t, settings.Group, job.ReviewGroup)
			assert.Equal(t, setting.DefaultModerationModel, job.ReviewModel)
			assert.Equal(t, setting.ModerationModeStrict, job.CapturedMode)
			assert.JSONEq(t, `{"violence":0.5}`, job.CapturedCategoryFinesJSON)
			assert.Equal(t, model.ModerationJobPending, job.Status)
			assert.Equal(t, "please use a blocked task prompt now", job.Payload)
			assert.NotEmpty(t, job.InputDigest)
			assert.NotContains(t, job.InputDigest, "blocked task prompt")
			assert.NotContains(t, job.Payload, "private system context")
			assert.NotContains(t, job.Payload, "earlier user input")
			assert.NotContains(t, job.Payload, "private tool result")
			var eventCount int64
			require.NoError(t, db.Model(&model.AdvancedSecurityEvent{}).Count(&eventCount).Error)
			assert.Zero(t, eventCount)
		})
	}
}

func TestCheckAdvancedSecurityTaskPromptDefaultDisabledDoesNotEnqueueOrBlock(t *testing.T) {
	db := setupTaskModerationTestDB(t)
	// Retired literal configuration must not override the new opt-in default.
	enableLegacyTaskLiteralRule(t)
	c, info := newTaskModerationTestContext()
	require.Nil(t, checkAdvancedSecurityTaskPrompt(c, info))
	require.False(t, c.IsAborted())
	var jobCount, eventCount int64
	require.NoError(t, db.Model(&model.ModerationJob{}).Count(&jobCount).Error)
	require.NoError(t, db.Model(&model.AdvancedSecurityEvent{}).Count(&eventCount).Error)
	assert.Zero(t, jobCount)
	assert.Zero(t, eventCount)
}

func TestTaskPromptFromContextSupportsSunoAndStandardRequests(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "standard prompt"})
	prompt, ok := taskPromptFromContext(c)
	assert.True(t, ok)
	assert.Equal(t, "standard prompt", prompt)

	c.Set("task_request", &struct{ Prompt string }{Prompt: "unsupported request"})
	_, ok = taskPromptFromContext(c)
	assert.False(t, ok)
}

func advancedSecurityRulesJSONForTest(settings setting.AdvancedSecuritySettings) string {
	encoded, err := json.Marshal(settings.RuleSet)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
