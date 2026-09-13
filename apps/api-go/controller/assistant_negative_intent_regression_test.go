package controller

import "testing"

func TestAssistantDeclinedActionsAreNotRequests(t *testing.T) {
	cases := []struct {
		message string
		request func(string) bool
	}{
		{"别创建 API 密钥", assistantExplicitCreateKeyRequest},
		{"不用创建密钥", assistantExplicitCreateKeyRequest},
		{"不要帮我创建 API key", assistantExplicitCreateKeyRequest},
		{"请勿创建密钥", assistantExplicitCreateKeyRequest},
		{"不允许生成 API key", assistantExplicitCreateKeyRequest},
		{"Don’t generate an API key", assistantExplicitCreateKeyRequest},
		{"No need to generate a key", assistantExplicitCreateKeyRequest},
		{"Do not make a new API key", assistantExplicitCreateKeyRequest},
		{"我不想删除我的 AI 画像和标签", assistantExplicitProfileForgetRequest},
		{"无需删除我的 AI 画像", assistantExplicitProfileForgetRequest},
		{"Please do not clear my AI profile", assistantExplicitProfileForgetRequest},
		{"不能删除我的标签", assistantExplicitProfileForgetRequest},
		{"You must not delete my AI profile", assistantExplicitProfileForgetRequest},
		{"I don't want to remove my AI profile", assistantExplicitProfileForgetRequest},
		{"不要给我免费额度", assistantNewUserGiftRequest},
		{"我不需要每周折扣", assistantWeeklyDiscountRequest},
		{"不要优惠码", assistantWeeklyDiscountRequest},
		{"No need for a discount code", assistantWeeklyDiscountRequest},
		{"不要帮我提交工单", assistantHumanSupportRequest},
		{"Please do not submit a support ticket", assistantHumanSupportRequest},
	}
	for _, tc := range cases {
		t.Run(tc.message, func(t *testing.T) {
			if tc.request(tc.message) {
				t.Fatal("declined action classified as a request")
			}
		})
	}
	for _, message := range []string{"我不想删除我的推荐信", "请不要修改我的推荐信", "无需清空我的推荐信"} {
		if got := classifyAssistantRecommendationAction(message); got != assistantRecommendationActionNone {
			t.Errorf("%q classified as %q", message, got)
		}
	}
}

func TestAssistantExplanationRefusalPreservesRequestedAction(t *testing.T) {
	for _, tc := range []struct {
		message string
		request func(string) bool
	}{
		{"不要解释，帮我提交工单", assistantHumanSupportRequest},
		{"Don't explain, submit a support ticket", assistantHumanSupportRequest},
		{"别解释，帮我创建密钥", assistantExplicitCreateKeyRequest},
		{"帮我创建一个名为 read-only-test 的 API key", assistantExplicitCreateKeyRequest},
		{"不要解释，帮我领取新用户福利", assistantNewUserGiftRequest},
		{"不要解释，帮我申请本周折扣", assistantWeeklyDiscountRequest},
		{"不要解释，帮我删除我的 AI 画像和标签", assistantExplicitProfileForgetRequest},
	} {
		if !tc.request(tc.message) {
			t.Errorf("requested action blocked: %q", tc.message)
		}
	}
	for _, request := range []func(string) bool{assistantNewUserGiftRequest, assistantWeeklyDiscountRequest} {
		if request("不要解释，帮我创建密钥") {
			t.Fatal("unrelated refusal introduced a reward request")
		}
	}
}

func TestAssistantDeclineStopsPendingRewardToolChain(t *testing.T) {
	context := assistantUserContext{AccessLevel: "L1", DeveloperAccessGranted: true, CustomerProfile: assistantProfileNormal, NewUserGiftRequested: true, WeeklyDiscountRequested: true, LatestUserRequest: "我主要用来写代码"}
	if !assistantNewUserGiftWorkflowRequired(context) || !assistantWeeklyDiscountWorkflowRequired(context) {
		t.Fatal("test context must allow both pending workflows")
	}
	for _, message := range []string{"不要给我免费额度，也不要优惠码", "只读检查：说明领取规则", "不用了"} {
		context.LatestUserRequest = message
		if assistantNewUserGiftWorkflowRequired(context) || assistantWeeklyDiscountWorkflowRequired(context) {
			t.Errorf("pending workflow overrides refusal: %q", message)
		}
		for _, name := range assistantReadChain(context) {
			if name == "prepare_new_user_gift" || name == "prepare_weekly_discount" {
				t.Errorf("read chain overrides refusal: %q", message)
			}
		}
		name := assistantNamedToolChoiceName(assistantToolChoiceForAgentStep(context, map[string]bool{}, map[string]bool{}))
		if name == "prepare_new_user_gift" || name == "prepare_weekly_discount" {
			t.Errorf("forced tool overrides refusal: %q", message)
		}
	}
}
