package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func trustConfigurationFixture() TrustLevelConfiguration {
	config := legacyTrustLevelConfiguration()
	for level, credits := range []int64{0, 500001, 1000001, 1500001, 2000001} {
		config.Tiers[level].MinPaidCredits = credits
	}
	config.PaidActivationEnabled, config.DecayPeriodDays = true, 0
	config.Tiers[2].DiscountRatio = 0.88
	return config
}

func installTrustConfiguration(t *testing.T, config TrustLevelConfiguration) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	old, existed := common.OptionMap[TrustLevelBenefitsOptionKey]
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	raw, err := json.Marshal(config)
	require.NoError(t, err)
	common.OptionMap[TrustLevelBenefitsOptionKey] = string(raw)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap[TrustLevelBenefitsOptionKey] = old
		} else {
			delete(common.OptionMap, TrustLevelBenefitsOptionKey)
		}
	})
}

func TestTrustConfigurationRejectsRoleThresholdsAndUnsafeValues(t *testing.T) {
	valid := trustConfigurationFixture()
	for name, change := range map[string]func(*TrustLevelConfiguration){
		"role tier":           func(c *TrustLevelConfiguration) { c.Tiers = append(c.Tiers, TrustLevelConfigurationTier{Level: 5}) },
		"duplicate threshold": func(c *TrustLevelConfiguration) { c.Tiers[2].MinPaidCredits = c.Tiers[1].MinPaidCredits },
		"unsafe credits":      func(c *TrustLevelConfiguration) { c.Tiers[4].MinPaidCredits = common.MaxWalletQuota + 1 },
		"negative ratio":      func(c *TrustLevelConfiguration) { c.Tiers[1].DiscountRatio = -1 },
		"role privilege":      func(c *TrustLevelConfiguration) { c.Tiers[1].Benefits = []string{"administrator_access"} },
		"duplicate benefit":   func(c *TrustLevelConfiguration) { c.Tiers[2].Benefits = []string{"usage_discount", "usage_discount"} },
		"invalid decay":       func(c *TrustLevelConfiguration) { c.DecayPeriodDays = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(valid)
			var candidate TrustLevelConfiguration
			require.NoError(t, json.Unmarshal(raw, &candidate))
			change(&candidate)
			raw, _ = json.Marshal(candidate)
			require.Error(t, ValidateOptionValue(TrustLevelBenefitsOptionKey, string(raw)))
		})
	}
	raw, _ := json.Marshal(valid)
	require.NoError(t, ValidateOptionValue(TrustLevelBenefitsOptionKey, string(raw)))
	require.Error(t, ValidateOptionValue(TrustLevelBenefitsOptionKey, string(raw)+" {}"))
	require.Error(t, ValidateOptionValue(TrustLevelBenefitsOptionKey, `{"version":1,"role":100}`))
}

func TestTrustConfigurationPersistenceAndImmediateCreditBoundary(t *testing.T) {
	setupPriceLockTest(t)
	config := trustConfigurationFixture()
	raw, _ := json.Marshal(config)
	require.NoError(t, UpdateOption(TrustLevelBenefitsOptionKey, string(raw)))
	require.JSONEq(t, string(raw), persistedPriceOption(t, TrustLevelBenefitsOptionKey))
	now := time.Now().Unix()
	for _, tc := range []struct {
		credits   int64
		activated bool
		level     int
	}{
		{500000, false, 0}, {500001, true, 1}, {1000000, true, 1}, {1000001, true, 2}, {2000001, true, 4},
	} {
		info := evaluateTrustLevelCredits(common.RoleCommonUser, nil, tc.credits, float64(tc.credits)/500000, tc.activated, now-400*86400, now, GetTrustLevelConfiguration())
		require.Equal(t, tc.level, info.Level)
		require.LessOrEqual(t, info.AutomaticLevel, 4)
	}
	info := evaluateTrustLevelCredits(common.RoleCommonUser, nil, 1000001, 0, true, now, now, GetTrustLevelConfiguration())
	require.Equal(t, 0.88, info.DiscountRatio)
	require.Equal(t, "1000001", *info.PaidCredits)
	require.Equal(t, "500000", *info.CreditsToNextLevel)
	config.Tiers[2].MinPaidCredits = 1100001
	raw, _ = json.Marshal(config)
	require.NoError(t, UpdateOption(TrustLevelBenefitsOptionKey, string(raw)))
	info = evaluateTrustLevelCredits(common.RoleCommonUser, nil, 1000001, 0, true, now, now, GetTrustLevelConfiguration())
	require.Equal(t, 1, info.AutomaticLevel, "current configuration must immediately rejudge the same paid credits")
	invalid := config
	invalid.Tiers[2].MinPaidCredits = 1
	raw, _ = json.Marshal(invalid)
	require.Error(t, UpdateOption(TrustLevelBenefitsOptionKey, string(raw)))
	require.Equal(t, int64(1100001), GetTrustLevelConfiguration().Tiers[2].MinPaidCredits, "invalid writes must not publish or persist")
}

