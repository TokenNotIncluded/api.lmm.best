package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantNewUserGiftStatusReadDoesNotConsumeOrModifyDecision(t *testing.T) {
	setupAssistantCurrencyTest(t)
	for _, state := range []string{"none", model.AssistantGiftOffered, model.AssistantGiftClaimed, model.AssistantGiftDeclined} {
		t.Run(state, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.AssistantNewUserGift{}, &model.AssistantGiftRiskKey{}, &model.AssistantGiftRiskMemory{}, &model.TopUp{}))
			user := model.User{Username: "gift-status-" + state, Email: state + "@example.test", AffCode: "gift-status-" + state, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 456}
			require.NoError(t, db.Create(&user).Error)
			gift := model.AssistantNewUserGift{UserId: user.Id, ConversationId: 987, Status: state, AmountCents: 525, Quota: 2625000, Reason: "internal evaluation details must not enter a status response", CreatedAt: 100}
			if state == model.AssistantGiftDeclined {
				gift.AmountCents, gift.Quota = 0, 0
			}
			if state == model.AssistantGiftClaimed {
				gift.ClaimedAt = 200
			}
			if state != "none" {
				require.NoError(t, db.Create(&gift).Error)
			}
			for range 2 {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set(assistantActorUserIDKey, user.Id)
				c.Set(assistantUserContextKey, assistantUserContext{UserID: user.Id, AccessLevel: "L0", LatestUserRequest: "我领取了吗"})
				result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "get_new_user_gift_status", Arguments: "{}"}})
				assert.Equal(t, true, result["ok"])
				assert.Equal(t, true, result["read_only"])
				assert.Equal(t, state, result["status"])
				assert.Equal(t, "USD", result["currency"])
				assert.Equal(t, state != "none", result["one_time_decision_used"])
				assert.Equal(t, state == model.AssistantGiftOffered, result["claim_available"])
				expectedAmount := gift.AmountCents
				if state == "none" {
					expectedAmount = 0
					assert.Contains(t, result["next_step"], "does not establish eligibility")
				}
				assert.Equal(t, expectedAmount, result["amount_cents"])
				assert.Equal(t, "LEGACY_CENTS", result["amount_unit"])
				if state == model.AssistantGiftOffered || state == model.AssistantGiftClaimed {
					assert.Equal(t, 2625000, result["credit_amount"])
					assert.Equal(t, float64(0.75), result["amount_usd"])
				} else {
					assert.Equal(t, 0, result["credit_amount"])
					assert.Equal(t, float64(0), result["amount_usd"])
				}
				encoded, err := json.Marshal(result)
				require.NoError(t, err)
				for _, hidden := range []string{"internal evaluation", "reason", "conversation_id", "user_id", "quota", "confirmation_token"} {
					assert.NotContains(t, string(encoded), hidden)
				}
				action, hasAction := c.Get(assistantClientActionKey)
				assert.Equal(t, state == model.AssistantGiftOffered, hasAction)
				if hasAction {
					card := action.(map[string]any)
					assert.Equal(t, gift.AmountCents, card["amount_cents"])
					assert.Equal(t, 2625000, card["credit_amount"])
					assert.Equal(t, float64(0.75), card["amount_usd"])
					assert.Equal(t, model.AssistantGiftOffered, card["status"])
					assert.NotEqual(t, gift.Reason, card["reason"])
				}
			}
			var storedUser model.User
			require.NoError(t, db.First(&storedUser, user.Id).Error)
			assert.Equal(t, user.Quota, storedUser.Quota)
			var giftCount int64
			require.NoError(t, db.Model(&model.AssistantNewUserGift{}).Count(&giftCount).Error)
			if state == "none" {
				assert.Zero(t, giftCount)
			} else {
				assert.EqualValues(t, 1, giftCount)
				stored, err := model.GetAssistantNewUserGift(user.Id)
				require.NoError(t, err)
				assert.Equal(t, gift, *stored)
			}
			for _, table := range []any{&model.AssistantGiftRiskKey{}, &model.AssistantGiftRiskMemory{}, &model.TopUp{}} {
				var count int64
				require.NoError(t, db.Model(table).Count(&count).Error)
				assert.Zero(t, count)
			}
		})
	}
}

