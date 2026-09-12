package model

import (
	"context"
	"maps"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var l1AutoReviewTestModels = []any{&User{}, &Token{}, &Option{}, &Channel{}, &Ability{}, &DeveloperAccessRequest{}, &DeveloperAccessRecommendationArchive{}, &TopUp{}}

func seedL1AutoReviewTest(t *testing.T) (User, User, DeveloperAccessRequest, setting.AssistantL1AutoReviewSettings) {
	t.Helper()
	previousSettings := setting.GetAssistantL1AutoReviewSettings()
	previousGroups := ratio_setting.GroupRatio2JSONString()
	previousRedis := common.RedisEnabled
	common.RedisEnabled = false
	common.OptionMapRWMutex.Lock()
	previousOptions := maps.Clone(common.OptionMap)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		require.NoError(t, setting.UpdateAssistantL1AutoReviewOptions(previousSettings.OptionValues()))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(previousGroups))
		common.RedisEnabled = previousRedis
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"review":1,"default":1}`))
	root := User{Username: "auto-review-root", AffCode: "auto-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	user := User{Username: "auto-review-applicant", AffCode: "auto-user", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, DB.Create(&root).Error)
	require.NoError(t, DB.Create(&user).Error)
	channel := Channel{Name: "review-route", Status: common.ChannelStatusEnabled, Group: "review", Models: "review-model"}
	require.NoError(t, DB.Create(&channel).Error)
	require.NoError(t, DB.Create(&Ability{Group: "review", Model: "review-model", ChannelId: channel.Id, Enabled: true}).Error)
	config := setting.DefaultAssistantL1AutoReviewSettings()
	config.Enabled, config.Model, config.Group, config.Prompt = true, "review-model", "review", "Review concrete development use cases and explain your decision."
	require.NoError(t, UpdateOptionsBulk(config.OptionValues()))
	request, err := SubmitAssistantDeveloperAccessRecommendation(user.Id, "Integrate the API into a robotics project", "The application describes a concrete robotics integration for developer APIs.")
	require.NoError(t, err)
	return root, user, *request, config
}

func TestApplyDeveloperAccessAutoReviewApproveAndReplay(t *testing.T) {
	useSingleConnectionTestDB(t, l1AutoReviewTestModels...)
	root, user, request, config := seedL1AutoReviewTest(t)
	var approved *DeveloperAccessRequest
	err := withSingleConnectionDeadline(t, func() error {
		var err error
		approved, err = ApplyDeveloperAccessAutoReview(context.Background(), root.Id, request, config, true, "Your project can now use developer access.")
		return err
	})
	require.NoError(t, err)
	require.Equal(t, DeveloperAccessRequestApproved, approved.Status)
	require.Equal(t, request.Revision+1, approved.Revision)
	require.NotEmpty(t, approved.AdminNote)
	require.Equal(t, root.Id, approved.AdminUserId)
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Positive(t, user.ConsoleActivatedAt)
	require.Nil(t, user.TrustLevelOverride)
	var archives int64
	require.NoError(t, DB.Model(&DeveloperAccessRecommendationArchive{}).Where("user_id = ?", user.Id).Count(&archives).Error)
	require.EqualValues(t, 1, archives)
	_, err = ApplyDeveloperAccessAutoReview(context.Background(), root.Id, request, config, true, "Duplicate response")
	require.ErrorIs(t, err, ErrDeveloperAccessRequestReviewed)
}

func TestApplyDeveloperAccessAutoReviewHumanReplyDoesNotGrantOrReject(t *testing.T) {
	useSingleConnectionTestDB(t, l1AutoReviewTestModels...)
	root, user, request, config := seedL1AutoReviewTest(t)
	reviewed, err := ApplyDeveloperAccessAutoReview(context.Background(), root.Id, request, config, false, "Your request is awaiting manual review. password: example-only-password")
	require.NoError(t, err)
	require.Equal(t, DeveloperAccessRequestPending, reviewed.Status)
	require.Zero(t, reviewed.ReviewedAt)
	require.Zero(t, reviewed.AdminUserId)
	require.NotContains(t, reviewed.AdminNote, "example-only-password")
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Zero(t, user.ConsoleActivatedAt)
	_, err = ApplyDeveloperAccessAutoReview(context.Background(), root.Id, request, config, true, "A stale approval")
	require.ErrorIs(t, err, ErrDeveloperAccessRequestChanged)
	// A human may still settle the pending request after the agent's reply.
	_, err = ReviewDeveloperAccessRequest(root.Id, request.Id, false, "Please provide more project details.")
	require.NoError(t, err)
}

func TestApplyDeveloperAccessAutoReviewFencesStaleAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, User, User, *DeveloperAccessRequest, setting.AssistantL1AutoReviewSettings)
	}{
		{"edited letter", func(t *testing.T, _, user User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			_, err := SubmitAssistantDeveloperAccessRequestWithoutRecommendation(user.Id, "A different API project requires a new review")
			require.NoError(t, err)
		}},
		{"manual decision", func(t *testing.T, root, _ User, request *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			_, err := ReviewDeveloperAccessRequest(root.Id, request.Id, false, "Manual decision takes priority")
			require.NoError(t, err)
		}},
		{"disabled user", func(t *testing.T, _, user User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			require.NoError(t, DB.Model(&user).Update("status", common.UserStatusDisabled).Error)
		}},
		{"admin role", func(t *testing.T, _, user User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			require.NoError(t, DB.Model(&user).Update("role", common.RoleAdminUser).Error)
		}},
		{"explicit L0 restriction", func(t *testing.T, _, user User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			require.NoError(t, DB.Model(&user).Update("trust_level_override", 0).Error)
		}},
		{"root disabled", func(t *testing.T, root, _ User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			require.NoError(t, DB.Model(&root).Update("status", common.UserStatusDisabled).Error)
		}},
		{"root demoted", func(t *testing.T, root, _ User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			require.NoError(t, DB.Model(&root).Update("role", common.RoleCommonUser).Error)
		}},
		{"route removed", func(t *testing.T, _, _ User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			require.NoError(t, DB.Model(&Channel{}).Where("name = ?", "review-route").Update("status", common.ChannelStatusManuallyDisabled).Error)
		}},
		{"local configuration changed", func(t *testing.T, _, _ User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			require.NoError(t, UpdateOption(setting.AssistantL1AutoReviewPromptOptionKey, "Changed administrator review policy"))
		}},
		{"remote configuration changed", func(t *testing.T, _, _ User, _ *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			require.NoError(t, DB.Model(&Option{}).Where("key = ?", setting.AssistantL1AutoReviewMinConfidenceOptionKey).Update("value", "1").Error)
		}},
		{"legacy source", func(_ *testing.T, _, _ User, request *DeveloperAccessRequest, _ setting.AssistantL1AutoReviewSettings) {
			request.Source = DeveloperAccessRequestSourceOld
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useSingleConnectionTestDB(t, l1AutoReviewTestModels...)
			root, user, request, config := seedL1AutoReviewTest(t)
			tc.mutate(t, root, user, &request, config)
			_, err := ApplyDeveloperAccessAutoReview(context.Background(), root.Id, request, config, true, "The application is approved.")
			require.Error(t, err)
			require.NoError(t, DB.First(&user, user.Id).Error)
			require.Zero(t, user.ConsoleActivatedAt)
			var persisted DeveloperAccessRequest
			require.NoError(t, DB.First(&persisted, request.Id).Error)
			require.NotEqual(t, DeveloperAccessRequestApproved, persisted.Status)
		})
	}
}

func TestApplyDeveloperAccessAutoReviewInvalidReplyAndCancellation(t *testing.T) {
	useSingleConnectionTestDB(t, l1AutoReviewTestModels...)
	root, user, request, config := seedL1AutoReviewTest(t)
	for _, note := range []string{"", "  ", "a", "\u200b\u200b", "a\u200b", "nul\x00reply", "invalid\xfftext"} {
		_, err := ApplyDeveloperAccessAutoReview(context.Background(), root.Id, request, config, true, note)
		require.Error(t, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ApplyDeveloperAccessAutoReview(ctx, root.Id, request, config, true, "Valid reply but canceled")
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, DB.First(&user, user.Id).Error)
	require.Zero(t, user.ConsoleActivatedAt)
}

func TestL1AutoReviewSettingsValidationAndAtomicStorage(t *testing.T) {
	useSingleConnectionTestDB(t, l1AutoReviewTestModels...)
	_, _, _, original := seedL1AutoReviewTest(t)
	for _, values := range []map[string]string{
		{setting.AssistantL1AutoReviewMinConfidenceOptionKey: "NaN"},
		{setting.AssistantL1AutoReviewGroupOptionKey: "missing"},
		{setting.AssistantL1AutoReviewModelOptionKey: "missing"},
		{setting.AssistantL1AutoReviewPromptOptionKey: ""},
	} {
		require.Error(t, UpdateOptionsBulk(values))
		require.Equal(t, original, setting.GetAssistantL1AutoReviewSettings())
		stored, err := assistantL1AutoReviewOptionValues(DB)
		require.NoError(t, err)
		require.Equal(t, original.OptionValues(), stored)
	}

	// Simulate a second node's commit. A partial update must incorporate the
	// stored policy rather than silently restore this node's stale prompt.
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", setting.AssistantL1AutoReviewPromptOptionKey).Update("value", "Policy updated on another node").Error)
	require.NoError(t, UpdateOption(setting.AssistantL1AutoReviewMinConfidenceOptionKey, "0.99"))
	require.Equal(t, "Policy updated on another node", setting.GetAssistantL1AutoReviewSettings().Prompt)

	// Disabling must remain possible when a route has just disappeared.
	require.NoError(t, DB.Model(&Channel{}).Where("1 = 1").Update("status", common.ChannelStatusManuallyDisabled).Error)
	require.NoError(t, UpdateOption(setting.AssistantL1AutoReviewEnabledOptionKey, "false"))
	require.False(t, setting.GetAssistantL1AutoReviewSettings().Enabled)
	require.Error(t, UpdateOption(setting.AssistantL1AutoReviewEnabledOptionKey, "true"))
}

func TestL1AutoReviewRejectsAuthoritativelyIncompleteSettings(t *testing.T) {
	useSingleConnectionTestDB(t, l1AutoReviewTestModels...)
	_, _, _, original := seedL1AutoReviewTest(t)
	// Cache is valid, but the durable prompt was cleared by another node.
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", setting.AssistantL1AutoReviewPromptOptionKey).Update("value", "").Error)
	require.Error(t, UpdateOption(setting.AssistantL1AutoReviewMinConfidenceOptionKey, "0.99"))
	require.Equal(t, original, setting.GetAssistantL1AutoReviewSettings())
	var threshold Option
	require.NoError(t, DB.Where("key = ?", setting.AssistantL1AutoReviewMinConfidenceOptionKey).First(&threshold).Error)
	require.Equal(t, "0.98", threshold.Value)
}

func TestL1AutoReviewSettingsTransactionRollback(t *testing.T) {
	useSingleConnectionTestDB(t, l1AutoReviewTestModels...)
	_, _, _, original := seedL1AutoReviewTest(t)
	err := DB.Transaction(func(tx *gorm.DB) error {
		values := map[string]string{setting.AssistantL1AutoReviewPromptOptionKey: "Uncommitted policy"}
		if err := lockAssistantL1AutoReviewOptions(tx, values); err != nil {
			return err
		}
		return gorm.ErrInvalidTransaction
	})
	require.ErrorIs(t, err, gorm.ErrInvalidTransaction)
	require.Equal(t, original, setting.GetAssistantL1AutoReviewSettings())
}
