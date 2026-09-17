package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestReferralHistoryUsesAuthenticatedIdentityAndStableCursor(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ReferralLedgerEntry{}))
	user := model.User{Username: "history-owner", AffCode: "history-owner", Quota: 888, AffQuota: -1200}
	other := model.User{Username: "other-owner", AffCode: "other-owner"}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&other).Error)
	for i := 0; i < 53; i++ {
		require.NoError(t, db.Create(&model.ReferralLedgerEntry{UserId: user.Id, RewardId: 1, EventKey: fmt.Sprint("owner-", i), Kind: "clawback", Reason: "abuse", Quota: -1}).Error)
	}
	require.NoError(t, db.Create(&model.ReferralLedgerEntry{UserId: other.Id, RewardId: 2, EventKey: "private", Kind: "reward", Reason: "first_top_up", Quota: 999999}).Error)
	call := func(query string) map[string]interface{} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Set("id", user.Id)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/user/self/aff/rewards"+query, nil)
		GetReferralRewards(c)
		require.NotContains(t, recorder.Body.String(), "event_key")
		require.NotContains(t, recorder.Body.String(), "user_id")
		var response map[string]interface{}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		return response
	}
	response := call(fmt.Sprintf("?user_id=%d", other.Id))
	require.Equal(t, true, response["success"])
	data := response["data"].(map[string]interface{})
	entries := data["entries"].([]interface{})
	require.Len(t, entries, 50)
	require.EqualValues(t, 1200, data["debt_quota"])
	require.EqualValues(t, 0, data["available_quota"])
	for _, entry := range entries {
		require.EqualValues(t, 1, entry.(map[string]interface{})["reward_id"])
	}
	cursor := int(data["next_cursor"].(float64))
	second := call(fmt.Sprintf("?before=%d", cursor))["data"].(map[string]interface{})
	require.Len(t, second["entries"].([]interface{}), 3)
	require.EqualValues(t, 0, second["next_cursor"])
	require.Equal(t, false, call("?before=-1")["success"])
}

func TestReferralOrdinaryDisablePreservesRewardsAndAbuseRequiresLivePrivilege(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ReferralReward{}, &model.ReferralLedgerEntry{}, &model.ReferralModerationEvent{}))
	actor := model.User{Id: 9999, Username: "referral-operator", AffCode: "referral-operator", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	inviter := model.User{Username: "referral-parent", AffCode: "referral-parent", AffQuota: 1000, Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	invitee := model.User{Username: "referral-child", AffCode: "referral-child", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&actor).Error)
	require.NoError(t, db.Create(&inviter).Error)
	invitee.InviterId = inviter.Id
	require.NoError(t, db.Create(&invitee).Error)
	require.NoError(t, db.Create(&model.ReferralReward{InviteeId: invitee.Id, InviterId: inviter.Id, TopUpId: 1, Quota: 1000, Status: "earned", PenaltyPercent: 20}).Error)
	response := performManageUserRequest(t, fmt.Sprintf(`{"id":%d,"action":"disable"}`, invitee.Id))
	require.Contains(t, response.Body.String(), `"success":true`)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Equal(t, 1000, inviter.AffQuota)
	// A stale root role in a session must not authorize this sensitive action.
	require.NoError(t, db.Model(&actor).Update("role", common.RoleCommonUser).Error)
	body := fmt.Sprintf(`{"id":%d,"action":"ban_abuse","reason":"abuse","evidence":"reviewed evidence","request_id":"controller-ban","penalize_inviter":true}`, invitee.Id)
	response = performManageUserRequest(t, body)
	require.Contains(t, response.Body.String(), `"success":false`)
	require.NoError(t, db.Model(&actor).Update("role", common.RoleRootUser).Error)
	response = performManageUserRequest(t, body)
	require.Contains(t, response.Body.String(), `"success":true`)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Equal(t, -200, inviter.AffQuota)
	response = performManageUserRequest(t, body)
	require.Contains(t, response.Body.String(), `"success":true`)
	require.NoError(t, db.First(&inviter, inviter.Id).Error)
	require.Equal(t, -200, inviter.AffQuota)
}
