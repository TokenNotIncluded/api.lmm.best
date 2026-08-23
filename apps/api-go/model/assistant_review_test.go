package model

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReviewAggregates(t *testing.T) {
	user := setupAssistantLeadTestDB(t)
	require.NoError(t, DB.AutoMigrate(&PromptPresetStat{}, &AssistantSecurityIncident{}, &AdvancedSecurityEvent{}, &TopUp{}, &SubscriptionOrder{}, &FinanceLedgerEntry{}, &Log{}))

	require.NoError(t, DB.Create(&AssistantProfileBucket{Profile: AssistantProfileUnknown, BucketStart: 100, Count: 5}).Error)
	require.NoError(t, DB.Create(&AssistantProfileBucket{Profile: AssistantProfileTechnical, BucketStart: 100, Count: 2}).Error)
	require.NoError(t, DB.Create(&AssistantProfileBucket{Profile: AssistantProfileUnknown, BucketStart: 300, Count: 99}).Error)
	require.NoError(t, DB.Create(&AssistantLead{
		UserId: user.Id, Source: AssistantLeadSourceHandoff, Intent: AssistantIntentHumanSupport,
		Message: "private support text", Status: AssistantLeadStatusPending, CreatedAt: 100,
	}).Error)
	require.NoError(t, DB.Create(&AssistantLead{
		UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentCost,
		Status: AssistantLeadStatusObserved, CreatedAt: 120,
	}).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId: user.Id, TradeNo: "review-topup", PaymentProvider: PaymentProviderStripe,
		Status: common.TopUpStatusSuccess, CreateTime: 110, CompleteTime: 150,
	}).Error)
	require.NoError(t, DB.Create(&SubscriptionOrder{
		UserId: user.Id, TradeNo: "review-subscription", PaymentProvider: PaymentProviderStripe,
		Status: common.TopUpStatusSuccess, CreateTime: 110, CompleteTime: 160,
	}).Error)
	// Subscription settlement keeps a successful TopUp mirror for wallet/order
	// compatibility. The automatic review must count this as a subscription,
	// not as an additional top-up order.
	require.NoError(t, DB.Create(&TopUp{
		UserId: user.Id, TradeNo: "review-subscription", PaymentProvider: PaymentProviderStripe,
		Status: common.TopUpStatusSuccess, CreateTime: 110, CompleteTime: 160,
	}).Error)
	_, err := AppendFinanceLedgerEntry(&FinanceLedgerEntry{
		EntryType: FinanceEntryRevenue, AmountMicros: 2_500_000, Currency: FinanceCurrencyUSD,
		Direction: FinanceDirectionDebit, SourceType: FinanceSourceRefund, SourceId: "review-refund",
		OccurredAt: 170, CreatedBy: user.Id, IdempotencyKey: "review-refund",
	})
	require.NoError(t, err)
	priorPaidUser := &User{
		Username: "assistant-review-prior-paid", Password: "password", Email: "prior-paid@example.com",
		Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "review-prior-aff",
	}
	require.NoError(t, DB.Create(priorPaidUser).Error)
	require.NoError(t, DB.Create(&TopUp{
		UserId: priorPaidUser.Id, TradeNo: "review-prior-topup", PaymentProvider: PaymentProviderCreem,
		Status: common.TopUpStatusSuccess, CreateTime: 130, CompleteTime: 140,
	}).Error)
	require.NoError(t, DB.Create(&AssistantLead{
		UserId: priorPaidUser.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentCost,
		Status: AssistantLeadStatusObserved, CreatedAt: 180,
	}).Error)
	require.NoError(t, DB.Create(&PromptPresetStat{
		PresetId: "pricing_cost", BucketStart: 100, Generation: 1, Version: PromptPresetVersion,
		ClickCount: 10, ConversationCount: 2, RecommendationCount: 4, ApprovalCount: 0, UpdatedAt: 100,
	}).Error)
	require.NoError(t, DB.Create(&PromptPresetStat{
		PresetId: "pricing_cost", BucketStart: 300, Generation: 2, Version: PromptPresetVersion,
		ClickCount: 99, ConversationCount: 99, UpdatedAt: 300,
	}).Error)
	require.NoError(t, DB.Create(&AssistantSecurityIncident{
		UserId: user.Id, ConversationId: 99, Category: AssistantSecurityIncidentCategory,
		Status: AssistantSecurityIncidentStatusOpen, InputDigest: strings.Repeat("a", 64), CreatedAt: 100, UpdatedAt: 100,
	}).Error)
	require.NoError(t, DB.Create(&AdvancedSecurityEvent{
		CreatedAt: 100, RequestID: "request-in-window", UserID: user.Id, Username: user.Username,
		Decision: AdvancedSecurityDecisionBlocked, RuleID: "prompt-injection", Category: "prompt_injection",
		InputDigest: "digest-in-window", PatternDigest: "pattern-in-window",
	}).Error)
	require.NoError(t, DB.Create(&AdvancedSecurityEvent{
		CreatedAt: 300, RequestID: "request-outside-window", UserID: user.Id, Username: user.Username,
		Decision: AdvancedSecurityDecisionAudited, RuleID: "outside", Category: "outside",
	}).Error)
	require.NoError(t, DB.Create(&[]Log{
		{CreatedAt: 100, Type: LogTypeError, ChannelId: 7, ModelName: "gpt-review", Content: "private upstream response"},
		{CreatedAt: 150, Type: LogTypeError, ChannelId: 7, ModelName: "gpt-review", Content: "another private error"},
		{CreatedAt: 300, Type: LogTypeError, ChannelId: 8, ModelName: "outside-model", Content: "outside window"},
	}).Error)

	review, err := BuildAssistantReview(context.Background(), 1, 200)
	require.NoError(t, err)
	assert.EqualValues(t, 1, review.CurrentSupport)
	assert.EqualValues(t, 1, review.CurrentIncidents)
	assert.EqualValues(t, 2, review.Commerce.ChatUsers)
	assert.EqualValues(t, 1, review.Commerce.SuccessfulTopUpOrders)
	assert.EqualValues(t, 1, review.Commerce.SuccessfulSubscriptionOrders)
	assert.EqualValues(t, 1, review.Commerce.PaidUsers)
	assert.Equal(t, float64(50), review.Commerce.ConversionRatePercent)
	assert.EqualValues(t, 1, review.Commerce.RefundCount)
	assert.EqualValues(t, 2_500_000, review.Commerce.RefundAmountMicros)
	assert.EqualValues(t, 1, review.Security.TotalMatches)
	assert.EqualValues(t, 1, review.Security.BlockedMatches)
	assert.EqualValues(t, 0, review.Security.AuditedMatches)
	assert.EqualValues(t, 1, review.Security.AffectedRequests)
	assert.EqualValues(t, 1, review.Security.AffectedUsers)
	assert.Equal(t, []AdvancedSecurityStatBucket{{Key: "prompt_injection", Count: 1}}, review.Security.ByCategory)
	assert.Equal(t, []AdvancedSecurityStatBucket{{Key: "prompt-injection", Count: 1}}, review.Security.ByRule)
	assert.EqualValues(t, 2, review.Security.ErrorLogCount)
	assert.Equal(t, []AdvancedSecurityStatBucket{{Key: "7", Count: 2}}, review.Security.ErrorChannels)
	assert.Equal(t, []AdvancedSecurityStatBucket{{Key: "gpt-review", Count: 2}}, review.Security.ErrorModels)
	require.Len(t, review.Presets, 1)
	assert.EqualValues(t, 10, review.Presets[0].Clicks)
	assert.EqualValues(t, 5, review.Profiles[0].Count)

	codes := make([]string, 0, len(review.Actions))
	for _, action := range review.Actions {
		codes = append(codes, action.Code)
	}
	assert.ElementsMatch(t, []string{
		"review_support_queue",
		"review_security_incidents",
		"review_security_events",
		"review_error_channels",
		"improve_profile_classification",
		"improve_pre_conversation_prompts",
		"review_recommendation_quality",
	}, codes)

	encoded, err := json.Marshal(review)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "private support text")
	assert.NotContains(t, string(encoded), user.Email)
	assert.NotContains(t, string(encoded), "request-in-window")
	assert.NotContains(t, string(encoded), "digest-in-window")
	assert.NotContains(t, string(encoded), "private upstream response")
	assert.Less(t, len(encoded), 16*1024)
}