func TestAssistantNewUserGiftStatusToolReadsOnlyActor(t *testing.T) {
	setupAssistantCurrencyTest(t)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantNewUserGift{}))
	require.NoError(t, db.Create(&model.AssistantNewUserGift{UserId: 11, Status: model.AssistantGiftClaimed, AmountCents: 123, Quota: 615000, Reason: "private owner reason"}).Error)
	require.NoError(t, db.Create(&model.AssistantNewUserGift{UserId: 22, Status: model.AssistantGiftOffered, AmountCents: 900, Reason: "other user reason"}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(assistantActorUserIDKey, 11)
	c.Set("id", 22)
	result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "get_new_user_gift_status", Arguments: `{"user_id":22,"identifier":"22"}`}})
	assert.Equal(t, model.AssistantGiftClaimed, result["status"])
	assert.Equal(t, 123, result["amount_cents"])
	assert.Equal(t, false, result["claim_available"])
	_, hasAction := c.Get(assistantClientActionKey)
	assert.False(t, hasAction)
}

func TestAssistantNewUserGiftStatusReadUnavailableDoesNotInferNone(t *testing.T) {
	setupTokenControllerTestDB(t) // No gift table: simulate a failed database read.
	for _, userID := range []int{123, 0} {
		result := executeAssistantNewUserGiftStatusTool(nil, userID)
		assert.Equal(t, false, result["ok"])
		assert.Equal(t, "unavailable", result["status"])
		assert.NotContains(t, result, "one_time_decision_used")
		assert.NotContains(t, result, "claim_available")
	}
}

