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
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	setupTokenControllerTestDB(t)
}

func TestAssistantCurrencyWalletAndGiftUsePersistedCreditAmounts(t *testing.T) {
	setupAssistantCurrencyTest(t)
	gift := &model.AssistantNewUserGift{AmountCents: 999, Quota: 3500000, Status: model.AssistantGiftOffered}
	for _, fx := range []float64{7, 7.2, 8} {
		operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = fx, 99
		fields := assistantWalletBalanceFields(3500000)
		assert.Equal(t, float64(7), fields["wallet_balance_usd"])
		assert.Equal(t, float64(500000), fields["credits_per_usd"])
		dto, err := assistantGiftResponse(gift)
		require.NoError(t, err)
		assert.Equal(t, "LEGACY_CENTS", dto.AmountUnit)
		assert.Equal(t, 999, dto.AmountCents)
		assert.Equal(t, 3500000, dto.CreditAmount)
		require.NotNil(t, dto.AmountUSD)
		assert.Equal(t, float64(7), *dto.AmountUSD)
		encoded, err := json.Marshal(dto)
		require.NoError(t, err)
		assert.Contains(t, string(encoded), `"amount_cents":999`)
		assert.Contains(t, string(encoded), `"credit_amount":3500000`)
		assert.Contains(t, string(encoded), `"amount_usd":7`)
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
		assert.Equal(t, float64(7), result["pending_reward_usd"])
		assert.Equal(t, float64(14), result["total_reward_usd"])
		assert.Equal(t, float64(3.5), result["reward_per_inviter_usd"])
		assert.Equal(t, float64(7), result["reward_per_invitee_usd"])
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

func TestAssistantCurrencyUnrepresentableProjectionKeepsJSONFinite(t *testing.T) {
	setupAssistantCurrencyTest(t)
	db := setupAssistantAccountProgressDB(t)
	user := assistantAccountProgressUser(t, db, true)
	oldGetter := drawingUserQuota
	oldInviter, oldInvitee := common.QuotaForInviter, common.QuotaForInvitee
	t.Cleanup(func() {
		drawingUserQuota = oldGetter
		common.QuotaForInviter, common.QuotaForInvitee = oldInviter, oldInvitee
	})
	for _, tc := range []struct {
		anchor  string
		credits int
	}{
		{anchor: "1e-500", credits: 1},
		{anchor: "1e-300", credits: 9007199254740991},
	} {
		t.Run(tc.anchor, func(t *testing.T) {
			require.NoError(t, common.SetCreditCurrencyBasis(decimal.RequireFromString(tc.anchor), decimal.NewFromInt(500000)))
			wallet := assistantWalletBalanceFields(tc.credits)
			assert.Equal(t, "unavailable", wallet["wallet_balance_status"])
			assert.Nil(t, wallet["wallet_balance_usd"])
			assert.Nil(t, wallet["credits_per_usd"])
			_, err := json.Marshal(wallet)
			require.NoError(t, err)
			gift := &model.AssistantNewUserGift{AmountCents: 525, Quota: tc.credits, Status: model.AssistantGiftOffered}
			dto, err := assistantGiftResponse(gift)
			require.NoError(t, err)
			assert.Equal(t, tc.credits, dto.CreditAmount)
			assert.Nil(t, dto.AmountUSD)
			assert.Nil(t, dto.CreditsPerUSD)
			encoded, err := json.Marshal(dto)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), `"amount_usd":null`)
			assert.Contains(t, string(encoded), `"credits_per_usd":null`)
			require.NoError(t, db.Model(user).Updates(map[string]any{"aff_quota": tc.credits, "aff_history": tc.credits}).Error)
			common.QuotaForInviter, common.QuotaForInvitee = tc.credits, tc.credits
			rewards := executeAssistantInvitationTool(user.Id)
			require.Equal(t, true, rewards["ok"])
			assert.Equal(t, tc.credits, rewards["pending_reward_credit"])
			for _, field := range []string{"pending_reward_usd", "total_reward_usd", "reward_per_inviter_usd", "reward_per_invitee_usd"} {
				assert.Nil(t, rewards[field])
			}
			_, err = json.Marshal(rewards)
			require.NoError(t, err)
			drawingUserQuota = func(int, bool) (int, error) { return tc.credits, nil }
			access := drawingWebAccessForUser(user.Id)
			assert.False(t, access.Allowed)
			assert.Nil(t, access.BalanceUSD)
			_, err = json.Marshal(access)
			require.NoError(t, err)
			c, w := newAuthenticatedContext(t, http.MethodPost, "/pg/images/generations", nil, user.Id)
			assert.False(t, requireDrawingWebBalance(c, user.Id))
			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
			assert.Contains(t, w.Body.String(), `"code":"WEB_DRAWING_BALANCE_UNAVAILABLE"`)
		})
	}
}

func TestAssistantCurrencyNormalZeroRemainsExplicitZero(t *testing.T) {
	setupAssistantCurrencyTest(t)
	fields := assistantWalletBalanceFields(0)
	assert.Equal(t, "available", fields["wallet_balance_status"])
	assert.Equal(t, float64(0), fields["wallet_balance_usd"])
	dto, err := assistantGiftResponse(&model.AssistantNewUserGift{Status: model.AssistantGiftDeclined})
	require.NoError(t, err)
	require.NotNil(t, dto.AmountUSD)
	assert.Equal(t, float64(0), *dto.AmountUSD)
	oldGetter := drawingUserQuota
	t.Cleanup(func() { drawingUserQuota = oldGetter })
	drawingUserQuota = func(int, bool) (int, error) { return 0, nil }
	access := drawingWebAccessForUser(17)
	require.NotNil(t, access.BalanceUSD)
	assert.Equal(t, float64(0), *access.BalanceUSD)
	assert.False(t, access.Allowed)
}
