package controller

import (
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

// Weekly reward questions are reads unless the current message explicitly
// asks for a decision. Follow-ups without a reward topic are resolved by the
// request context, so they must not become weekly requests globally.
func assistantWeeklyDiscountStatusRequest(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" || !assistantTextContainsAny(text,
		"优惠券", "coupon", "每周折扣", "本周折扣", "周折扣", "每周优惠", "本周优惠", "充值折扣", "优惠码", "折扣码",
		"weekly discount", "weekly coupon", "recharge discount", "discount code",
	) {
		return false
	}
	if assistantActionDeclined(text, assistantDiscountActionRule) {
		return false
	}
	if assistantRewardDecisionCompletionQuestion(text) {
		return true
	}
	if assistantTextContainsAny(text,
		"领取了吗", "领取了没", "领过了吗", "领过吗", "领了吗", "领取成功", "领取是否成功", "是否领取", "有没有领取", "已经领取", "已领取",
		"申请结果", "申请成功", "申请过", "申请的", "评估结果", "审核结果", "评估过", "决定过",
		"评估了吗", "被评估", "决定了吗", "决定了没", "已经决定", "evaluated", "decided",
		"did i claim", "have i claimed", "already claimed", "claim successful", "was my claim", "application status", "application result", "evaluation result", "decision result",
	) {
		return true
	}
	if assistantTextContainsAny(text,
		"求一张", "求个", "给张", "来张", "我想申请", "我要申请", "我希望申请", "帮我申请", "替我申请", "请申请", "想领", "要领", "帮我领",
		"给我优惠码", "给我折扣码", "发我优惠码", "发我折扣码", "送我优惠码", "送我折扣码", "给我本周折扣", "给我每周折扣",
		"i want to apply", "i'd like to apply", "i want to claim", "i'd like to claim", "please claim", "grant me", "give me a discount", "give me the discount",
	) || strings.HasPrefix(text, "apply ") || strings.HasPrefix(text, "please apply ") || strings.HasPrefix(text, "claim ") {
		return false
	}
	// Asking to evaluate according to the rules is still an application.
	if assistantTextContainsAny(text, "评估", "决定", "evaluate", "decide") && assistantExplicitGiftDecisionRequest(text) {
		return false
	}
	if assistantTextContainsAny(text,
		"状态", "查询", "查看", "领过", "领了", "领到", "有没有", "是否有", "还有", "审核", "通过了", "结果", "需要申请", "需要领取", "申请吗", "规则", "条件", "要求", "资格", "还差", "怎么", "如何", "怎样", "能申请", "可以申请", "能领取", "可以领取", "能获得", "可以获得",
		"status", "check", "show", "do i have", "is there", "available", "rules", "requirement", "criteria", "eligible", "eligibility", "how do", "how to", "can i apply", "can i claim",
	) {
		return true
	}
	return !assistantTextContainsAny(text,
		"求一张", "求个", "给张", "来张", "申请", "领取", "想领", "要领", "帮我领", "给我", "送我", "发我", "希望给",
		"apply", "i want to claim", "i'd like to claim", "claim my", "claim the", "please claim", "give me", "grant me",
	)
}

func assistantCurrentUTCWeekStart() int64 {
	now := time.Now().UTC()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return day.AddDate(0, 0, -(int(day.Weekday())+6)%7).Unix()
}

func executeAssistantWeeklyDiscountStatusTool(c *gin.Context, userID int) map[string]any {
	if userID <= 0 {
		return map[string]any{"ok": false, "status": "unavailable", "read_only": true, "error": "A signed-in user is required to read their weekly discount status."}
	}
	// Only read the trusted actor's existing record for the current UTC week.
	// Do not decide, claim, or generate a discount code for a status question.
	reward, err := model.GetAssistantWeeklyDiscount(userID)
	if err != nil {
		return map[string]any{
			"ok": false, "status": "unavailable", "read_only": true,
			"error": "The current weekly discount status could not be read. Do not infer a decision or a successful claim.",
		}
	}
	result := map[string]any{
		"ok": true, "read_only": true, "status": "none", "current_utc_week": assistantCurrentUTCWeekStart(),
		"decision_used": false, "claim_available": false,
		"next_step": "No weekly discount decision is stored for the current UTC week. This does not establish eligibility. Explain the weekly rules if asked; an evaluation requires a separate explicit application and sufficient conversation detail.",
	}
	if reward == nil {
		return result
	}
	result["current_utc_week"] = reward.WeekStart
	result["status"] = reward.Status
	result["decision_used"] = true
	result["discount_percent"] = reward.DiscountPercent
	switch reward.Status {
	case model.AssistantWeeklyDiscountOffered:
		result["claim_available"] = true
		result["next_step"] = "The existing weekly discount is ready for the user to claim from the discount card during the current UTC week. Never claim it for them or evaluate it again this week."
		if c != nil {
			c.Set(assistantClientActionKey, map[string]any{
				"type": "weekly_discount", "discount_percent": reward.DiscountPercent, "status": reward.Status,
				"reason": "领取已发放的本周折扣 / Claim your existing weekly discount",
			})
		}
	case model.AssistantWeeklyDiscountClaimed:
		result["next_step"] = "The stored weekly discount has already been claimed. The decision cannot be reset by changing conversations; the user may view their existing private code in the weekly discount card."
	case model.AssistantWeeklyDiscountDeclined:
		result["next_step"] = "This UTC week's decision was completed without an offered discount. It cannot be reset or reevaluated by changing conversations."
	default:
		return map[string]any{"ok": false, "status": "unavailable", "read_only": true, "error": "The stored weekly discount status is unavailable. Do not offer or reevaluate a discount."}
	}
	return result
}