func TestAssistantGiftStatusQueriesOverrideOldApplicationAndCannotPrepare(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantNewUserGift{}))
	for _, message := range []string{
		"我领取了吗", "当前有没有礼包", "还有礼包吗", "新用户礼包当前状态", "我的新用户礼包领取成功了吗", "我领过新手礼包吗",
		"礼包规则", "新用户福利还差什么条件", "当前对话算实质交流吗", "我还符合新用户礼包资格吗", "怎么领取新用户礼包", "新用户礼包", "我可以申请新用户礼包吗", "帮我查询“新用户礼包”的状态",
		"我申请的新用户礼包审核通过了吗", "我的新用户礼包申请结果出来了吗", "新用户礼包申请成功了吗", "我需要申请新用户福利吗",
		"我的新用户礼包被评估了吗", "我的新用户礼包已经决定了吗", "我的新用户礼包被评估过吗", "Has my welcome gift been evaluated?", "Has my welcome gift been decided?",
		"Was my welcome gift decided?", "Was my welcome gift evaluated?", "我的新用户礼包是否评估", "新用户礼包你评估吗",
		"Have I claimed the welcome gift?", "What is my new-user gift status?", "How do I claim the new user gift?", "What are the welcome gift rules?", `Check the status of my "welcome gift"`,
	} {
		t.Run(message, func(t *testing.T) {
			context := assistantUserContext{AccessLevel: "L0", CustomerProfile: assistantProfileNormal, LatestUserRequest: message, NewUserGiftRequested: true}
			assert.True(t, assistantNewUserGiftStatusRequest(message))
			assert.False(t, assistantNewUserGiftRequest(message))
			assert.False(t, assistantNewUserGiftWorkflowRequired(context))
			assert.False(t, assistantNewUserGiftToolAllowed(context))
			assert.Equal(t, []string{"get_new_user_gift_status"}, assistantReadChain(context))
			assert.Equal(t, "get_new_user_gift_status", assistantNamedToolChoiceName(assistantToolChoiceForContext(context)))
			assert.Equal(t, "get_new_user_gift_status", assistantNamedToolChoiceName(assistantToolChoiceForAgentStep(context, map[string]bool{}, map[string]bool{})))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(assistantActorUserIDKey, 123)
			c.Set(assistantUserContextKey, context)
			result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "prepare_new_user_gift", Arguments: `{"amount_cents":0,"reason":"An old legitimate workflow should not authorize a current status inquiry."}`}})
			assert.Equal(t, "read_only_request", result["status"])
			assert.Equal(t, false, result["mutation_attempted"])
			assert.Contains(t, result["next_step"], "get_new_user_gift_status")
			assert.Equal(t, "read_only_request", executeAssistantNewUserGiftTool(c, 123, nil)["status"], "direct executor must keep the same guard")
		})
	}
	var count int64
	require.NoError(t, db.Model(&model.AssistantNewUserGift{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAssistantGiftApplicationsStillAllowOneTimeDecision(t *testing.T) {
	for _, message := range []string{"申请新人福利", "我想领取新手福利", "我想申请新用户福利", "领取新用户礼包", "请评估我的新用户礼包", "请按礼包规则帮我评估一下", "请按照新用户礼包规则给我评估额度", "Please evaluate my welcome gift", "I want to claim my welcome gift", "Claim my welcome gift", "那我作为一个新用户，你希望给我多少额度"} {
		context := assistantUserContext{AccessLevel: "L1", DeveloperAccessGranted: true, LatestUserRequest: message}
		assert.False(t, assistantNewUserGiftStatusRequest(message), message)
		assert.True(t, assistantNewUserGiftWorkflowRequired(context), message)
	}
}

func TestAssistantGiftStatusToolCatalogueIncludesAllRolesWithoutGiftPermission(t *testing.T) {
	for _, context := range []assistantUserContext{
		{AccessLevel: "L0"}, {AccessLevel: "L1", DeveloperAccessGranted: true},
		{AccessLevel: "ADMIN", AdministratorMode: true}, {AccessLevel: "ROOT", AdministratorMode: true},
		{AccessLevel: "L0", GiftRewardBlocked: true, CustomerProfile: assistantProfileSecurityRisk},
	} {
		names := map[string]bool{}
		for _, tool := range assistantToolDefinitionsForContext(context) {
			names[tool.Function.Name] = true
		}
		assert.True(t, names["get_new_user_gift_status"], context.AccessLevel)
		assert.Equal(t, assistantNewUserGiftToolAllowed(context), names["prepare_new_user_gift"])
	}
	context := assistantUserContext{AccessLevel: "L0", LatestUserRequest: "礼包规则"}
	for _, tool := range assistantToolDefinitionsForContext(context) {
		assert.NotEqual(t, "prepare_new_user_gift", tool.Function.Name)
	}
	assert.True(t, assistantServerReadFallbackAllowed("get_new_user_gift_status"))
}

func TestPrepareAssistantRequestGiftStatusBypassesCachedAnswer(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantLead{}, &model.AssistantProfileBucket{}, &model.AssistantFirstQuestionStat{}))
	withAssistantSettings(t, true, "assistant-gift-status-cache-bypass-model")
	original := setting.GetAssistantSettings()
	setting.SetAssistantCacheEnabled(true)
	require.NoError(t, setting.UpdateAssistantCacheTTLMinutes("10"))
	t.Cleanup(func() {
		setting.SetAssistantCacheEnabled(original.CacheEnabled)
		_ = setting.UpdateAssistantCacheTTLMinutes(strconv.Itoa(original.CacheTTLMinutes))
	})
	message := "我领取了吗"
	settings := setting.GetAssistantSettings()
	context := assistantUserContextForRequest(42, message)
	key := assistantCacheKey(settings, []assistantOpenAIMessage{{Role: "user", Content: message}}, context)
	require.NotEmpty(t, key)
	storeAssistantCachedResponse(settings, key, http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"stale unclaimed gift answer"}}]}`))
	calls := 0
	engine := gin.New()
	engine.POST("/api/assistant/chat", func(c *gin.Context) {
		c.Set("id", 42)
		PrepareAssistantRequest(c)
	}, func(c *gin.Context) {
		calls++
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/api/assistant/chat", strings.NewReader(`{"message":"`+message+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Empty(t, response.Header().Get("X-LMM-Assistant-Cache"))
	assert.Equal(t, 1, calls)
	assert.NotContains(t, response.Body.String(), "stale unclaimed gift answer")
}

