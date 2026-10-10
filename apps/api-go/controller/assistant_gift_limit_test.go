package controller

import (
	"math"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func assistantCapControllerFixture(t *testing.T) (*gorm.DB, *gin.Context, model.User) {
	t.Helper()
	setupAssistantCurrencyTest(t)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(model.RegistrationGuardMigrationModels()...))
	require.NoError(t, db.AutoMigrate(&model.AssistantNewUserGift{}, &model.AssistantGiftRiskKey{}, &model.AssistantGiftRiskMemory{}, &model.TopUp{}))
	level := model.TrustLevelMinUser
	user := model.User{Username: "cap-controller", Email: "cap-controller@example.com", AffCode: "cap-controller", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, CreatedAt: time.Now().Add(-24 * time.Hour).Unix(), TrustLevelOverride: &level}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, model.ObserveAssistantRegistration(user.Id, "198.51.100.10", ""))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/api/assistant/chat", nil)
	c.Request.RemoteAddr = "198.51.100.10:1234"
	c.Set("assistant_conversation", []assistantOpenAIMessage{{Role: "user", Content: "Please evaluate my welcome gift. I will use this for monitoring crawler jobs and implementing company backend modules from UI prototypes, designs and coding standards."}})
	return db, c, user
}

func setControllerGiftCap(t *testing.T, db *gorm.DB, value int) {
	t.Helper()
	require.NoError(t, db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&model.Option{Key: setting.AssistantNewUserGiftMaxCreditsOptionKey, Value: strconv.Itoa(value)}).Error)
}

func TestAssistantGiftToolSchemaAndPromptTrackChangedDurableCap(t *testing.T) {
	db, _, user := assistantCapControllerFixture(t)
	context := assistantUserContext{UserID: user.Id, AccessLevel: "L1", RewardTopic: "gift", NewUserGiftRequested: true}
	for _, cap := range []int{100000, 9000000, 0, 500000} {
		setControllerGiftCap(t, db, cap)
		found := false
		for _, tool := range assistantToolDefinitionsForContext(context) {
			if tool.Function.Name != "prepare_new_user_gift" {
				continue
			}
			found = true
			properties := tool.Function.Parameters["properties"].(map[string]any)
			amount := properties["amount_credits"].(map[string]any)
			require.Equal(t, cap, amount["exclusiveMaximum"])
			require.NotContains(t, properties, "amount_cents")
		}
		require.True(t, found)
		prompt := buildAssistantSystemPrompt(setting.GetAssistantSettings(), context)
		require.Contains(t, prompt, "Current welcome-gift maximum: "+strconv.Itoa(cap)+" integer wallet credits")
		require.NotContains(t, prompt, "0 to 1000 LEGACY_CENTS")
	}
}

func TestAssistantGiftToolRejectsOverCapInvalidNumbersAndMixedUnits(t *testing.T) {
	db, c, user := assistantCapControllerFixture(t)
	setControllerGiftCap(t, db, 100000)
	for _, input := range []map[string]any{
		{"amount_credits": 100001.0, "reason": "A concrete legitimate backend development workflow."},
		{"amount_credits": 100000.0, "reason": "The exclusive upper bound must be rejected."},
		{"amount_credits": math.NaN()}, {"amount_credits": math.Inf(1)}, {"amount_credits": -1.0}, {"amount_credits": 1.5}, {"amount_credits": float64(common.MaxWalletQuota) + 1},
		{"amount_credits": 1.0, "amount_cents": 1.0}, {"amount_credits": 1.0, "amount_unit": "USD"},
		{"amount_cents": 1000.0, "reason": "A concrete legitimate backend development workflow."},
	} {
		result := executeAssistantNewUserGiftTool(c, user.Id, input)
		require.Equal(t, false, result["ok"])
		var count int64
		require.NoError(t, db.Model(&model.AssistantNewUserGift{}).Count(&count).Error)
		require.Zero(t, count)
	}
	result := executeAssistantNewUserGiftTool(c, user.Id, map[string]any{"amount_credits": 99999.0, "reason": "A concrete legitimate backend development workflow."})
	require.Equal(t, true, result["ok"])
	require.Equal(t, model.AssistantGiftClaimed, result["status"])
	require.Equal(t, 99999, result["credit_amount"])
	require.Equal(t, 100000, result["max_credit_amount"])
	var account model.User
	require.NoError(t, db.First(&account, user.Id).Error)
	require.Equal(t, 99999, account.Quota)
}

func TestAssistantGiftStatusDoesNotPromiseClaimAfterCapIsLowered(t *testing.T) {
	db, c, user := assistantCapControllerFixture(t)
	setControllerGiftCap(t, db, 9000000)
	_, _, err := model.DecideAssistantNewUserGiftCredits(user.Id, 1, 7000000, "A concrete legitimate backend development workflow.", 1, 80, "198.51.100.10")
	require.NoError(t, err)
	for _, cap := range []int{100000, 0} {
		setControllerGiftCap(t, db, cap)
		result := executeAssistantNewUserGiftStatusTool(c, user.Id)
		require.Equal(t, true, result["ok"])
		require.Equal(t, false, result["claim_available"])
		require.Equal(t, 7000000, result["credit_amount"])
		require.Equal(t, cap, result["max_credit_amount"])
		_, offered := c.Get(assistantClientActionKey)
		require.False(t, offered)
		gift, err := model.GetAssistantNewUserGift(user.Id)
		require.NoError(t, err)
		dto, err := assistantGiftResponse(gift)
		require.NoError(t, err)
		require.False(t, dto.ClaimAvailable)
	}
}

func TestAssistantGiftCapAdminPreviewUsesSameCreditValidator(t *testing.T) {
	for _, bad := range []string{"-1", "0.5", "NaN", "9007199254740992"} {
		require.Error(t, validateAssistantAdminConfigValue(setting.AssistantNewUserGiftMaxCreditsOptionKey, bad))
	}
	for _, good := range []string{"0", "1", "9000000"} {
		require.NoError(t, validateAssistantAdminConfigValue(setting.AssistantNewUserGiftMaxCreditsOptionKey, good))
	}
}
