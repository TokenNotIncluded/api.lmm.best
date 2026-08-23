package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAssistantLeadTestDB(t *testing.T) *User {
	t.Helper()
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&AssistantLead{}, &AssistantProfileBucket{}, &AssistantFirstQuestionStat{}))
	user := &User{
		Username: "assistant-lead-user",
		Password: "password",
		Email:    "assistant@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(user).Error)
	return user
}

func TestClassifyAssistantIntent(t *testing.T) {
	tests := map[string]string{
		"请转人工客服":                                  AssistantIntentHumanSupport,
		"How do I create an API key?":             AssistantIntentAPIKey,
		"Base URL 和模型 ID 是什么":                     AssistantIntentAPIKey,
		"Windows 安装 Claude Code 和 CC Switch":      AssistantIntentClientSetup,
		"macOS 怎样配置 cc-switch":                    AssistantIntentClientSetup,
		"怎样把 Base URL 配置进 cc-switch":              AssistantIntentClientSetup,
		"开源挑战完成后怎么赠小费":                            AssistantIntentBounty,
		"帮我计算 token 成本":                           AssistantIntentCost,
		"GPT 5.6 SOL 的 pricing 是多少":               AssistantIntentCost,
		"我是 L0，GPT 5.6 SOL 的价格是多少":                AssistantIntentCost,
		"帮我算一下 17.5% 的折扣":                         AssistantIntentMath,
		"请把当前推荐信润色得专业一些":                          AssistantIntentRecommendation,
		"哪个套餐最划算，有优惠吗":                            AssistantIntentPlanPurchase,
		"怎么领取新手奖励礼包":                              AssistantIntentInvitation,
		"我想申请新人福利":                                AssistantIntentInvitation,
		"我想申请新用户福利":                               AssistantIntentInvitation,
		"How do I claim the new user bonus?":      AssistantIntentInvitation,
		"How do I earn the welcome gift?":         AssistantIntentInvitation,
		"How do I claim the new user gift?":       AssistantIntentInvitation,
		"L0 审核多久能到 L1":                            AssistantIntentOnboarding,
		"请管理员帮我审核 L1":                             AssistantIntentOnboarding,
		"我刚注册还是 L0，请一步一步说明如何申请 L1":                AssistantIntentOnboarding,
		"我要给团队创建 API key 并设置分组":                   AssistantIntentAPIKey,
		"登录后遇到 502，如何联系管理员":                       AssistantIntentHumanSupport,
		"如何发布开源挑战并提交真实 PR":                        AssistantIntentBounty,
		"我想查看高频 API 的用量统计":                        AssistantIntentUsage,
		"我这个月用了多少 token？":                         AssistantIntentUsage,
		"How many tokens have I used this month?": AssistantIntentUsage,
		"hello":           AssistantIntentOther,
		"GPT 5.6 SOL 多少钱": AssistantIntentCost,
		"我的额度还剩多少":        AssistantIntentUsage,
		"怎么充值":            AssistantIntentPlanPurchase,
		"这个接口报错了":         AssistantIntentHumanSupport,
		"有哪些模型能用":         AssistantIntentModels,
		"怎么用这个平台":         AssistantIntentOnboarding,
	}
	for message, expected := range tests {
		assert.Equal(t, expected, ClassifyAssistantIntent(message), message)
	}
}

func TestShouldRecordAssistantIntentSkipsLowSignalMessagesAndFollowUpOther(t *testing.T) {
	assert.False(t, ShouldRecordAssistantIntent("hello", true))
	assert.False(t, ShouldRecordAssistantIntent("谢谢！", false))
	assert.False(t, ShouldRecordAssistantIntent("好的。", false))
	assert.True(t, ShouldRecordAssistantIntent("a substantive uncategorized first question", true))
	assert.False(t, ShouldRecordAssistantIntent("a substantive uncategorized follow-up", false))
	assert.True(t, ShouldRecordAssistantIntent("How do I create an API key?", false))
	assert.False(t, ShouldRecordAssistantIntent("   ", true))
}

func TestRecordAssistantIntentDoesNotPersistChatMessage(t *testing.T) {
	user := setupAssistantLeadTestDB(t)
	require.NoError(t, RecordAssistantIntent(user.Id, "我的 API key 是 sk-super-secret-123"))

	var lead AssistantLead
	require.NoError(t, DB.First(&lead).Error)
	assert.Equal(t, AssistantLeadSourceChat, lead.Source)
	assert.Equal(t, AssistantIntentAPIKey, lead.Intent)
	assert.Empty(t, lead.Message)
}

