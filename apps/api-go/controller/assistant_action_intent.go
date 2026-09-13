package controller

import (
	"regexp"
	"strings"
)

type assistantActionRule struct {
	negative *regexp.Regexp
	target   *regexp.Regexp
}

const assistantNegativePrefix = `(?:不(?:需要|想要|愿意|允许|可以|能够|想|要|用|需|必|能|可)?|无需|没有必要|别|请勿|勿|拒绝|do\s+not(?:\s+(?:want|need)(?:\s+you)?(?:\s+to)?)?|don't(?:\s+(?:want|need)(?:\s+you)?(?:\s+to)?)?|(?:must|should|can|could|would)\s+not|can't|cannot|shouldn't|mustn't|never|no\s+need(?:\s+to)?|not\s+to)`
const assistantActionModifiers = `(?:\s*(?:再|先|暂时|现在|帮我|替我|给我|为我|帮忙|请|你|您|to|for|please|a|an|any|the|my|me)\s*)*`

var (
	assistantIntentClauses            = regexp.MustCompile(`[，,。.;；!?！？\n]+`)
	assistantClauseNegation           = regexp.MustCompile(assistantNegativePrefix)
	assistantReadOnlyInstruction      = regexp.MustCompile(`(?:^|[，,。.;；!?！？\n])\s*(?:请)?(?:只读(?:验收|检查|测试|模式|操作)?|read[- ]only(?:\s+(?:test|check|audit|validation))?|仅查询|只查询)(?:$|[\s:：，,。.;；!?！？])`)
	assistantKeyActionRule            = newAssistantActionRule("创建", "新建", "生成", "开一个", "建一个", "create", "generate", "make", "new key")
	assistantProfileActionRule        = newAssistantActionRule("删除", "删掉", "移除", "清除", "清空", "忘记", "重置", "delete", "remove", "erase", "clear", "forget", "reset")
	assistantRecommendationActionRule = newAssistantActionRule("删除", "删掉", "移除", "清空", "清除", "撤回", "润色", "修改", "改写", "重写", "编辑", "优化", "更新", "替换", "精简", "缩短", "扩写", "delete", "remove", "clear", "discard", "polish", "edit", "revise", "rewrite", "update", "improve", "replace", "shorten", "expand")
	assistantGiftActionRule           = newAssistantActionRule("领取", "申请", "赠送", "免费额度", "赠送额度", "新用户礼包", "新用户福利", "新手礼包", "礼包", "claim", "give", "free credit", "welcome gift", "welcome bonus", "new user gift", "new-user gift")
	assistantDiscountActionRule       = newAssistantActionRule("领取", "申请", "优惠码", "折扣码", "每周折扣", "每周优惠", "本周优惠", "充值折扣", "claim", "give", "weekly discount", "weekly coupon", "discount code", "coupon")
	assistantSupportActionRule        = newAssistantActionRule("提交", "转人工", "联系", "预约", "转交", "人工核查", "工单", "submit", "contact", "send", "transfer", "book", "request human support", "human review", "support ticket")
)

func newAssistantActionRule(actions ...string) assistantActionRule {
	for i := range actions {
		actions[i] = regexp.QuoteMeta(actions[i])
	}
	target := "(?:" + strings.Join(actions, "|") + ")"
	return assistantActionRule{
		negative: regexp.MustCompile(assistantNegativePrefix + assistantActionModifiers + `\s*` + target),
		target:   regexp.MustCompile(target),
	}
}

// Refusals apply to the requested action, not unrelated prose such as
// “don't explain, create a key”. A refusal also covers coordinated actions
// in the same clause: “不要创建密钥、修改配置或提交工单”.
func assistantActionDeclined(message string, rule assistantActionRule) bool {
	text := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(message, "’", "'")))
	if assistantReadOnlyInstruction.MatchString(text) {
		return true
	}
	switch strings.Trim(text, " ，,。.;；!?！？\n") {
	case "不要了", "不用了", "不需要了", "算了", "取消", "cancel", "never mind", "nevermind", "no thanks":
		return true
	}
	for _, clause := range assistantIntentClauses.Split(text, -1) {
		if rule.negative.MatchString(clause) {
			return true
		}
		if strings.ContainsAny(clause, "、或") || strings.Contains(clause, " or ") {
			negative := assistantClauseNegation.FindStringIndex(clause)
			target := rule.target.FindStringIndex(clause)
			if negative != nil && target != nil && negative[0] < target[0] {
				return true
			}
		}
	}
	return false
}