func TestReviewDistillsFirstQuestionsWithoutHistoricalOtherNoise(t *testing.T) {
	user := setupAssistantLeadTestDB(t)
	require.NoError(t, DB.AutoMigrate(&PromptPresetStat{}, &AssistantSecurityIncident{}, &AdvancedSecurityEvent{}, &TopUp{}, &SubscriptionOrder{}, &FinanceLedgerEntry{}, &Log{}))
	require.NoError(t, DB.Create(&AssistantFirstQuestionStat{
		QuestionHash: "review-first-question",
		Question:     "how do i connect a desktop client?",
		BucketStart:  100,
		Count:        2,
		LastAskedAt:  120,
	}).Error)
	require.NoError(t, DB.Create(&[]AssistantLead{
		{UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentOther, Status: AssistantLeadStatusObserved, CreatedAt: 100},
		{UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentOther, Status: AssistantLeadStatusObserved, CreatedAt: 110},
		{UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentOther, Status: AssistantLeadStatusObserved, CreatedAt: 120},
		{UserId: user.Id, Source: AssistantLeadSourceChat, Intent: AssistantIntentAPIKey, Status: AssistantLeadStatusObserved, CreatedAt: 130},
	}).Error)

	review, err := BuildAssistantReview(context.Background(), 1, 200)
	require.NoError(t, err)
	require.NotEmpty(t, review.FirstQuestions)
	assert.Contains(t, review.FirstQuestions[0].Question, "desktop client")
	require.Equal(t, []AssistantIntentSummary{{Intent: AssistantIntentClientSetup, Count: 2}}, review.DistilledIntents)
	codes := make([]string, 0, len(review.Actions))
	for _, action := range review.Actions {
		codes = append(codes, action.Code)
	}
	assert.NotContains(t, codes, "improve_intent_classification")
}

func TestReviewInvalidWindow(t *testing.T) {
	_, err := BuildAssistantReview(context.Background(), 0, 1)
	require.Error(t, err)
}

func TestReviewCancellation(t *testing.T) {
	setupAssistantLeadTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := BuildAssistantReview(ctx, 1, 2)
	require.ErrorIs(t, err, context.Canceled)
}