func TestAssistantGiftStatusOnlyQuestionDoesNotCreatePendingApplication(t *testing.T) {
	conversation := []assistantOpenAIMessage{{Role: "user", Content: "新用户礼包领过了吗"}, {Role: "assistant", Content: "还没有记录"}, {Role: "user", Content: "我会用来写代码和 API 调试"}}
	assert.False(t, assistantConversationHasNewUserGiftRequest(conversation))
	context := assistantUserContextForRequest(0, conversation[len(conversation)-1].Content, conversation)
	assert.False(t, context.NewUserGiftRequested)
	assert.False(t, assistantNewUserGiftWorkflowRequired(context))
}

func TestAssistantRewardReadOnlyFollowUpsUseMostRecentTopic(t *testing.T) {
	for _, test := range []struct {
		name, topic, question, expected string
	}{
		{name: "gift missing conditions", topic: "我想申请新用户礼包", question: "还差什么条件", expected: "gift"},
		{name: "gift then L1 conditions", topic: "我想升级到 L1", question: "还差什么条件", expected: "other"},
		{name: "gift then client setup conditions", topic: "怎么配置客户端", question: "还差什么条件", expected: "other"},
		{name: "gift then weekly claim status", topic: "我想申请每周折扣", question: "我领取了吗", expected: "weekly_discount"},
		{name: "gift then referral claim status", topic: "我的邀请奖励领取成功了吗", question: "我领取了吗", expected: "other"},
		{name: "gift then checkin claim status", topic: "每日签到奖励领取成功了吗", question: "我领取了吗", expected: "other"},
	} {
		t.Run(test.name, func(t *testing.T) {
			conversation := []assistantOpenAIMessage{{Role: "user", Content: "我想申请新用户礼包"}, {Role: "assistant", Content: "请说明用途"}, {Role: "user", Content: test.topic}, {Role: "assistant", Content: "收到"}, {Role: "user", Content: test.question}}
			context := assistantUserContextForRequest(0, test.question, conversation)
			assert.Equal(t, test.expected, context.RewardTopic)
			assert.Equal(t, test.expected == "gift", assistantNewUserGiftStatusWorkflowRequired(context))
			assert.Equal(t, test.expected == "weekly_discount", assistantWeeklyDiscountStatusWorkflowRequired(context))
			assert.False(t, assistantNewUserGiftWorkflowRequired(context))
			assert.False(t, assistantWeeklyDiscountWorkflowRequired(context))
			if test.expected == "gift" {
				assert.Equal(t, []string{"get_new_user_gift_status"}, assistantReadChain(context))
			} else if test.expected == "weekly_discount" {
				assert.Equal(t, []string{"get_weekly_discount_status"}, assistantReadChain(context))
			} else {
				assert.NotContains(t, assistantReadChain(context), "get_new_user_gift_status")
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set(assistantUserContextKey, context)
			c.Set(assistantPolicyConversationKey, conversation)
			for _, prepare := range []string{"prepare_new_user_gift", "prepare_weekly_discount"} {
				result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: prepare, Arguments: "{}"}})
				assert.Equal(t, "read_only_request", result["status"])
				assert.Equal(t, false, result["mutation_attempted"])
			}
		})
	}
}

