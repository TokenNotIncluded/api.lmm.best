package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRegistrationGuard(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupAssistantGiftTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}, &DeveloperAccessRequest{}, &DeveloperAccessRecommendationArchive{}, &AssistantConversation{}, &AssistantHistoryMessage{}, &AssistantSupportRequest{}))
	return db
}
func guardUser(t *testing.T, name string) User {
	t.Helper()
	user := User{Username: name, AffCode: name, Email: name + "@example.test", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, ObserveAssistantRegistration(user.Id, "198.51.100.10", ""))
	return user
}
func guardCampaign(t *testing.T, user User) {
	t.Helper()
	for _, message := range []string{
		"I would like to receive the new user reward for connecting my development application and testing a chatbot with detailed API requests.",
		"My second message describes a workflow for debugging TypeScript tools and integrating the welcome quota into an automated model evaluation suite.",
	} {
		require.NoError(t, ObserveAssistantRegistration(user.Id, "198.51.100.10", message))
	}
}
func guardUsedIdentity(t *testing.T, user User) {
	t.Helper()
	var profile AssistantRegistrationProfile
	require.NoError(t, DB.First(&profile, "user_id = ?", user.Id).Error)
	require.NoError(t, DB.Create(&AssistantGiftRiskMemory{KeyHash: profile.IdentityHash, Kind: assistantGiftRiskIdentity, DecisionCount: 1, WindowStartedAt: common.GetTimestamp(), UpdatedAt: common.GetTimestamp()}).Error)
}
func guardConversation(t *testing.T, user User) *AssistantConversation {
	t.Helper()
	conversation, err := PrepareAssistantConversation(user.Id, 0, "my real request")
	require.NoError(t, err)
	return conversation
}
func TestRegistrationGuardWeakSignalsNeverBan(t *testing.T) {
	setupRegistrationGuard(t)
	user := guardUser(t, "x7q2z9")
	require.NoError(t, DB.Model(&user).Update("email", "4829312@qq.com").Error)
	user.Email = "4829312@qq.com"
	for _, message := range []string{"你好", "我换个问题", "名字随机的", "算了懒得打字"} {
		require.NoError(t, ObserveAssistantRegistration(user.Id, "198.51.100.10", message))
	}
	summary, err := GetAssistantRegistrationSummary(user.Id)
	require.NoError(t, err)
	require.False(t, summary.Decision.Alert)
	require.False(t, summary.Decision.Hold)
	conv := guardConversation(t, user)
	_, err = ApplyAssistantRegistrationAction(user.Id, conv.Id, "suspend")
	require.ErrorIs(t, err, ErrAssistantRegistrationCheck)
	var count int64
	require.NoError(t, DB.Model(&AssistantRegistrationFingerprint{}).Count(&count).Error)
	require.Zero(t, count)
}
func TestRegistrationGuardCorrelationBoundariesAndReversal(t *testing.T) {
	setupRegistrationGuard(t)
	for i := 0; i < 4; i++ {
		guardCampaign(t, guardUser(t, fmt.Sprintf("peer-%d", i)))
	}
	user := guardUser(t, "subject")
	guardCampaign(t, user)
	conv := guardConversation(t, user)
	summary, err := GetAssistantRegistrationSummary(user.Id)
	require.NoError(t, err)
	require.Equal(t, 4, summary.Evidence.TemplatePeers)
	require.Equal(t, 4, summary.Evidence.NetworkPeers)
	require.True(t, summary.Decision.Hold)
	require.False(t, summary.Decision.CanSuspend)
	_, err = ApplyAssistantRegistrationAction(user.Id, conv.Id, "suspend")
	require.ErrorIs(t, err, ErrAssistantRegistrationCheck)
	guardUsedIdentity(t, user)
	// A model cannot choose someone else's conversation or sanction an administrator.
	other := guardUser(t, "other")
	otherConv := guardConversation(t, other)
	_, err = ApplyAssistantRegistrationAction(user.Id, otherConv.Id, "suspend")
	require.Error(t, err)
	require.NoError(t, DB.Model(&user).Update("role", common.RoleAdminUser).Error)
	_, err = ApplyAssistantRegistrationAction(user.Id, conv.Id, "suspend")
	require.Error(t, err)
	require.NoError(t, DB.Model(&user).Update("role", common.RoleCommonUser).Error)
	receipt, err := ApplyAssistantRegistrationAction(user.Id, conv.Id, "suspend")
	require.NoError(t, err)
	require.Equal(t, "suspend", receipt.Action)
	require.False(t, strings.Contains(receipt.Evidence, user.Email))
	repeated, err := ApplyAssistantRegistrationAction(user.Id, conv.Id, "suspend")
	require.NoError(t, err)
	require.Equal(t, receipt.ID, repeated.ID)
	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	require.Equal(t, common.UserStatusDisabled, stored.Status)
	require.Greater(t, stored.AuthVersion, user.AuthVersion)
	var notices int64
	require.NoError(t, DB.Model(&AssistantHistoryMessage{}).Where("conversation_id = ? AND content = ?", conv.Id, AssistantRegistrationTerminationNotice).Count(&notices).Error)
	require.EqualValues(t, 1, notices)
	_, err = ReleaseAssistantRegistrationSuspension(other.Id, user.Id)
	require.Error(t, err)
	require.NoError(t, DB.Model(&other).Update("role", common.RoleAdminUser).Error)
	released, err := ReleaseAssistantRegistrationSuspension(other.Id, user.Id)
	require.NoError(t, err)
	require.Equal(t, "released", released.Action)
	_, err = ApplyAssistantRegistrationAction(user.Id, conv.Id, "suspend")
	require.ErrorIs(t, err, ErrAssistantRegistrationCheck)
}
func TestRegistrationGuardMissingStaleAndChangedIdentityFailClosed(t *testing.T) {
	setupRegistrationGuard(t)
	user := guardUser(t, "fresh")
	require.NoError(t, CheckAssistantRegistration(user.Id))
	require.NoError(t, DB.Model(&AssistantRegistrationProfile{}).Where("user_id = ?", user.Id).Update("observed_at", common.GetTimestamp()-901).Error)
	require.ErrorIs(t, CheckAssistantRegistration(user.Id), ErrAssistantRegistrationCheck)
	require.NoError(t, ObserveAssistantRegistration(user.Id, "198.51.100.10", ""))
	require.NoError(t, DB.Model(&user).Update("email", "changed@example.test").Error)
	require.ErrorIs(t, CheckAssistantRegistration(user.Id), ErrAssistantRegistrationCheck)
	require.NoError(t, DB.Model(&user).Updates(map[string]any{"email": "", "github_id": "bound-subject"}).Error)
	require.NoError(t, ObserveAssistantRegistration(user.Id, "198.51.100.10", ""))
	require.NoError(t, CheckAssistantRegistration(user.Id))
}
func TestRegistrationGuardGlobalCapBecomesNotification(t *testing.T) {
	setupRegistrationGuard(t)
	for i := 0; i < 4; i++ {
		guardCampaign(t, guardUser(t, fmt.Sprintf("seed-%d", i)))
	}
	require.NoError(t, DB.Create(&Option{Key: AssistantRegistrationDailyCapOption, Value: "1"}).Error)
	for i := 0; i < 2; i++ {
		user := guardUser(t, fmt.Sprintf("cap-%d", i))
		guardCampaign(t, user)
		guardUsedIdentity(t, user)
		receipt, err := ApplyAssistantRegistrationAction(user.Id, guardConversation(t, user).Id, "suspend")
		require.NoError(t, err)
		expected := "suspend"
		if i == 1 {
			expected = "notify"
		}
		require.Equal(t, expected, receipt.Action)
	}
	for _, v := range []string{"-1", "6", "nan", "1.5"} {
		require.Error(t, ValidateRegistrationGuardOption(AssistantRegistrationDailyCapOption, v))
	}
	require.Error(t, ValidateRegistrationGuardOption(AssistantRegistrationAutoSuspendOption, "yes"))
}
