package controller

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAssistantWeeklyDiscountStatusReadDoesNotConsumeOrModifyDecision(t *testing.T) {
	for _, state := range []string{"none", model.AssistantWeeklyDiscountOffered, model.AssistantWeeklyDiscountClaimed, model.AssistantWeeklyDiscountDeclined} {
		t.Run(state, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.AssistantWeeklyDiscount{}, &model.DiscountCode{}, &model.DiscountCodeReservation{}))
			user := model.User{Username: "weekly-status-" + state, AffCode: "weekly-status-" + state, Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 456}
			require.NoError(t, db.Create(&user).Error)
			weekStart := assistantCurrentUTCWeekStart()
			reward := model.AssistantWeeklyDiscount{UserId: user.Id, WeekStart: weekStart, ConversationId: 987, Status: state, DiscountPercent: 7, Reason: "internal weekly evaluation score and reason must remain private", CreatedAt: 100}
			if state == model.AssistantWeeklyDiscountDeclined {
				reward.DiscountPercent = 0
			}
			if state == model.AssistantWeeklyDiscountClaimed {
				code := model.DiscountCode{Code: "AIWEEK-SECRET-PRIVATE-CODE", OwnerUserID: user.Id, DiscountPercent: 7, MaxUses: 1, CreatedTime: 200, UpdatedTime: 200}
				require.NoError(t, db.Create(&code).Error)
				reward.CodeId, reward.ClaimedAt = code.Id, 200
			}
			if state != "none" {
				require.NoError(t, db.Create(&reward).Error)
			}
			writes := 0
			observeWrite := func(*gorm.DB) { writes++ }
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register("weekly_status_observe_create", observeWrite))
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("weekly_status_observe_update", observeWrite))
			require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("weekly_status_observe_delete", observeWrite))
			for range 2 {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				result := executeAssistantWeeklyDiscountStatusTool(c, user.Id)
				assert.Equal(t, true, result["ok"])
				assert.Equal(t, true, result["read_only"])
				assert.Equal(t, state, result["status"])
				assert.Equal(t, weekStart, result["current_utc_week"])
				assert.Equal(t, state != "none", result["decision_used"])
				assert.Equal(t, state == model.AssistantWeeklyDiscountOffered, result["claim_available"])
				if state == "none" {
					assert.NotContains(t, result, "discount_percent")
					assert.Contains(t, result["next_step"], "does not establish eligibility")
				} else {
					assert.Equal(t, reward.DiscountPercent, result["discount_percent"])
				}
				encoded, err := json.Marshal(result)
				require.NoError(t, err)
				for _, hidden := range []string{"AIWEEK-SECRET", "internal weekly", `"reason"`, `"code"`, "code_id", "conversation_id", "user_id", "quota", "score", "confirmation_token"} {
					assert.NotContains(t, string(encoded), hidden)
				}
				action, hasAction := c.Get(assistantClientActionKey)
				assert.Equal(t, state == model.AssistantWeeklyDiscountOffered, hasAction)
				if hasAction {
					card := action.(map[string]any)
					assert.Equal(t, "weekly_discount", card["type"])
					assert.Equal(t, reward.DiscountPercent, card["discount_percent"])
					assert.Equal(t, state, card["status"])
					assert.NotEqual(t, reward.Reason, card["reason"])
					assert.NotContains(t, card, "code")
				}
			}
			assert.Zero(t, writes, "status reads must never attempt a database write")
			var storedUser model.User
			require.NoError(t, db.First(&storedUser, user.Id).Error)
			assert.Equal(t, user.Quota, storedUser.Quota)
			var rewards []model.AssistantWeeklyDiscount
			require.NoError(t, db.Find(&rewards).Error)
			if state == "none" {
				assert.Empty(t, rewards)
			} else {
				require.Len(t, rewards, 1)
				assert.Equal(t, reward, rewards[0])
			}
			var codeCount int64
			require.NoError(t, db.Model(&model.DiscountCode{}).Count(&codeCount).Error)
			if state == model.AssistantWeeklyDiscountClaimed {
				assert.EqualValues(t, 1, codeCount)
			} else {
				assert.Zero(t, codeCount)
			}
			var reservationCount int64
			require.NoError(t, db.Model(&model.DiscountCodeReservation{}).Count(&reservationCount).Error)
			assert.Zero(t, reservationCount)
		})
	}
}

