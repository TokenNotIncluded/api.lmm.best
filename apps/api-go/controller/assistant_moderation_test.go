package controller

import (
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func assistantModerationFixture(t *testing.T, enabled bool) (*gorm.DB, model.User) {
	t.Helper()
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis := common.RedisEnabled
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedis
	})
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.ModerationJob{}, &model.AssistantTurnReceipt{}))
	user := model.User{Username: "moderation-actor", Password: "offline-password", Group: "member", Quota: 1000, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, ConsoleActivatedAt: 1}
	require.NoError(t, db.Create(&user).Error)
	if enabled {
		values := setting.DefaultModerationSettings().OptionValues()
		values[setting.AssistantModerationEnabledOptionKey] = "true"
		values[setting.ModerationGroupPoliciesOptionKey] = `{"member":{"mode":"strict","category_fines_usd":{"hate":0.1}}}`
		for key, value := range values {
			require.NoError(t, db.Create(&model.Option{Key: key, Value: value}).Error)
		}
	}
	return db, user
}

func TestAssistantModerationUsesActorAndStableTurnInsteadOfRelayPayer(t *testing.T) {
	db, actor := assistantModerationFixture(t, true)
	c, _ := newAuthenticatedContext(t, http.MethodPost, "/api/assistant/chat", nil, 999)
	c.Set(assistantActorUserIDKey, actor.Id)
	c.Set(assistantActorGroupKey, actor.Group)
	c.Set("assistant_client_turn_id", "stable-current-turn-0001")
	c.Set(common.RequestIdKey, "first-transport-request")
	c.Set("assistant_conversation", []assistantOpenAIMessage{{Role: "system", Content: "private system prompt"}, {Role: "tool", Content: "private tool payload"}})
	queueAssistantModerationText(c, service.ModerationSourceAssistantInput, "current user turn")
	c.Set("assistant_history_canonical_content", "saved canonical answer")
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"transient unsaved answer"}}]}`)
	queueAssistantModerationResponse(c, http.StatusOK, body)
	c.Set(common.RequestIdKey, "second-transport-request")
	queueAssistantModerationText(c, service.ModerationSourceAssistantInput, "current user turn")
	queueAssistantModerationResponse(c, http.StatusOK, body)
	var jobs []model.ModerationJob
	require.NoError(t, db.Order("id").Find(&jobs).Error)
	require.Len(t, jobs, 2, "a transport retry must not duplicate either review")
	for _, job := range jobs {
		require.Equal(t, actor.Id, job.UserID)
		require.Equal(t, actor.Group, job.Group)
		require.Equal(t, "assistant:stable-current-turn-0001", job.RequestID)
		require.Equal(t, model.ModerationJobPending, job.Status)
		require.NotContains(t, job.Payload, "private")
		require.NotContains(t, job.Payload, "transient")
	}
	require.Equal(t, "current user turn", jobs[0].Payload)
	require.Equal(t, "saved canonical answer", jobs[1].Payload)
	var unchanged model.User
	require.NoError(t, db.First(&unchanged, actor.Id).Error)
	require.Equal(t, actor.Quota, unchanged.Quota, "enqueue must never synchronously fine a user")
}

func TestAssistantSavedAnswerQueuesCanonicalOutputOnce(t *testing.T) {
	db, actor := assistantModerationFixture(t, true)
	c, _ := newAuthenticatedContext(t, http.MethodPost, "/api/assistant/chat", nil, actor.Id)
	c.Set(assistantActorGroupKey, actor.Group)
	c.Set("assistant_history_latest_message", "my current question")
	c.Set("assistant_client_turn_id", "saved-answer-turn-0001")
	body := []byte(`{"choices":[{"message":{"role":"assistant","content":"first saved answer"}}]}`)
	recordAssistantHistoryResponse(c, http.StatusOK, body)
	require.Positive(t, assistantHistoryConversationID(c))
	recordAssistantHistoryResponse(c, http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"different retry answer"}}]}`))
	var jobs []model.ModerationJob
	require.NoError(t, db.Find(&jobs).Error)
	require.Len(t, jobs, 1)
	require.Equal(t, service.ModerationSourceAssistantOutput, jobs[0].Source)
	require.Equal(t, "first saved answer", jobs[0].Payload)
}

func TestAssistantModerationDefaultsOffAndSkipsFailedResponses(t *testing.T) {
	db, actor := assistantModerationFixture(t, false)
	c, _ := newAuthenticatedContext(t, http.MethodPost, "/api/assistant/chat", nil, actor.Id)
	c.Set(assistantActorGroupKey, actor.Group)
	c.Set(common.RequestIdKey, "request-with-no-policy")
	queueAssistantModerationText(c, service.ModerationSourceAssistantInput, "user turn")
	queueAssistantModerationResponse(c, http.StatusOK, []byte(`{"choices":[{"message":{"content":"answer"}}]}`))
	var count int64
	require.NoError(t, db.Model(&model.ModerationJob{}).Count(&count).Error)
	require.Zero(t, count)
	queueAssistantModerationResponse(c, http.StatusBadGateway, []byte(`{"choices":[{"message":{"content":"error body"}}]}`))
	require.NoError(t, db.Model(&model.ModerationJob{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestAssistantModerationReviewsDeliveredAnswerWhenHistoryStorageFails(t *testing.T) {
	db, actor := assistantModerationFixture(t, true)
	require.NoError(t, db.Migrator().DropTable(&model.AssistantHistoryMessage{}))
	c, _ := newAuthenticatedContext(t, http.MethodPost, "/api/assistant/chat", nil, actor.Id)
	c.Set(assistantActorGroupKey, actor.Group)
	c.Set("assistant_history_latest_message", "current user question")
	c.Set("assistant_client_turn_id", "history-outage-turn-0001")
	body := []byte(`{"choices":[{"message":{"content":"answer delivered during history outage"}}]}`)
	recordAssistantHistoryResponse(c, http.StatusOK, body)
	require.False(t, c.GetBool("assistant_turn_unavailable"))
	var jobs []model.ModerationJob
	require.NoError(t, db.Find(&jobs).Error)
	require.Len(t, jobs, 1)
	require.Equal(t, service.ModerationSourceAssistantOutput, jobs[0].Source)
	require.Equal(t, "answer delivered during history outage", jobs[0].Payload)
}
