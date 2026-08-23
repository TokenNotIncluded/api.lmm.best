package model

import (
	"context"
	"errors"
	"math"
	"sort"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

const reviewListLimit = 20

type AssistantPresetReview struct {
	PresetID        string `json:"preset_id"`
	Clicks          int64  `json:"clicks"`
	Conversations   int64  `json:"conversations"`
	Recommendations int64  `json:"recommendations"`
	Approvals       int64  `json:"approvals"`
}

type AssistantReviewAction struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// AssistantCommerceReview contains only bounded aggregates.  It connects
// assistant chat activity to settled orders through distinct user counts in
// SQL, but never returns the join keys or any user-level data to callers.
type AssistantCommerceReview struct {
	ChatUsers                    int64   `json:"chat_users"`
	SuccessfulTopUpOrders        int64   `json:"successful_topup_orders"`
	SuccessfulSubscriptionOrders int64   `json:"successful_subscription_orders"`
	PaidUsers                    int64   `json:"paid_users"`
	ConversionRatePercent        float64 `json:"conversion_rate_percent"`
	RefundCount                  int64   `json:"refund_count"`
	RefundAmountMicros           int64   `json:"refund_amount_micros"`
}

// AssistantSecurityReview contains only windowed counters and bounded
// category/rule aggregates. It deliberately excludes request IDs, user IDs,
// usernames, digests, prompts, and matcher patterns.
type AssistantSecurityReview struct {
	TotalMatches     int64                        `json:"total_matches"`
	BlockedMatches   int64                        `json:"blocked_matches"`
	AuditedMatches   int64                        `json:"audited_matches"`
	AffectedRequests int64                        `json:"affected_requests"`
	AffectedUsers    int64                        `json:"affected_users"`
	ByCategory       []AdvancedSecurityStatBucket `json:"by_category"`
	ByRule           []AdvancedSecurityStatBucket `json:"by_rule"`
	ErrorLogCount    int64                        `json:"error_log_count"`
	ErrorChannels    []AdvancedSecurityStatBucket `json:"error_channels"`
	ErrorModels      []AdvancedSecurityStatBucket `json:"error_models"`
}

// AssistantReview is aggregate-only. It deliberately excludes user IDs,
// transcripts, email addresses, profile strategies, and memory. First
// questions are redacted, normalized aggregates already used for product
// analytics, not raw chat text.
type AssistantReview struct {
	WindowStart      int64                           `json:"window_start"`
	WindowEnd        int64                           `json:"window_end"`
	ObservedAt       int64                           `json:"observed_at"`
	Intents          []AssistantIntentSummary        `json:"intents"`
	DistilledIntents []AssistantIntentSummary        `json:"distilled_intents"`
	Profiles         []AssistantProfileSummary       `json:"profiles"`
	Presets          []AssistantPresetReview         `json:"presets"`
	FirstQuestions   []AssistantFirstQuestionSummary `json:"first_questions"`
	CurrentSupport   int64                           `json:"current_pending_support"`
	CurrentIncidents int64                           `json:"current_open_security_incidents"`
	Commerce         AssistantCommerceReview         `json:"commerce"`
	Security         AssistantSecurityReview         `json:"security"`
	Actions          []AssistantReviewAction         `json:"actions"`
}

func trimReview[T any](rows []T) []T {
	if len(rows) > reviewListLimit {
		return rows[:reviewListLimit]
	}
	return rows
}

func reviewPresets(ctx context.Context, since, until int64) ([]AssistantPresetReview, error) {
	rows := make([]AssistantPresetReview, 0, maxPromptPresets)
	err := DB.WithContext(ctx).Model(&PromptPresetStat{}).
		Select("preset_id, SUM(click_count) AS clicks, SUM(conversation_count) AS conversations, SUM(recommendation_count) AS recommendations, SUM(approval_count) AS approvals").
		Where("bucket_start >= ? AND bucket_start <= ?", since, until).
		Group("preset_id").
		Order("approvals DESC, recommendations DESC, conversations DESC, clicks DESC, preset_id ASC").
		Limit(reviewListLimit).
		Scan(&rows).Error
	return rows, err
}

func reviewCount(ctx context.Context, table any, query string, args ...any) (int64, error) {
	var count int64
	err := DB.WithContext(ctx).Model(table).Where(query, args...).Count(&count).Error
	return count, err
}

func reviewSuccessfulOrderCount(ctx context.Context, tableName string, since, until int64) (int64, error) {
	query := DB.WithContext(ctx).Table(tableName+" AS orders").
		Joins(`JOIN (
  SELECT user_id, MIN(created_at) AS first_chat_at
  FROM assistant_leads
  WHERE source = ? AND created_at >= ? AND created_at <= ?
  GROUP BY user_id
) AS chat_cohort ON chat_cohort.user_id = orders.user_id`, AssistantLeadSourceChat, since, until).
		Where("orders.status = ?", common.TopUpStatusSuccess).
		Where("COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) >= chat_cohort.first_chat_at").
		Where("COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) >= ? AND COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) <= ?", since, until)
	// A completed subscription also creates a successful TopUp mirror for
	// wallet/order compatibility. It is not a second payment order and must
	// not inflate the automatic review's top-up conversion metric. Keep the
	// exclusion local to this source so real standalone top-ups are unchanged.
	if tableName == "top_ups" {
		query = query.Where(`NOT EXISTS (
  SELECT 1 FROM subscription_orders AS subscription_order
  WHERE subscription_order.trade_no = orders.trade_no
    AND subscription_order.status = ?
)`, common.TopUpStatusSuccess)
	}
	var count int64
	err := query.Count(&count).Error
	return count, err
}

func reviewDistinctChatUsers(ctx context.Context, since, until int64) (int64, error) {
	var count int64
	err := DB.WithContext(ctx).Model(&AssistantLead{}).
		Where("source = ? AND created_at >= ? AND created_at <= ?", AssistantLeadSourceChat, since, until).
		Distinct("user_id").Count(&count).Error
	return count, err
}

func reviewPaidChatUsers(ctx context.Context, since, until int64) (int64, error) {
	// Keep this as one bounded aggregate query.  The UNION removes users who
	// bought both a top-up and a subscription before the intersection with the
	// chat cohort; no user IDs leave the database.
	const query = `
SELECT COUNT(DISTINCT paid.user_id)
FROM (
  SELECT orders.user_id FROM top_ups AS orders
  JOIN (
    SELECT user_id, MIN(created_at) AS first_chat_at
    FROM assistant_leads
    WHERE source = ? AND created_at >= ? AND created_at <= ?
    GROUP BY user_id
  ) AS chat_cohort ON chat_cohort.user_id = orders.user_id
  WHERE orders.status = ? AND COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) >= chat_cohort.first_chat_at
    AND COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) >= ? AND COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) <= ?
  UNION
  SELECT orders.user_id FROM subscription_orders AS orders
  JOIN (
    SELECT user_id, MIN(created_at) AS first_chat_at
    FROM assistant_leads
    WHERE source = ? AND created_at >= ? AND created_at <= ?
    GROUP BY user_id
  ) AS chat_cohort ON chat_cohort.user_id = orders.user_id
  WHERE orders.status = ? AND COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) >= chat_cohort.first_chat_at
    AND COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) >= ? AND COALESCE(NULLIF(orders.complete_time, 0), orders.create_time) <= ?
) AS paid
`
	var count int64
	err := DB.WithContext(ctx).Raw(query,
		AssistantLeadSourceChat, since, until, common.TopUpStatusSuccess, since, until,
		AssistantLeadSourceChat, since, until, common.TopUpStatusSuccess, since, until,
	).Scan(&count).Error
	return count, err
}

func reviewRefunds(ctx context.Context, since, until int64) (int64, int64, error) {
	var result struct {
		Count  int64 `gorm:"column:count"`
		Amount int64 `gorm:"column:amount"`
	}
	err := DB.WithContext(ctx).Model(&FinanceLedgerEntry{}).
		Select("COUNT(*) AS count, COALESCE(SUM(amount_micros), 0) AS amount").
		Where("source_type = ? AND occurred_at >= ? AND occurred_at <= ?", FinanceSourceRefund, since, until).
		Scan(&result).Error
	return result.Count, result.Amount, err
}

func reviewCommerce(ctx context.Context, since, until int64) (AssistantCommerceReview, error) {
	var review AssistantCommerceReview
	var err error
	if review.ChatUsers, err = reviewDistinctChatUsers(ctx, since, until); err != nil {
		return AssistantCommerceReview{}, err
	}
	if review.SuccessfulTopUpOrders, err = reviewSuccessfulOrderCount(ctx, "top_ups", since, until); err != nil {
		return AssistantCommerceReview{}, err
	}
	if review.SuccessfulSubscriptionOrders, err = reviewSuccessfulOrderCount(ctx, "subscription_orders", since, until); err != nil {
		return AssistantCommerceReview{}, err
	}
	if review.PaidUsers, err = reviewPaidChatUsers(ctx, since, until); err != nil {
		return AssistantCommerceReview{}, err
	}
	if review.ChatUsers > 0 {
		review.ConversionRatePercent = math.Round(float64(review.PaidUsers)*10000/float64(review.ChatUsers)) / 100
	}
	if review.RefundCount, review.RefundAmountMicros, err = reviewRefunds(ctx, since, until); err != nil {
		return AssistantCommerceReview{}, err
	}
	return review, nil
}

func reviewActions(review AssistantReview) []AssistantReviewAction {
	actions := make([]AssistantReviewAction, 0, 8)
	if review.CurrentSupport > 0 {
		actions = append(actions, AssistantReviewAction{Code: "review_support_queue", Count: review.CurrentSupport})
	}
	if review.CurrentIncidents > 0 {
		actions = append(actions, AssistantReviewAction{Code: "review_security_incidents", Count: review.CurrentIncidents})
	}
	if review.Security.TotalMatches > 0 {
		actions = append(actions, AssistantReviewAction{Code: "review_security_events", Count: review.Security.TotalMatches})
	}
	if review.Security.ErrorLogCount > 0 {
		actions = append(actions, AssistantReviewAction{Code: "review_error_channels", Count: review.Security.ErrorLogCount})
	}

	var profiles, unknown int64
	for _, row := range review.Profiles {
		profiles += row.Count
		if row.Profile == AssistantProfileUnknown {
			unknown = row.Count
		}
	}
	if profiles > 0 && unknown*100 >= profiles*40 {
		actions = append(actions, AssistantReviewAction{Code: "improve_profile_classification", Count: unknown})
	}

	var clicks, conversations, recommendations, approvals int64
	for _, row := range review.Presets {
		clicks += row.Clicks
		conversations += row.Conversations
		recommendations += row.Recommendations
		approvals += row.Approvals
	}
	if clicks >= 5 && conversations*100 < clicks*40 {
		actions = append(actions, AssistantReviewAction{Code: "improve_pre_conversation_prompts", Count: clicks - conversations})
	}
	if recommendations >= 3 && approvals*100 < recommendations*30 {
		actions = append(actions, AssistantReviewAction{Code: "review_recommendation_quality", Count: recommendations - approvals})
	}
	if review.Commerce.ChatUsers >= 5 && review.Commerce.PaidUsers*100 < review.Commerce.ChatUsers*5 {
		actions = append(actions, AssistantReviewAction{Code: "review_chat_to_purchase_conversion", Count: review.Commerce.ChatUsers - review.Commerce.PaidUsers})
	}

	intentRows := review.DistilledIntents
	if len(intentRows) == 0 {
		intentRows = review.Intents
	}
	var intents, other int64
	for _, row := range intentRows {
		intents += row.Count
		if row.Intent == AssistantIntentOther {
			other = row.Count
		}
	}
	if intents > 0 && other*100 >= intents*40 {
		actions = append(actions, AssistantReviewAction{Code: "improve_intent_classification", Count: other})
	}

	sort.SliceStable(actions, func(i, j int) bool {
		if actions[i].Count != actions[j].Count {
			return actions[i].Count > actions[j].Count
		}
		return actions[i].Code < actions[j].Code
	})
	return actions
}

func BuildAssistantReview(ctx context.Context, start, end int64) (AssistantReview, error) {
	if DB == nil || start <= 0 || end < start {
		return AssistantReview{}, errors.New("invalid assistant review window")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	review := AssistantReview{WindowStart: start, WindowEnd: end, ObservedAt: end}
	var err error
	if review.Intents, err = listAssistantIntents(ctx, start, end); err != nil {
		return AssistantReview{}, err
	}
	review.Intents = trimReview(review.Intents)
	if review.Profiles, err = listAssistantProfiles(ctx, start, end); err != nil {
		return AssistantReview{}, err
	}
	review.Profiles = trimReview(review.Profiles)
	if review.Presets, err = reviewPresets(ctx, start, end); err != nil {
		return AssistantReview{}, err
	}
	if review.FirstQuestions, err = listAssistantFirstQuestions(ctx, start, end); err != nil {
		return AssistantReview{}, err
	}
	if review.DistilledIntents, err = listAssistantFirstQuestionThemes(ctx, start, end); err != nil {
		return AssistantReview{}, err
	}
	if review.CurrentSupport, err = reviewCount(ctx, &AssistantLead{}, "source = ? AND status = ?", AssistantLeadSourceHandoff, AssistantLeadStatusPending); err != nil {
		return AssistantReview{}, err
	}
	if review.CurrentIncidents, err = reviewCount(ctx, &AssistantSecurityIncident{}, "status = ?", AssistantSecurityIncidentStatusOpen); err != nil {
		return AssistantReview{}, err
	}
	if review.Commerce, err = reviewCommerce(ctx, start, end); err != nil {
		return AssistantReview{}, err
	}
	securityStats, err := GetAdvancedSecurityStats(AdvancedSecurityEventFilter{
		StartTimestamp: start,
		EndTimestamp:   end,
	})
	if err != nil {
		return AssistantReview{}, err
	}
	review.Security = AssistantSecurityReview{
		TotalMatches:     securityStats.TotalMatches,
		BlockedMatches:   securityStats.BlockedMatches,
		AuditedMatches:   securityStats.AuditedMatches,
		AffectedRequests: securityStats.AffectedRequests,
		AffectedUsers:    securityStats.AffectedUsers,
		ByCategory:       trimReview(securityStats.ByCategory),
		ByRule:           trimReview(securityStats.ByRule),
	}
	errorStats, err := GetAssistantErrorLogReview(ctx, start, end)
	if err != nil {
		return AssistantReview{}, err
	}
	review.Security.ErrorLogCount = errorStats.Count
	review.Security.ErrorChannels = errorStats.Channels
	review.Security.ErrorModels = errorStats.Models
	review.Actions = reviewActions(review)
	return review, nil
}
