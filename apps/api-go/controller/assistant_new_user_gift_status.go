package controller

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

// A status question is not authorization for a lifetime reward decision, even
// when an earlier turn applied for a gift or supplied a legitimate workflow.
// When gift wording is ambiguous, prefer reading the durable record.
func assistantNewUserGiftStatusRequest(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return false
	}
	if assistantExplicitWeeklyDiscountTopic(text) && !assistantExplicitNewUserGiftTopic(text) {
		return false
	}
	if assistantExplicitOtherRewardTopic(text) && !assistantExplicitWelcomeGiftRequest(text) && !strings.Contains(text, "礼包") {
		return false
	}
	if assistantRewardDecisionCompletionQuestion(text) {
		return true
	}
	if assistantTextContainsAny(text,
		"领取了吗", "领取了没", "领过了吗", "领过吗", "领了吗", "领取成功", "领取是否成功", "是否领取", "有没有领取", "已经领取",
		"评估过", "评估了吗", "被评估", "决定了吗", "决定了没", "已经决定", "evaluated", "decided", "evaluation status", "decision status",
		"did i claim", "have i claimed", "already claimed", "claim successful", "was my claim",
		"算实质交流", "算实质性交流", "算是实质交流", "算不算实质", "算有效交流", "counts as substantive", "count as substantive",
	) {
		return true
	}
	hasGift := assistantExplicitWelcomeGiftRequest(text) || assistantTextContainsAny(text,
		"礼包", "赠送额度", "免费额度", "welcome credit", "free credit",
	)
	if !hasGift {
		return false
	}
	if assistantTextContainsAny(text,
		"状态", "查询", "查看", "领过", "领了", "领到", "有礼包", "有没有", "是否有", "还有", "申请的", "申请过", "申请了", "申请结果", "申请成功", "审核", "通过了", "结果", "需要申请", "需要领取", "申请吗", "规则", "条件", "要求", "资格", "还差", "怎么", "如何", "怎样", "能申请", "可以申请", "能领取", "可以领取", "能获得", "可以获得",
		"status", "check", "show", "have a", "do i have", "is there", "available", "rules", "requirement", "criteria", "eligible", "eligibility", "how do", "how to",
	) {
		// An explicit request to evaluate under the rules is still a decision
		// request; a question about rules or eligibility alone is not.
		if !assistantTextContainsAny(text, "评估", "决定", "evaluate", "decide") || assistantTextContainsAny(text, "结果", "审核", "通过了", "result", "evaluated", "decided") {
			return true
		}
	}
	return !assistantExplicitGiftDecisionRequest(text)
}

func assistantExplicitGiftDecisionRequest(text string) bool {
	if assistantRewardDecisionCompletionQuestion(text) {
		return false
	}
	return assistantTextContainsAny(strings.ToLower(text),
		"申请", "领取", "想领", "要领", "帮我领", "给我", "送我", "发我", "希望给",
		"请评估", "帮我评估", "评估我", "评估一下", "评估新", "评估礼包", "现在评估", "想评估", "我要评估", "你来评估", "麻烦评估", "请决定", "帮我决定", "决定给我",
		"apply", "i want to claim", "i'd like to claim", "claim my", "claim the", "please claim", "give me", "grant me", "please evaluate", "evaluate my", "evaluate me", "evaluate the", "please decide", "decide my", "decide the",
	)
}

// A quoted action phrase is not a new instruction when the user is asking
// whether that action already happened. Check the question's position around
// the operation so "please evaluate whether I qualify" remains an application.
func assistantRewardDecisionCompletionQuestion(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	operationAt := len(text)
	for _, operation := range []string{"评估", "决定", "申请", "领取", "发放", "发码", "evaluate", "decid", "appl", "claim", "issu"} {
		if at := strings.Index(text, operation); at >= 0 && at < operationAt {
			operationAt = at
		}
	}
	if operationAt == len(text) {
		return false
	}
	if assistantTextContainsAny(text[:operationAt],
		"有没有", "是否", "是不是", "有无", "did you", "have you", "had you", "has my", "was my", "did i", "have i", "has it", "was it",
	) {
		return true
	}
	operationText := text[operationAt:]
	if assistantTextContainsAny(operationText, "了吗", "了没", "过吗", "完了吗", "完了没") {
		return true
	}
	// The object can separate the completed-action marker from the question
	// particle: "evaluated [the reward] already?" is still a status read.
	question := assistantTextContainsAny(operationText, "吗", "么", "没有", "没")
	if strings.Contains(operationText, "了") && question {
		return true
	}
	// A question addressed to the assistant is not a fresh evaluation command.
	// Treat ambiguous "you ... evaluate ... ?" wording conservatively; explicit
	// "please evaluate whether I qualify" keeps its application semantics.
	addressed := strings.HasPrefix(text, "你") || strings.HasPrefix(text, "您") || strings.HasPrefix(text, "you ")
	return addressed && (question || strings.Contains(operationText, "?"))
}