func TestAssistantAggregateSummariesAreRowBounded(t *testing.T) {
	user := setupAssistantLeadTestDB(t)
	for index := range assistantSummaryMaxRows + 5 {
		require.NoError(t, DB.Create(&AssistantLead{
			UserId: user.Id, Source: AssistantLeadSourceChat,
			Intent: fmt.Sprintf("legacy-%03d", index), Status: AssistantLeadStatusObserved,
			CreatedAt: 100,
		}).Error)
	}

	rows, err := listAssistantIntents(context.Background(), 1, 200)
	require.NoError(t, err)
	assert.Len(t, rows, assistantSummaryMaxRows)
}

func TestPurgeAssistantIntentLeadsBeforeIsBoundedAndPreservesHandoffs(t *testing.T) {
	user := setupAssistantLeadTestDB(t)
	require.NoError(t, DB.Create(&[]AssistantLead{
		{UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentAPIKey, Status: AssistantLeadStatusObserved, CreatedAt: 1},
		{UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentCost, Status: AssistantLeadStatusObserved, CreatedAt: 2},
		{UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentUsage, Status: AssistantLeadStatusObserved, CreatedAt: 3},
		{UserId: user.Id, Source: AssistantLeadSourceHandoff, Intent: AssistantIntentHumanSupport, Status: AssistantLeadStatusResolved, CreatedAt: 1, ResolvedAt: 2, Message: "keep operator history"},
		{UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentBounty, Status: AssistantLeadStatusObserved, CreatedAt: 11},
	}).Error)

	deleted, err := PurgeAssistantIntentLeadsBefore(context.Background(), 10, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 2, deleted)
	deleted, err = PurgeAssistantIntentLeadsBefore(context.Background(), 10, 2)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	deleted, err = PurgeAssistantIntentLeadsBefore(context.Background(), 10, 2)
	require.NoError(t, err)
	assert.Zero(t, deleted)

	var leads []AssistantLead
	require.NoError(t, DB.Order("id ASC").Find(&leads).Error)
	require.Len(t, leads, 2)
	assert.Equal(t, AssistantLeadSourceHandoff, leads[0].Source)
	assert.Equal(t, AssistantLeadSourceChat, leads[1].Source)
	assert.Equal(t, "keep operator history", leads[0].Message)
}

func TestAssistantHandoffRedactsSecretsAndIsIdempotent(t *testing.T) {
	user := setupAssistantLeadTestDB(t)
	lead, err := SubmitAssistantHandoff(user.Id, "登录失败，password: hunter2，key=sk-secret-token-123，电话 13800138000，IP 192.0.2.10")
	require.NoError(t, err)
	assert.NotContains(t, lead.Message, "hunter2")
	assert.NotContains(t, lead.Message, "sk-secret-token-123")
	assert.NotContains(t, lead.Message, "13800138000")
	assert.NotContains(t, lead.Message, "192.0.2.10")
	assert.Contains(t, lead.Message, "[REDACTED]")

	repeated, err := SubmitAssistantHandoff(user.Id, "another message")
	require.NoError(t, err)
	assert.Equal(t, lead.Id, repeated.Id)

	pending, err := ListAssistantHandoffs(AssistantLeadStatusPending, 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, user.Username, pending[0].Username)

	resolved, err := ResolveAssistantHandoff(99, lead.Id, "emailed user")
	require.NoError(t, err)
	assert.Equal(t, AssistantLeadStatusResolved, resolved.Status)
	assert.Equal(t, 99, resolved.AdminUserId)
	assert.Positive(t, resolved.ResolvedAt)

	_, err = ResolveAssistantHandoff(99, lead.Id, "again")
	assert.ErrorIs(t, err, ErrAssistantLeadAlreadyResolved)
}

func TestAssistantHandoffValidationAndIntentSummary(t *testing.T) {
	user := setupAssistantLeadTestDB(t)
	_, err := SubmitAssistantHandoff(user.Id, "")
	assert.ErrorIs(t, err, ErrAssistantHandoffMessageRequired)
	_, err = SubmitAssistantHandoff(user.Id, "四个字")
	assert.ErrorIs(t, err, ErrAssistantHandoffMessageTooShort)
	_, err = SubmitAssistantHandoff(user.Id, strings.Repeat("问", maxAssistantHandoffRunes+1))
	assert.ErrorIs(t, err, ErrAssistantHandoffMessageTooLong)

	require.NoError(t, RecordAssistantIntent(user.Id, "哪个套餐最划算"))
	require.NoError(t, RecordAssistantIntent(user.Id, "有没有优惠套餐"))
	require.NoError(t, RecordAssistantIntent(user.Id, "如何创建 API key"))
	summary, err := ListAssistantIntentSummary(0)
	require.NoError(t, err)
	require.Len(t, summary, 2)
	assert.Equal(t, AssistantIntentPlanPurchase, summary[0].Intent)
	assert.EqualValues(t, 2, summary[0].Count)
}