func TestAssistantWeeklyQueriesCannotResumeOldGiftDecision(t *testing.T) {
	for _, message := range []string{"我上周的折扣码领取成功了吗", "Have I claimed the weekly coupon?", "本周折扣规则是什么", "当前周折扣状态"} {
		context := assistantUserContext{LatestUserRequest: message, NewUserGiftRequested: true, WeeklyDiscountRequested: true, RewardTopic: "weekly_discount"}
		assert.False(t, assistantNewUserGiftStatusWorkflowRequired(context), message)
		assert.False(t, assistantNewUserGiftWorkflowRequired(context), message)
		assert.False(t, assistantWeeklyDiscountWorkflowRequired(context), message)
		assert.True(t, assistantWeeklyDiscountStatusWorkflowRequired(context), message)
		assert.Equal(t, []string{"get_weekly_discount_status"}, assistantReadChain(context), message)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set(assistantUserContextKey, context)
		assert.Equal(t, "read_only_request", executeAssistantWeeklyDiscountTool(c, 123, nil)["status"])
		assert.Equal(t, "read_only_request", executeAssistantNewUserGiftTool(c, 123, nil)["status"])
	}
}

func TestAssistantRewardCompletionQuestionsCannotAuthorizeAnotherDecision(t *testing.T) {
	for _, reward := range []struct {
		name, english, topic, statusTool string
	}{
		{name: "新用户礼包", english: "welcome gift", topic: "gift", statusTool: "get_new_user_gift_status"},
		{name: "每周折扣", english: "weekly discount", topic: "weekly_discount", statusTool: "get_weekly_discount_status"},
	} {
		for _, message := range []string{
			"你有没有帮我评估" + reward.name + "？",
			"你是不是已经帮我评估" + reward.name + "了？",
			"你是否已经帮我决定" + reward.name + "额度？",
			"你帮我评估" + reward.name + "了没？",
			"你帮我评估了" + reward.name + "吗？",
			"你给我评估了" + reward.name + "吗？",
			"你帮我评估了" + reward.name + "没有？",
			"你评估" + reward.name + "吗？",
			"您评估" + reward.name + "没有？",
			"你帮我决定了" + reward.name + "的额度吗？谢谢",
			"You evaluate my " + reward.english + "?",
			"Have you helped me evaluate my " + reward.english + "?",
			"Did you evaluate my " + reward.english + "?",
		} {
			t.Run(message, func(t *testing.T) {
				context := assistantUserContext{LatestUserRequest: message, RewardTopic: reward.topic, NewUserGiftRequested: true, WeeklyDiscountRequested: true}
				assert.True(t, assistantRewardDecisionCompletionQuestion(message))
				assert.False(t, assistantExplicitGiftDecisionRequest(message))
				assert.False(t, assistantNewUserGiftWorkflowRequired(context))
				assert.False(t, assistantWeeklyDiscountWorkflowRequired(context))
				assert.Equal(t, []string{reward.statusTool}, assistantReadChain(context))
				assert.Equal(t, reward.statusTool, assistantNamedToolChoiceName(assistantToolChoiceForContext(context)))
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set(assistantUserContextKey, context)
				for _, prepare := range []string{"prepare_new_user_gift", "prepare_weekly_discount"} {
					result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: prepare, Arguments: "{}"}})
					assert.Equal(t, "read_only_request", result["status"])
					assert.Equal(t, false, result["mutation_attempted"])
				}
				assert.Equal(t, "read_only_request", executeAssistantNewUserGiftTool(c, 123, nil)["status"])
				assert.Equal(t, "read_only_request", executeAssistantWeeklyDiscountTool(c, 123, nil)["status"])
			})
		}
	}
	for _, message := range []string{
		"请帮我评估是否符合新用户礼包条件", "请帮我评估是否符合每周折扣条件",
		"请你帮我评估是否符合新用户礼包条件", "请你帮我评估是否符合每周折扣条件",
		"Please evaluate whether I qualify for my welcome gift", "Please evaluate whether I qualify for my weekly discount",
	} {
		t.Run(message, func(t *testing.T) {
			assert.False(t, assistantRewardDecisionCompletionQuestion(message))
			assert.True(t, assistantExplicitGiftDecisionRequest(message))
			context := assistantUserContextForRequest(0, message)
			assert.False(t, assistantNewUserGiftStatusWorkflowRequired(context))
			assert.False(t, assistantWeeklyDiscountStatusWorkflowRequired(context))
			assert.True(t, assistantNewUserGiftWorkflowRequired(context) || assistantWeeklyDiscountWorkflowRequired(context))
		})
	}
}