func assistantNewUserGiftStatusWorkflowRequired(context assistantUserContext) bool {
	if assistantExplicitNewUserGiftTopic(context.LatestUserRequest) {
		return assistantNewUserGiftStatusRequest(context.LatestUserRequest)
	}
	if context.RewardTopic == "weekly_discount" || context.RewardTopic == "other" || assistantExplicitWeeklyDiscountTopic(context.LatestUserRequest) {
		return false
	}
	return assistantNewUserGiftStatusRequest(context.LatestUserRequest) ||
		((context.RewardTopic == "gift" || context.NewUserGiftRequested) && assistantRewardReadOnlyFollowUp(context.LatestUserRequest))
}

func assistantWeeklyDiscountStatusWorkflowRequired(context assistantUserContext) bool {
	return assistantWeeklyDiscountStatusRequest(context.LatestUserRequest) ||
		(context.RewardTopic == "weekly_discount" && assistantRewardReadOnlyFollowUp(context.LatestUserRequest))
}

func assistantExplicitNewUserGiftTopic(text string) bool {
	return assistantExplicitWelcomeGiftRequest(text) || assistantTextContainsAny(strings.ToLower(text), "礼包", "赠送额度", "免费额度", "welcome credit", "free credit")
}

func assistantExplicitWeeklyDiscountTopic(text string) bool {
	return assistantTextContainsAny(strings.ToLower(text), "折扣", "优惠码", "优惠券", "每周优惠", "本周优惠", "weekly discount", "weekly coupon", "recharge discount", "discount code", "coupon")
}

func assistantExplicitOtherRewardTopic(text string) bool {
	return assistantTextContainsAny(strings.ToLower(text), "签到", "打卡", "每日奖励", "邀请", "推荐奖励", "referral", "invitation reward", "check-in", "check in", "daily reward")
}

func assistantRewardReadOnlyFollowUp(text string) bool {
	text = strings.ToLower(text)
	if assistantRewardDecisionCompletionQuestion(text) {
		return true
	}
	if assistantTextContainsAny(text,
		"领取了吗", "领取了没", "领过吗", "领了吗", "领取成功", "是否领取", "有没有领取", "已经领取", "还差什么条件", "还需要什么条件", "还缺什么条件", "算实质交流", "算实质性交流", "算不算实质",
		"评估过", "评估了吗", "被评估", "决定了吗", "决定了没", "已经决定", "evaluated", "decided", "evaluation status", "decision status",
		"还能领取", "可以领取", "能领取吗", "怎么领", "如何领取", "怎么领取", "have i claimed", "did i claim", "already claimed", "claim successful", "what conditions", "requirements left", "count as substantive", "counts as substantive", "how do i claim", "can i claim",
	) {
		return true
	}
	if assistantExplicitGiftDecisionRequest(text) {
		return false
	}
	return assistantTextContainsAny(text, "规则", "条件", "资格", "标准", "当前状态", "目前多少", "rules", "requirements", "criteria", "eligibility", "current status")
}

// RewardTopic records only the most recent explicit topic in this request's
// conversation. A later L1 or client setup question clears an older reward
// topic; a generic use-case answer can still continue an actual application.
func assistantRewardTopicForRequest(message string, conversations ...[]assistantOpenAIMessage) string {
	topic := func(text string) string {
		// An explicit coupon application can mention check-in as context, not
		// as a request to switch away from the coupon workflow.
		if assistantWeeklyDiscountRequest(text) && !assistantExplicitNewUserGiftTopic(text) {
			return "weekly_discount"
		}
		if assistantExplicitOtherRewardTopic(text) && !assistantExplicitWelcomeGiftRequest(text) && !strings.Contains(text, "礼包") {
			return "other"
		}
		if assistantExplicitNewUserGiftTopic(text) || assistantNewUserGiftRequest(text) {
			return "gift"
		}
		if assistantExplicitWeeklyDiscountTopic(text) {
			return "weekly_discount"
		}
		if assistantTextContainsAny(strings.ToLower(text), "l1", "权限", "激活", "怎么配置", "如何配置", "配置客户端", "客户端配置", "连接客户端", "client setup", "configure my", "set up my") {
			return "other"
		}
		return ""
	}
	if current := topic(message); current != "" {
		return current
	}
	for _, conversation := range conversations {
		for i := len(conversation) - 1; i >= 0; i-- {
			if conversation[i].Role == "user" {
				if previous := topic(conversation[i].Content); previous != "" {
					return previous
				}
			}
		}
	}
	return ""
}

func assistantRewardContextForGuard(c *gin.Context) assistantUserContext {
	if c == nil {
		return assistantUserContext{}
	}
	context := assistantUserContextFromGin(c)
	context.LatestUserRequest = assistantGiftCurrentRequest(c)
	for _, key := range []string{assistantPolicyConversationKey, "assistant_conversation"} {
		if raw, exists := c.Get(key); exists {
			if messages, ok := raw.([]assistantOpenAIMessage); ok {
				context.RewardTopic = assistantRewardTopicForRequest(context.LatestUserRequest, messages)
				return context
			}
		}
	}
	if topic := assistantRewardTopicForRequest(context.LatestUserRequest); topic != "" {
		context.RewardTopic = topic
	}
	return context
}

