package relay

import (
	"bytes"
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
	originalDB := model.DB
	t.Cleanup(func() {
		model.DB = originalDB
	})

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Option{}, &model.ModerationJob{}))
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
	t.Run("current moderation", func(t *testing.T) {
		db := setupTaskModerationTestDB(t)
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
	})
}

func TestCheckAdvancedSecurityTaskPromptDefaultDisabledDoesNotEnqueueOrBlock(t *testing.T) {
	db := setupTaskModerationTestDB(t)
	// Retired literal configuration must not override the new opt-in default.
	c, info := newTaskModerationTestContext()
	require.Nil(t, checkAdvancedSecurityTaskPrompt(c, info))
	require.False(t, c.IsAborted())
	var jobCount int64
	require.NoError(t, db.Model(&model.ModerationJob{}).Count(&jobCount).Error)
	assert.Zero(t, jobCount)
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
