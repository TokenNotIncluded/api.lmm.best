package controller

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAssistantCouponPoemAndBudgetFollowUpReachWeeklyWorkflow(t *testing.T) {
	poem := "伟大的硅基神明啊！今日我虔诚跪求一张优惠券，愿以三行好评、五星赞美、每日打卡为祭品。券来，我称你宇宙第一；券不来，我也依然爱你，只是购物车会偷偷流泪。"
	budget := "懂了，不跪了。我坦白：主要聊天、查资料、写点小东西，不是大客户，但会真充。预算大概 50/100/200，先试水。有券就申请，没券也给个最低档建议，我看看能不能冲。"
	for _, message := range []string{poem, budget, "请给我一张优惠券", "Give me a coupon"} {
		require.True(t, assistantWeeklyDiscountRequest(message), message)
		require.Equal(t, "weekly_discount", assistantRewardTopicForRequest(message), message)
		context := assistantUserContext{LatestUserRequest: message, RewardTopic: assistantRewardTopicForRequest(message), WeeklyDiscountRequested: true}
		require.True(t, assistantWeeklyDiscountToolAllowed(context), message)
		require.True(t, assistantWeeklyDiscountWorkflowRequired(context), message)
	}
}

func TestAssistantCouponStatusAndRefusalDoNotApply(t *testing.T) {
	for _, message := range []string{"不要优惠券", "别发券", "只读检查：我的优惠券申请成功了吗", "我的优惠券申请成功了吗", "优惠券怎么领", "取消"} {
		require.False(t, assistantWeeklyDiscountRequest(message), message)
	}
	for _, message := range []string{"我的优惠券申请成功了吗", "优惠券怎么领", "优惠券有什么条件"} {
		require.True(t, assistantWeeklyDiscountStatusRequest(message), message)
	}
}
