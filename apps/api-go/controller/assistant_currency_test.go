package controller

import (
	"encoding/json"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// Install only for tests that exercise assistant money. Restore both the
// immutable denomination and mutable configuration; never seed the package.
func setupAssistantCurrencyTest(t *testing.T) {
	t.Helper()
	oldK, oldKErr := common.CreditsPerUSD()
	oldQ, oldQErr := common.LegacyPricingQuotaPerUnit()
	oldRuntimeQ := common.QuotaPerUnit
	oldFX, oldBonus := operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	t.Cleanup(func() {
		common.QuotaPerUnit = oldRuntimeQ
		operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = oldFX, oldBonus
		if oldKErr != nil || oldQErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(oldK, oldQ))
		}
	})
	common.QuotaPerUnit = 500000
	operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 7, 1
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
}

func TestAssistantCurrencyWalletAndGiftUsePersistedCreditAmounts(t *testing.T) {
	setupAssistantCurrencyTest(t)
	gift := &model.AssistantNewUserGift{AmountCents: 999, Quota: 3500000, Status: model.AssistantGiftOffered}
	for _, fx := range []float64{7, 7.2, 8} {
		operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = fx, 99
		fields := assistantWalletBalanceFields(3500000)
		assert.Equal(t, float64(1), fields["wallet_balance_usd"])
		assert.Equal(t, float64(3500000), fields["credits_per_usd"])
		dto, err := assistantGiftResponse(gift)
		require.NoError(t, err)
		assert.Equal(t, "LEGACY_CENTS", dto.AmountUnit)
		assert.Equal(t, 999, dto.AmountCents)
		assert.Equal(t, 3500000, dto.CreditAmount)
		assert.Equal(t, float64(1), dto.AmountUSD)
		encoded, err := json.Marshal(dto)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"amount_cents":999`)
		assert.Contains(t, string(encoded), `"credit_amount":3500000`)
		assert.Contains(t, string(encoded), `"amount_usd":1`)
	}
	common.ClearCreditsPerUSD()
	_, err := assistantGiftResponse(gift)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
}

func TestAssistantCurrencyMissingBasisBlocksGiftReadsAndClaimBeforeWrites(t *testing.T) {
	setupAssistantCurrencyTest(t)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantNewUserGift{}))
	gift := model.AssistantNewUserGift{UserId: 11, AmountCents: 525, Quota: 2625000, Status: model.AssistantGiftOffered}
	require.NoError(t, db.Create(&gift).Error)
	common.ClearCreditsPerUSD()
	result := executeAssistantNewUserGiftStatusTool(nil, 11)
	assert.Equal(t, false, result["ok"])
	assert.Equal(t, "unavailable", result["status"])
	assert.NotContains(t, result, "amount_usd")
	c, w := newAuthenticatedContext(t, http.MethodPost, "/api/assistant/new-user-gift/claim", nil, 11)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/new-user-gift/claim", nil)
	ClaimAssistantNewUserGift(c)
	assert.Contains(t, w.Body.String(), `"success":false`)
	stored, err := model.GetAssistantNewUserGift(11)
	require.NoError(t, err)
	assert.Equal(t, gift, *stored)
}

func TestAssistantInvitationRewardsUseActualUSDWithoutChangingCreditRewards(t *testing.T) {
	setupAssistantCurrencyTest(t)
	db := setupAssistantAccountProgressDB(t)
	user := assistantAccountProgressUser(t, db, true)
	require.NoError(t, db.Model(user).Updates(map[string]any{"aff_quota": 3500000, "aff_history": 7000000}).Error)
	oldInviter, oldInvitee := common.QuotaForInviter, common.QuotaForInvitee
	t.Cleanup(func() { common.QuotaForInviter, common.QuotaForInvitee = oldInviter, oldInvitee })
	common.QuotaForInviter, common.QuotaForInvitee = 1750000, 3500000
	for _, fx := range []float64{7, 8} {
		operation_setting.USDExchangeRate = fx
		result := executeAssistantInvitationTool(user.Id)
		require.Equal(t, true, result["ok"])
		assert.Equal(t, 3500000, result["pending_reward_credit"])
		assert.Equal(t, 7000000, result["total_reward_credit"])
		assert.Equal(t, float64(1), result["pending_reward_usd"])
		assert.Equal(t, float64(2), result["total_reward_usd"])
		assert.Equal(t, float64(0.5), result["reward_per_inviter_usd"])
		assert.Equal(t, float64(1), result["reward_per_invitee_usd"])
	}
	common.ClearCreditsPerUSD()
	result := executeAssistantInvitationTool(user.Id)
	assert.Equal(t, false, result["ok"])
	assert.Equal(t, "unavailable", result["status"])
	assert.NotContains(t, result, "pending_reward_usd")
}

func TestAssistantGiftLegacyCentsCannotBeRelabelledAsUSCents(t *testing.T) {
	result := executeAssistantNewUserGiftTool(nil, 7, map[string]any{"amount_cents": 525, "amount_unit": "USD"})
	assert.Equal(t, false, result["ok"])
	assert.Equal(t, "invalid_decision", result["status"])
}