func TestAssistantWeeklyDiscountStatusReadsOnlyActorAndCurrentUTCWeek(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantWeeklyDiscount{}))
	week := assistantCurrentUTCWeekStart()
	require.NoError(t, db.Create(&model.AssistantWeeklyDiscount{UserId: 11, WeekStart: week, Status: model.AssistantWeeklyDiscountOffered, DiscountPercent: 3, Reason: "actor private reason"}).Error)
	require.NoError(t, db.Create(&model.AssistantWeeklyDiscount{UserId: 22, WeekStart: week, Status: model.AssistantWeeklyDiscountOffered, DiscountPercent: 10, Reason: "other user's private reason"}).Error)
	require.NoError(t, db.Create(&model.AssistantWeeklyDiscount{UserId: 33, WeekStart: week - 7*24*60*60, Status: model.AssistantWeeklyDiscountClaimed, DiscountPercent: 9, Reason: "expired prior-week decision"}).Error)
	require.NoError(t, db.Create(&model.AssistantWeeklyDiscount{UserId: 33, WeekStart: week + 7*24*60*60, Status: model.AssistantWeeklyDiscountOffered, DiscountPercent: 8, Reason: "future-week decision"}).Error)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(assistantActorUserIDKey, 11)
	c.Set("id", 22)
	result := executeAssistantWeeklyDiscountStatusTool(c, 11)
	assert.Equal(t, 3, result["discount_percent"])
	assert.Equal(t, week, result["current_utc_week"])
	result = executeAssistantWeeklyDiscountStatusTool(nil, 33)
	assert.Equal(t, "none", result["status"])
	assert.Equal(t, false, result["decision_used"])
	assert.NotContains(t, result, "discount_percent")
	weekTime := time.Unix(week, 0).UTC()
	assert.Equal(t, time.Monday, weekTime.Weekday())
	assert.Equal(t, 0, weekTime.Hour())
	assert.Equal(t, 0, weekTime.Minute())
	assert.Equal(t, 0, weekTime.Second())
	assert.WithinRange(t, time.Now().UTC(), weekTime, weekTime.AddDate(0, 0, 7))
}

func TestAssistantWeeklyDiscountStatusReadUnavailableDoesNotInferNone(t *testing.T) {
	setupTokenControllerTestDB(t) // No weekly reward table: simulate a failed read.
	for _, userID := range []int{123, 0, -1} {
		result := executeAssistantWeeklyDiscountStatusTool(nil, userID)
		assert.Equal(t, false, result["ok"])
		assert.Equal(t, "unavailable", result["status"])
		assert.Equal(t, true, result["read_only"])
		assert.NotContains(t, result, "decision_used")
		assert.NotContains(t, result, "claim_available")
		assert.NotContains(t, result, "discount_percent")
	}
}

func TestAssistantWeeklyDiscountStatusRejectsUnknownStateAndForeignPrivateCode(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantWeeklyDiscount{}, &model.DiscountCode{}))
	week := assistantCurrentUTCWeekStart()
	foreignCode := model.DiscountCode{Code: "FOREIGN-PRIVATE-CODE", OwnerUserID: 22, DiscountPercent: 10}
	require.NoError(t, db.Create(&foreignCode).Error)
	require.NoError(t, db.Create(&model.AssistantWeeklyDiscount{UserId: 11, WeekStart: week, Status: model.AssistantWeeklyDiscountClaimed, DiscountPercent: 10, CodeId: foreignCode.Id}).Error)
	require.NoError(t, db.Create(&model.AssistantWeeklyDiscount{UserId: 33, WeekStart: week, Status: "unexpected", DiscountPercent: 10}).Error)
	for _, userID := range []int{11, 33} {
		result := executeAssistantWeeklyDiscountStatusTool(nil, userID)
		assert.Equal(t, false, result["ok"])
		assert.Equal(t, "unavailable", result["status"])
		assert.NotContains(t, result, "discount_percent")
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), foreignCode.Code)
	}
}

func TestAssistantWeeklyDiscountStatusClassifiesReadsAndApplications(t *testing.T) {
	for _, text := range []string{
		"每周折扣规则", "每周优惠有什么条件", "本周折扣领取了吗", "我的本周折扣当前状态", "我领过优惠码吗", "本周折扣领取成功了吗", "本周折扣已领取了吗",
		"我申请的本周折扣结果出来了吗", "每周折扣申请成功了吗", "本周折扣评估结果", "本周折扣还有吗", "怎么申请每周折扣", "我可以申请每周折扣吗", "优惠码", "本周折扣",
		"Have I claimed the weekly discount?", "What are the weekly discount rules?", "Check my weekly coupon status", "How do I claim the weekly discount?", "Can I apply for a weekly discount?",
	} {
		assert.True(t, assistantWeeklyDiscountStatusRequest(text), text)
	}
	for _, text := range []string{
		"我想申请每周折扣", "给我优惠码", "请评估我的每周折扣", "请按照每周折扣规则评估一下", "请按照每周折扣规则给我优惠码", "按每周折扣条件帮我申请", "领取本周折扣", "不要解释，帮我申请本周折扣",
		"Please evaluate my weekly discount", "I want to claim my weekly discount", "Give me a discount code", "Apply for a weekly discount",
		"我领取了吗", "当前规则", "新用户礼包规则", "新用户礼包领取了吗", "请评估我的新用户礼包", "不要优惠码", "我不需要每周折扣", "No need for a discount code", "",
	} {
		assert.False(t, assistantWeeklyDiscountStatusRequest(text), text)
	}
}