func assistantRewardReadOnlyRequest(c *gin.Context) bool {
	context := assistantRewardContextForGuard(c)
	return assistantNewUserGiftStatusWorkflowRequired(context) || assistantWeeklyDiscountStatusWorkflowRequired(context) || assistantRewardReadOnlyFollowUp(context.LatestUserRequest)
}

func assistantGiftCurrentRequest(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if text := strings.TrimSpace(c.GetString("assistant_history_latest_message")); text != "" {
		return text
	}
	if text := assistantUserContextFromGin(c).LatestUserRequest; strings.TrimSpace(text) != "" {
		return text
	}
	for _, key := range []string{assistantPolicyConversationKey, "assistant_conversation"} {
		if raw, exists := c.Get(key); exists {
			if messages, ok := raw.([]assistantOpenAIMessage); ok {
				for i := len(messages) - 1; i >= 0; i-- {
					if messages[i].Role == "user" {
						return messages[i].Content
					}
				}
			}
		}
	}
	return ""
}

func assistantGiftReadOnlyRequestResult() map[string]any {
	return map[string]any{
		"ok": false, "status": "read_only_request", "mutation_attempted": false,
		"error":     "A gift status, claim-result, or rules question cannot consume the one-time decision.",
		"next_step": "Call get_new_user_gift_status to read the current user's stored status; do not evaluate a gift in this request.",
	}
}

func assistantWeeklyDiscountReadOnlyRequestResult() map[string]any {
	return map[string]any{
		"ok": false, "status": "read_only_request", "mutation_attempted": false,
		"error":     "A weekly discount status, claim-result, or rules question cannot consume this week's decision.",
		"next_step": "Call get_weekly_discount_status to read the current user's stored weekly status; do not evaluate a discount in this request.",
	}
}

func executeAssistantNewUserGiftStatusTool(c *gin.Context, userID int) map[string]any {
	if userID <= 0 {
		return map[string]any{"ok": false, "status": "unavailable", "read_only": true, "error": "A signed-in user is required to read their gift status."}
	}
	// Only read the stored gift and current cap. In particular, do not call Decide,
	// registration/risk observation, or eligibility checks from a status read.
	gift, err := model.GetAssistantNewUserGift(userID)
	if err != nil {
		return map[string]any{
			"ok": false, "status": "unavailable", "read_only": true,
			"error": "The current gift status could not be read. Do not infer eligibility, a decision, or a successful claim.",
		}
	}
	result, err := assistantGiftMoneyFields(gift)
	if err != nil {
		return map[string]any{"ok": false, "status": "unavailable", "read_only": true, "error": "Gift currency units are unavailable. Do not infer an amount or a successful claim."}
	}
	for key, value := range map[string]any{
		"ok": true, "read_only": true, "status": "none",
		"one_time_decision_used": false, "claim_available": result["claim_available"],
		"next_step": "No gift decision is stored. This does not establish eligibility. Explain the one-time rules if asked; an evaluation requires a separate explicit application and sufficient conversation detail.",
	} {
		result[key] = value
	}
	if gift == nil {
		return result
	}
	result["status"] = gift.Status
	result["one_time_decision_used"] = true
	switch gift.Status {
	case model.AssistantGiftOffered:
		if result["claim_available"] != true {
			result["next_step"] = "The stored offer is unchanged but cannot currently be claimed under the administrator-configured gift cap. Do not promise a claim or create another decision."
			return result
		}
		result["next_step"] = "The existing offered gift is ready for the user to claim from the gift card. Never claim it for them or evaluate it again."
		if c != nil {
			money := make(map[string]any)
			for _, key := range []string{"amount_cents", "amount_unit", "credit_amount", "credit_amount_unit", "public_credit_amount", "amount_usd", "currency", "credits_per_usd", "credit_unit_schema_version", "quota_unit", "public_credit_unit", "legacy_credit_unit", "ledger_quota_per_usd", "ledger_quota_per_usd_exact", "public_credits_per_usd", "public_credits_per_usd_exact", "max_credit_amount", "claim_available", "claim_blocked_code"} {
				money[key] = result[key]
			}
			money["type"], money["status"] = "new_user_gift", gift.Status
			// Keep the card useful without exposing stored evaluation prose.
			money["reason"] = "领取已发放的新用户礼包 / Claim your existing welcome gift"
			c.Set(assistantClientActionKey, money)
		}
	case model.AssistantGiftClaimed:
		result["next_step"] = "The stored gift has already been claimed. The one-time decision cannot be reset by changing conversations."
	case model.AssistantGiftDeclined:
		result["next_step"] = "The one-time decision was completed without an offered gift. It cannot be reset or reevaluated by changing conversations."
	default:
		return map[string]any{"ok": false, "status": "unavailable", "read_only": true, "error": "The stored gift status is unavailable. Do not offer or reevaluate a gift."}
	}
	return result
}
