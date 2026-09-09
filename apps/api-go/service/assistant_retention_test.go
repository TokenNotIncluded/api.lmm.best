package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantRetentionHandlerUsesConfiguredScheduleAndPrivacySafePayload(t *testing.T) {
	original := setting.GetAssistantSettings()
	t.Cleanup(func() {
		setting.SetAssistantRetentionEnabled(original.RetentionEnabled)
		_ = setting.UpdateAssistantActiveRetentionDays(strconv.Itoa(original.ActiveRetentionDays))
		_ = setting.UpdateAssistantArchivedRetentionDays(strconv.Itoa(original.ArchivedRetentionDays))
		_ = setting.UpdateAssistantSecurityRetentionDays(strconv.Itoa(original.SecurityRetentionDays))
		_ = setting.UpdateAssistantRetentionIntervalHours(strconv.Itoa(original.RetentionIntervalHours))
	})

	setting.SetAssistantRetentionEnabled(true)
	require.NoError(t, setting.UpdateAssistantActiveRetentionDays("120"))
	require.NoError(t, setting.UpdateAssistantArchivedRetentionDays("45"))
	require.NoError(t, setting.UpdateAssistantSecurityRetentionDays("365"))
	require.NoError(t, setting.UpdateAssistantRetentionIntervalHours("12"))

	handler := assistantRetentionHandler{}
	assert.True(t, handler.Enabled())
	assert.Equal(t, 12*time.Hour, handler.Interval())
	payload, ok := handler.NewPayload().(AssistantRetentionPayload)
	require.True(t, ok)
	assert.Equal(t, assistantRetentionBatchSize, payload.BatchSize)
	now := time.Now().Unix()
	assert.InDelta(t, now-int64(120*24*time.Hour/time.Second), payload.ActiveBefore, 2)
	assert.InDelta(t, now-int64(45*24*time.Hour/time.Second), payload.ArchivedBefore, 2)
	assert.InDelta(t, now-int64(365*24*time.Hour/time.Second), payload.RestrictedBefore, 2)
}

