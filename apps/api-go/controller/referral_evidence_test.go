package controller

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestReferralModerationEvidenceValidationAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence string
		valid    bool
	}{
		{"chinese-byte-boundary", strings.Repeat("证", 334), true},
		{"chinese-limit", strings.Repeat("证", 1000), true},
		{"emoji-limit", strings.Repeat("🙂", 1000), true},
		{"over-limit", strings.Repeat("证", 1001), false},
		{"nul", "review\x00evidence", false},
		{"whitespace", "\u0085\u3000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupManageUserTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.ReferralReward{}, &model.ReferralLedgerEntry{}, &model.ReferralModerationEvent{}))
			actor := model.User{Id: 9999, Username: "unicode-operator", AffCode: "unicode-operator", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
			inviter := model.User{Username: "unicode-parent", AffCode: "unicode-parent", AffQuota: 1000, Quota: 777, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			invitee := model.User{Username: "unicode-child", AffCode: "unicode-child", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&actor).Error)
			require.NoError(t, db.Create(&inviter).Error)
			invitee.InviterId = inviter.Id
			require.NoError(t, db.Create(&invitee).Error)
			require.NoError(t, db.Create(&model.ReferralReward{InviteeId: invitee.Id, InviterId: inviter.Id, TopUpId: 1, Quota: 1000, Status: "earned", PenaltyPercent: 20}).Error)
			payload := map[string]any{"id": invitee.Id, "action": "ban_abuse", "reason": "abuse", "evidence": tc.evidence, "request_id": "unicode-request", "penalize_inviter": true}
			invoke := func() bool {
				body, err := json.Marshal(payload)
				require.NoError(t, err)
				response := performManageUserRequest(t, string(body))
				var result struct {
					Success bool `json:"success"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
				return result.Success
			}
			require.Equal(t, tc.valid, invoke())
			if !tc.valid {
				var events, entries int64
				require.NoError(t, db.Model(&model.ReferralModerationEvent{}).Count(&events).Error)
				require.NoError(t, db.Model(&model.ReferralLedgerEntry{}).Count(&entries).Error)
				require.Zero(t, events)
				require.Zero(t, entries)
				require.NoError(t, db.First(&inviter, inviter.Id).Error)
				require.Equal(t, 1000, inviter.AffQuota)
				require.NoError(t, db.First(&invitee, invitee.Id).Error)
				require.Equal(t, common.UserStatusEnabled, invitee.Status)
				// Validation must not consume the request ID before a valid retry.
				payload["evidence"] = "核实后的有效证据"
				require.True(t, invoke())
			}
			require.True(t, invoke())
			var stored model.ReferralModerationEvent
			require.NoError(t, db.Where("request_id = ?", "unicode-request").First(&stored).Error)
			require.Equal(t, payload["evidence"], stored.Evidence)
			var events, entries int64
			require.NoError(t, db.Model(&model.ReferralModerationEvent{}).Count(&events).Error)
			require.NoError(t, db.Model(&model.ReferralLedgerEntry{}).Count(&entries).Error)
			require.EqualValues(t, 1, events)
			require.EqualValues(t, 2, entries)
			require.NoError(t, db.First(&inviter, inviter.Id).Error)
			require.Equal(t, -200, inviter.AffQuota)
			require.Equal(t, 777, inviter.Quota)
		})
	}
}
