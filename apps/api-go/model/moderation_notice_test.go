package model

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestModerationNoticeOwnerACLAndReadIsolation(t *testing.T) {
	db, owner := setupModerationEffectsTestDB(t)
	other := &User{Username: "moderation-other", AffCode: "moderation-other", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(other).Error)
	notice := ModerationNotice{UserID: owner.Id, JobID: 1, RequestID: "notice-input", Source: ModerationSourceRelayInput, Mode: "tolerant", CategoriesJSON: `["violence"]`, CreatedAt: 100, UpdatedAt: 100}
	require.NoError(t, db.Create(&notice).Error)
	count, err := unifiedModerationNoticeCount(db, owner.Id, true)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	count, err = unifiedModerationNoticeCount(db, other.Id, true)
	require.NoError(t, err)
	require.Zero(t, count, "root inbox is still owner-only")
	rows, err := unifiedModerationNoticeCandidates(db, other.Id, []int{notice.ID})
	require.NoError(t, err)
	require.Empty(t, rows)
	marked, err := markUnifiedGenericTodosRead(db, other.Id, other.Role, UnifiedTodoCategoryModeration, []int{notice.ID}, false)
	require.NoError(t, err)
	require.Zero(t, marked)
	marked, err = markUnifiedGenericTodosRead(db, owner.Id, owner.Role, UnifiedTodoCategoryModeration, []int{notice.ID}, false)
	require.NoError(t, err)
	require.Equal(t, 1, marked)
	count, err = unifiedModerationNoticeCount(db, owner.Id, true)
	require.NoError(t, err)
	require.Zero(t, count)
	rows, err = unifiedModerationNoticeCandidates(db, owner.Id, []int{notice.ID})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	encoded, err := json.Marshal(rows[0])
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "input_digest")
	require.NotContains(t, string(encoded), "payload")
}

func TestModerationNoticeOutputAndInputRemainSeparate(t *testing.T) {
	db, user := setupModerationEffectsTestDB(t)
	for _, source := range []string{ModerationSourceAssistantInput, ModerationSourceAssistantOutput} {
		job := claimModerationTestJob(t, moderationTestJob(user.Id, "one-turn", source, "strict"), "worker")
		require.NoError(t, CompleteModerationJob(t.Context(), job.ID, "worker", flaggedModerationCompletion()))
		require.NoError(t, CompleteModerationJob(t.Context(), job.ID, "worker", flaggedModerationCompletion()))
	}
	var notices []ModerationNotice
	require.NoError(t, db.Find(&notices).Error)
	require.Len(t, notices, 2)
	for _, notice := range notices {
		if notice.Source == ModerationSourceAssistantOutput {
			require.Zero(t, notice.ChargedQuota)
			require.Zero(t, notice.FeeRecordID)
			items, err := unifiedModerationNoticeCandidates(db, user.Id, []int{notice.ID})
			require.NoError(t, err)
			require.Len(t, items, 1)
			require.Contains(t, items[0].Item.Summary, "assistant's output")
			require.Contains(t, items[0].Item.Summary, "does not penalize")
		}
	}
}