func TestAssistantRetentionHandlerDeletesInBatchesAndFinishesTask(t *testing.T) {
	truncate(t)
	require.NoError(t, model.DB.AutoMigrate(
		&model.AssistantLead{},
		&model.AssistantUserProfileAudit{},
		&model.AssistantProfileBucket{},
		&model.AssistantFirstQuestionStat{},
		&model.AssistantConversation{}, &model.AssistantSupportRequest{},
		&model.AssistantHistoryMessage{},
		&model.AssistantSecureCard{},
		&model.AssistantSecurityIncident{},
		&model.AdvancedSecurityEvent{},
		&model.AssistantGiftRiskMemory{},
		&model.UnifiedTodoRead{},
	))
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM assistant_leads")
		model.DB.Exec("DELETE FROM assistant_user_profile_audits")
		model.DB.Exec("DELETE FROM assistant_profile_buckets")
		model.DB.Exec("DELETE FROM assistant_first_question_stats")
		model.DB.Exec("DELETE FROM unified_todo_reads")
		model.DB.Exec("DELETE FROM assistant_security_incidents")
		model.DB.Exec("DELETE FROM assistant_secure_cards")
		model.DB.Exec("DELETE FROM assistant_history_messages")
		model.DB.Exec("DELETE FROM assistant_conversations")
		model.DB.Exec("DELETE FROM advanced_security_events")
		model.DB.Exec("DELETE FROM assistant_gift_risk_memories")
	})
	require.NoError(t, model.DB.Create(&[]model.AssistantLead{
		{UserId: 100, Source: model.AssistantLeadSourceChat, Intent: model.AssistantIntentAPIKey, Status: model.AssistantLeadStatusObserved, CreatedAt: 1},
		{UserId: 100, Source: model.AssistantLeadSourceChat, Intent: model.AssistantIntentCost, Status: model.AssistantLeadStatusObserved, CreatedAt: 2},
		{UserId: 100, Source: model.AssistantLeadSourceChat, Intent: model.AssistantIntentUsage, Status: model.AssistantLeadStatusObserved, CreatedAt: 11},
		{UserId: 100, Source: model.AssistantLeadSourceHandoff, Intent: model.AssistantIntentHumanSupport, Status: model.AssistantLeadStatusPending, CreatedAt: 1, Message: "keep support queue"},
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.AssistantUserProfileAudit{
		{UserId: 100, Source: model.AssistantProfileSourceAI, CreatedAt: 1},
		{UserId: 100, Source: model.AssistantProfileSourceAI, CreatedAt: 2},
		{UserId: 100, Source: model.AssistantProfileSourceAI, CreatedAt: 11},
		{UserId: 100, Source: model.AssistantProfileSourceAdmin, CreatedAt: 1},
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.AssistantProfileBucket{
		{Profile: model.AssistantProfileUnknown, BucketStart: 1, Count: 2},
		{Profile: model.AssistantProfileTechnical, BucketStart: 11, Count: 3},
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.AssistantFirstQuestionStat{
		{QuestionHash: "old-question", Question: "old question", BucketStart: 1, Count: 2, LastAskedAt: 1},
		{QuestionHash: "new-question", Question: "new question", BucketStart: 11, Count: 3, LastAskedAt: 11},
	}).Error)

	for index := range 3 {
		conversation := model.AssistantConversation{
			UserId:             100 + index,
			Title:              "old",
			LastMessagePreview: "old",
			CreatedAt:          1,
			UpdatedAt:          1,
		}
		require.NoError(t, model.DB.Create(&conversation).Error)
		require.NoError(t, model.DB.Create(&model.AssistantHistoryMessage{
			ConversationId: conversation.Id,
			Sequence:       1,
			Role:           model.AssistantHistoryRoleUser,
			Content:        "redacted",
			CreatedAt:      1,
		}).Error)
	}
	require.NoError(t, model.DB.Create(&[]model.AdvancedSecurityEvent{
		{CreatedAt: 1, RequestID: "old-request-1", RuleID: "rule", Category: "category", Decision: model.AdvancedSecurityDecisionAudited},
		{CreatedAt: 1, RequestID: "old-request-2", RuleID: "rule", Category: "category", Decision: model.AdvancedSecurityDecisionBlocked},
		{CreatedAt: 11, RequestID: "new-request", RuleID: "rule", Category: "category", Decision: model.AdvancedSecurityDecisionAudited},
	}).Error)
	require.NoError(t, model.DB.Create(&[]model.AssistantGiftRiskMemory{
		{KeyHash: "old-network-1", Kind: "network", DecisionCount: 1, WindowStartedAt: 1, UpdatedAt: 1},
		{KeyHash: "old-network-2", Kind: "network", DecisionCount: 2, WindowStartedAt: 2, UpdatedAt: 2},
		{KeyHash: "new-network", Kind: "network", DecisionCount: 1, WindowStartedAt: 11, UpdatedAt: 11},
		{KeyHash: "old-identity", Kind: "identity", DecisionCount: 1, WindowStartedAt: 1, UpdatedAt: 1},
	}).Error)

	payload := AssistantRetentionPayload{
		AssistantRetentionCutoffs: model.AssistantRetentionCutoffs{
			ActiveBefore:     10,
			ArchivedBefore:   10,
			RestrictedBefore: 10,
		},
		BatchSize: 2,
	}
	task, err := model.CreateSystemTask(model.SystemTaskTypeAssistantRetention, payload, AssistantRetentionState{})
	require.NoError(t, err)
	claimedTask, claimed, err := model.ClaimSystemTask(task.ID, task.Type, "retention-runner", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)

	assistantRetentionHandler{}.Run(context.Background(), claimedTask, "retention-runner")

	stored, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, model.SystemTaskStatusSucceeded, stored.Status)
	state := AssistantRetentionState{}
	require.NoError(t, stored.DecodeState(&state))
	assert.EqualValues(t, 3, state.Conversations)
	assert.EqualValues(t, 3, state.Messages)
	assert.EqualValues(t, 2, state.IntentLeads)
	assert.EqualValues(t, 2, state.ProfileAudits)
	assert.EqualValues(t, 1, state.ProfileBuckets)
	assert.EqualValues(t, 1, state.FirstQuestions)
	assert.EqualValues(t, 2, state.SecurityEvents)
	assert.EqualValues(t, 2, state.GiftRiskMemory)
	assert.Equal(t, 100, state.Progress)
	var remaining int64
	require.NoError(t, model.DB.Model(&model.AssistantConversation{}).Count(&remaining).Error)
	assert.Zero(t, remaining)
	require.NoError(t, model.DB.Model(&model.AdvancedSecurityEvent{}).Where("created_at < ?", 10).Count(&remaining).Error)
	assert.Zero(t, remaining)
	require.NoError(t, model.DB.Model(&model.AdvancedSecurityEvent{}).Where("created_at >= ?", 10).Count(&remaining).Error)
	assert.EqualValues(t, 1, remaining)
	require.NoError(t, model.DB.Model(&model.AssistantLead{}).Where("source = ? AND created_at < ?", model.AssistantLeadSourceChat, 10).Count(&remaining).Error)
	assert.Zero(t, remaining)
	require.NoError(t, model.DB.Model(&model.AssistantLead{}).Where("source = ?", model.AssistantLeadSourceHandoff).Count(&remaining).Error)
	assert.EqualValues(t, 1, remaining)
	require.NoError(t, model.DB.Model(&model.AssistantUserProfileAudit{}).Where("source = ? AND created_at < ?", model.AssistantProfileSourceAI, 10).Count(&remaining).Error)
	assert.Zero(t, remaining)
	require.NoError(t, model.DB.Model(&model.AssistantUserProfileAudit{}).Where("source = ?", model.AssistantProfileSourceAdmin).Count(&remaining).Error)
	assert.EqualValues(t, 1, remaining)
	require.NoError(t, model.DB.Model(&model.AssistantGiftRiskMemory{}).Where("kind = ? AND updated_at < ?", "network", 10).Count(&remaining).Error)
	assert.Zero(t, remaining)
	require.NoError(t, model.DB.Model(&model.AssistantGiftRiskMemory{}).Where("key_hash = ?", "new-network").Count(&remaining).Error)
	assert.EqualValues(t, 1, remaining)
	require.NoError(t, model.DB.Model(&model.AssistantGiftRiskMemory{}).Where("key_hash = ?", "old-identity").Count(&remaining).Error)
	assert.EqualValues(t, 1, remaining)
}
