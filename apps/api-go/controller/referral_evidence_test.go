package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestReferralModerationInvalidEvidenceDoesNotConsumeRequest(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ReferralReward{}, &model.ReferralLedgerEntry{}, &model.ReferralModerationEvent{}))
	actor := model.User{Id: 9999, Username: "evidence-operator", AffCode: "evidence-operator", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	user := model.User{Username: "evidence-target", AffCode: "evidence-target", Quota: 12345, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&actor).Error)
	require.NoError(t, db.Create(&user).Error)
	send := func(evidence string) bool {
		body, err := json.Marshal(map[string]interface{}{
			"id": user.Id, "action": "ban_abuse", "reason": "abuse",
			"evidence": evidence, "request_id": "correctable-evidence", "penalize_inviter": false,
		})
		require.NoError(t, err)
		response := performManageUserRequest(t, string(body))
		var result struct {
			Success bool `json:"success"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		return result.Success
	}
	for _, evidence := range []string{strings.Repeat("证", 334), strings.Repeat("😀", 251), "\u0085\u3000", "proof\x00suffix"} {
		require.False(t, send(evidence))
		var count int64
		require.NoError(t, db.Model(&model.ReferralModerationEvent{}).Count(&count).Error)
		require.Zero(t, count, "invalid input must not reserve an idempotency key")
		require.NoError(t, db.First(&user, user.Id).Error)
		require.Equal(t, common.UserStatusEnabled, user.Status)
		require.Equal(t, 12345, user.Quota)
	}
	exact := strings.Repeat("证", 333) + "a"
	require.True(t, send("\u0085 "+exact+"\u3000"))
	require.True(t, send(exact), "normalized exact retry must be the same operation")
	var events []model.ReferralModerationEvent
	require.NoError(t, db.Find(&events).Error)
	require.Len(t, events, 1)
	require.Equal(t, exact, events[0].Evidence)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, common.UserStatusDisabled, user.Status)
	require.Equal(t, 12345, user.Quota)
}