func TestTrustConfigurationRoleLevelsHaveNoPaidProgress(t *testing.T) {
	config := trustConfigurationFixture()
	config.Tiers[4].DiscountRatio = 0.5
	config.RoleTiers[0].DiscountRatio = 0.75
	config.RoleTiers[1].DiscountRatio = 0.8
	for _, tc := range []struct{ role, level int }{{common.RoleAdminUser, 5}, {common.RoleRootUser, 6}} {
		for _, credits := range []int64{0, common.MaxWalletQuota} {
			info := evaluateTrustLevelCredits(tc.role, nil, credits, 0, true, 1, time.Now().Unix(), config)
			require.Equal(t, tc.level, info.Level)
			require.Equal(t, "role", info.LevelSource)
			if tc.level == 5 {
				require.Equal(t, 0.75, info.DiscountRatio)
			} else {
				require.Equal(t, 0.8, info.DiscountRatio)
			}
			require.LessOrEqual(t, info.AutomaticLevel, 4)
			require.Nil(t, info.NextLevelPaidCredits)
			require.Nil(t, info.CreditsToNextLevel)
			require.Nil(t, info.NextLevel)
		}
	}
	for _, tier := range GetTrustLevelRoleTiers() {
		raw, _ := json.Marshal(tier)
		require.NotContains(t, string(raw), "min_paid")
		require.True(t, tier.RoleOnly)
	}
}

func TestTrustConfigurationAggregateUsesCreditsAndPreservesStoredHistory(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	installTrustConfiguration(t, trustConfigurationFixture())
	const userID = 9371
	rows := []TopUp{
		{UserId: userID, TradeNo: "real-one", CreditedQuota: 500000, Amount: 9999, Money: 9999, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe},
		{UserId: userID, TradeNo: "one-credit", CreditedQuota: 1, Money: 0.01, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderCreem},
		{UserId: userID, TradeNo: "pending", CreditedQuota: 9000000, Money: 100, Status: common.TopUpStatusPending, PaymentProvider: PaymentProviderStripe},
		{UserId: userID, TradeNo: "balance", CreditedQuota: 9000000, Money: 100, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderBalance, PaymentMethod: PaymentMethodBalance},
	}
	require.NoError(t, db.Create(&rows).Error)
	before := append([]TopUp(nil), rows...)
	aggregate, err := getFreshPaidTopUpAggregate(userID)
	require.NoError(t, err)
	require.EqualValues(t, 500001, aggregate.PaidCredits)
	require.True(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))
	oldFX := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 99.12345
	t.Cleanup(func() { operation_setting.USDExchangeRate = oldFX })
	aggregate, err = getFreshPaidTopUpAggregate(userID)
	require.NoError(t, err)
	require.EqualValues(t, 500001, aggregate.PaidCredits, "display FX cannot alter historical credited points")
	var after []TopUp
	require.NoError(t, db.Order("id").Find(&after).Error)
	require.Equal(t, before, after, "configuration and projections never rewrite old orders")
}
