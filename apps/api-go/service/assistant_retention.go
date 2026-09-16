package service

import (
	"context"
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
)

const assistantRetentionBatchSize = 200

type AssistantRetentionPayload struct {
	model.AssistantRetentionCutoffs
	BatchSize int `json:"batch_size"`
}

type AssistantRetentionState struct {
	model.AssistantRetentionDeleteResult
	Progress int `json:"progress"`
}

type assistantRetentionHandler struct{}

func (assistantRetentionHandler) Type() string {
	return model.SystemTaskTypeAssistantRetention
}

func (assistantRetentionHandler) Enabled() bool {
	return setting.GetAssistantSettings().RetentionEnabled
}

func (assistantRetentionHandler) Interval() time.Duration {
	hours := setting.GetAssistantSettings().RetentionIntervalHours
	if hours < 1 {
		hours = 24
	}
	return time.Duration(hours) * time.Hour
}

func (assistantRetentionHandler) NewPayload() any {
	settings := setting.GetAssistantSettings()
	return AssistantRetentionPayload{
		AssistantRetentionCutoffs: model.AssistantRetentionCutoffsFromNow(
			time.Now(),
			settings.ActiveRetentionDays,
			settings.ArchivedRetentionDays,
			settings.SecurityRetentionDays,
		),
		BatchSize: assistantRetentionBatchSize,
	}
}

func (assistantRetentionHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := AssistantRetentionPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	if payload.ActiveBefore <= 0 || payload.ArchivedBefore <= 0 || payload.RestrictedBefore <= 0 {
		failSystemTask(task, runnerID, errors.New("assistant retention cutoffs are required"))
		return
	}
	if err := model.PruneAssistantRegistrationFingerprints(time.Now()); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}
	payload.BatchSize = model.NormalizeAssistantRetentionBatchSize(payload.BatchSize)

	state := AssistantRetentionState{}
	if err := task.DecodeState(&state); err != nil {
		failSystemTask(task, runnerID, err)
		return
	}

	for {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		deleted, err := model.PurgeAssistantConversationsBefore(ctx, payload.AssistantRetentionCutoffs, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if deleted.Conversations == 0 {
			break
		}
		state.Conversations += deleted.Conversations
		state.Messages += deleted.Messages
		state.SecureCards += deleted.SecureCards
		state.Incidents += deleted.Incidents
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		deleted, err := model.PurgeAssistantProfileBucketsBefore(ctx, payload.ActiveBefore, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if deleted == 0 {
			break
		}
		state.ProfileBuckets += deleted
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		deleted, err := model.PurgeAssistantFirstQuestionsBefore(ctx, payload.ActiveBefore, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if deleted == 0 {
			break
		}
		state.FirstQuestions += deleted
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		deleted, err := model.PurgeAssistantIntentLeadsBefore(ctx, payload.ActiveBefore, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if deleted == 0 {
			break
		}
		state.IntentLeads += deleted
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		deleted, err := model.PurgeAssistantUserProfileAuditsBefore(ctx, payload.ActiveBefore, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if deleted == 0 {
			break
		}
		state.ProfileAudits += deleted
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		deleted, err := model.PurgeAdvancedSecurityEventsBefore(ctx, payload.RestrictedBefore, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if deleted == 0 {
			break
		}
		state.SecurityEvents += deleted
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		deleted, err := model.PurgeAssistantGiftNetworkRiskBefore(ctx, payload.RestrictedBefore, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if deleted == 0 {
			break
		}
		state.GiftRiskMemory += deleted
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		deleted, err := model.PurgeAssistantRequestReviewsBefore(ctx, payload.RestrictedBefore, payload.BatchSize)
		if err != nil {
			failSystemTask(task, runnerID, err)
			return
		}
		if deleted == 0 {
			break
		}
		state.RequestReviews += deleted
		if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
			logSystemTaskLockError(ctx, task, err)
			return
		}
	}

	state.Progress = 100
	if err := model.UpdateSystemTaskState(task.TaskID, runnerID, state); err != nil {
		logSystemTaskLockError(ctx, task, err)
		return
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, model.SystemTaskStatusSucceeded, state.AssistantRetentionDeleteResult, ""); err != nil {
		logSystemTaskLockError(ctx, task, err)
	}
}

func init() {
	RegisterSystemTaskHandler(assistantRetentionHandler{})
}