func TestAssistantProfileSummaryIsAggregateOnly(t *testing.T) {
	_ = setupAssistantLeadTestDB(t)
	require.NoError(t, RecordAssistantProfile("guided_buyer"))
	require.NoError(t, RecordAssistantProfile("guided_buyer"))
	require.NoError(t, RecordAssistantProfile("normal_user"))
	require.NoError(t, RecordAssistantProfile("support_seeking"))
	require.NoError(t, RecordAssistantProfile("l0_applicant"))

	summary, err := ListAssistantProfileSummary(0)
	require.NoError(t, err)
	require.Len(t, summary, 4)
	summaryCounts := map[string]int64{}
	for _, item := range summary {
		summaryCounts[item.Profile] = item.Count
	}
	assert.EqualValues(t, 2, summaryCounts["guided_buyer"])
	assert.EqualValues(t, 1, summaryCounts["normal_user"])
	assert.EqualValues(t, 1, summaryCounts["support_seeking"])
	assert.EqualValues(t, 1, summaryCounts["l0_applicant"])

	var buckets []AssistantProfileBucket
	require.NoError(t, DB.Find(&buckets).Error)
	require.Len(t, buckets, 4)
	counts := map[string]int64{}
	for _, bucket := range buckets {
		counts[bucket.Profile] = bucket.Count
		assert.NotContains(t, bucket.Profile, "@")
	}
	assert.EqualValues(t, 2, counts["guided_buyer"])
	assert.EqualValues(t, 1, counts["normal_user"])
	assert.EqualValues(t, 1, counts["support_seeking"])
	assert.EqualValues(t, 1, counts["l0_applicant"])
	assert.Error(t, RecordAssistantProfile("user@example.com"))
}

func TestAssistantFirstQuestionAggregationNormalizesAndRedacts(t *testing.T) {
	_ = setupAssistantLeadTestDB(t)
	first := "  How   do I use the API? email: alice@example.com user_id=123 token=short-secret-123 api_key=sk_live_supersecret123  " // gitleaks:allow
	second := "how do I use the api? email: bob@example.com user_id=987 token=another-secret-456 api_key=sk_live_othersecret456"      // gitleaks:allow
	require.NoError(t, RecordAssistantFirstQuestion(first))
	require.NoError(t, RecordAssistantFirstQuestion(second))

	var rows []AssistantFirstQuestionStat
	require.NoError(t, DB.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 2, rows[0].Count)
	assert.NotContains(t, rows[0].Question, "alice@example.com")
	assert.NotContains(t, rows[0].Question, "bob@example.com")
	assert.NotContains(t, rows[0].Question, "sk_live_supersecret123") // gitleaks:allow
	assert.NotContains(t, rows[0].Question, "sk_live_othersecret456") // gitleaks:allow
	assert.NotContains(t, rows[0].Question, "short-secret-123")
	assert.NotContains(t, rows[0].Question, "another-secret-456")
	assert.NotContains(t, rows[0].Question, "user_id")

	serialized, err := json.Marshal(rows[0])
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "email")
	assert.NotContains(t, string(serialized), "user_id")

	summary, err := ListAssistantFirstQuestionSummary(0)
	require.NoError(t, err)
	require.Len(t, summary, 1)
	assert.EqualValues(t, 2, summary[0].Count)
	assert.Positive(t, summary[0].LastAskedAt)
	assert.Equal(t, rows[0].Question, summary[0].Question)
}

func TestAssistantFirstQuestionSummaryReturnsTopTen(t *testing.T) {
	_ = setupAssistantLeadTestDB(t)
	for index := 0; index < 12; index++ {
		question := fmt.Sprintf("question %d", index)
		for count := 0; count <= index; count++ {
			require.NoError(t, RecordAssistantFirstQuestion(question))
		}
	}

	summary, err := ListAssistantFirstQuestionSummary(0)
	require.NoError(t, err)
	require.Len(t, summary, 10)
	assert.Equal(t, "question 11", summary[0].Question)
	assert.EqualValues(t, 12, summary[0].Count)
	assert.Equal(t, "question 2", summary[9].Question)
	assert.EqualValues(t, 3, summary[9].Count)
	for _, item := range summary {
		assert.Positive(t, item.LastAskedAt)
	}
}
