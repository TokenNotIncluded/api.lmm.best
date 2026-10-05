package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This fixture represents an initialized historical site. Q is the site's
// existing policy calibration; K is a separate literal seven-times-Q anchor.
func installPaidPolicyCurrencyFixture(t *testing.T, q float64) {
	t.Helper()
	oldK, oldErr := common.CreditsPerUSD()
	oldQ, _ := common.LegacyPricingQuotaPerUnit()
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromFloat(q).Mul(decimal.NewFromInt(7)), decimal.NewFromFloat(q)))
	t.Cleanup(func() {
		if oldErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(oldK, oldQ))
		}
	})
}

func TestPaidPolicyKeepsHistoricalThresholdsAndOrderBytesAfterRateDrift(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}))
	testPaidPolicyHistoricalSite(t, db)
}

func TestPaidPolicyHistoricalSitePostgres(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &TopUp{})
	oldDB := DB
	DB = db
	t.Cleanup(func() { DB = oldDB })
	usePostgresDatabaseType(t)
	testPaidPolicyHistoricalSite(t, db)
}

func testPaidPolicyHistoricalSite(t *testing.T, db *gorm.DB) {
	t.Helper()
	installPaidPolicyCurrencyFixture(t, 500000)
	withDeveloperAccessSetting(t, true, 100)
	oldQ, oldFX, oldB := common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	common.QuotaPerUnit = 500000
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = oldQ, oldFX, oldB
	})
	users := []User{{Username: "snapshot", AffCode: "snapshot"}, {Username: "legacy-stripe", AffCode: "legacy-stripe"}, {Username: "legacy-creem", AffCode: "legacy-creem"}, {Username: "below", AffCode: "below"}}
	for i := range users {
		users[i].Role, users[i].Status = common.RoleCommonUser, common.UserStatusEnabled
		require.NoError(t, db.Create(&users[i]).Error)
	}
	orders := []TopUp{
		{UserId: users[0].Id, CreditedQuota: 50000000, Amount: 100},
		{UserId: users[1].Id, Amount: 100},
		{UserId: users[2].Id, Amount: 50000000, PaymentProvider: PaymentProviderCreem},
		{UserId: users[3].Id, CreditedQuota: 49999999, Amount: 100},
	}
	for i := range orders {
		orders[i].TradeNo = users[i].Username
		orders[i].Money, orders[i].CompleteTime, orders[i].Status = 0.01, time.Now().Unix(), common.TopUpStatusSuccess
		if orders[i].PaymentProvider == "" {
			orders[i].PaymentProvider = PaymentProviderStripe
		}
		require.NoError(t, db.Create(&orders[i]).Error)
	}
	before, err := json.Marshal(orders)
	require.NoError(t, err)
	check := func() {
		t.Helper()
		for i, user := range users {
			fresh, err := GetFreshUserAccessSnapshot(&user)
			require.NoError(t, err)
			cached, err := GetTrustLevelInfoForUserBase(user.ToBaseUser())
			require.NoError(t, err)
			access, err := GetDeveloperAccessStateForUser(&user)
			require.NoError(t, err)
			require.Equal(t, i < 3, access.Granted)
			require.Equal(t, i < 3, fresh.PaidActivationComplete)
			require.Equal(t, fresh.TrustLevel, cached)
			require.Equal(t, LegacyPaidPolicyCurrency, fresh.TrustLevel.PaidAmountCurrency)
			if i < 3 {
				require.Equal(t, 100.0, fresh.TrustLevel.PaidAmount)
				require.EqualValues(t, 100000000, fresh.PaidAmountMicros)
				require.Equal(t, 2, fresh.TrustLevel.Level)
				require.InDelta(t, 100.0/7, *fresh.TrustLevel.PaidAmountUSD, 1e-14)
			} else {
				require.Equal(t, 99.999998, fresh.TrustLevel.PaidAmount)
				require.Equal(t, 0, fresh.TrustLevel.Level)
			}
		}
		var l0 []int
		require.NoError(t, applyL0UserFilter(db, db.Model(&User{})).Order("users.id").Pluck("users.id", &l0).Error)
		require.Equal(t, []int{users[3].Id}, l0)
	}
	check()
	common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 3000000, 9.9, 2.4
	check()
	var stored []TopUp
	require.NoError(t, db.Order("id").Find(&stored).Error)
	after, err := json.Marshal(stored)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "policy reads must not rewrite old payment or credit snapshots")
	tiers := GetTrustLevelTiers()
	require.Equal(t, 100.0, tiers[2].MinPaidAmount)
	require.Equal(t, LegacyPaidPolicyCurrency, tiers[2].MinPaidAmountCurrency)
	require.InDelta(t, 100.0/7, *tiers[2].MinPaidAmountUSD, 1e-14)
}

func TestPaidPolicyOneCreditUSDDoesNotUseRoundedLegacyMicros(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	installPaidPolicyCurrencyFixture(t, 3000000)
	withDeveloperAccessSetting(t, true, 0.000001)
	user := User{Id: 917, Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&TopUp{UserId: user.Id, TradeNo: "one-credit", CreditedQuota: 1, Money: 1, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess}).Error)
	snapshot, err := GetFreshUserAccessSnapshot(&user)
	require.NoError(t, err)
	require.Zero(t, snapshot.PaidAmountMicros)
	require.Zero(t, snapshot.TrustLevel.PaidAmount)
	require.False(t, snapshot.DeveloperAccess.Granted)
	require.Equal(t, 0.0000000476190476, *snapshot.TrustLevel.PaidAmountUSD)
}

func TestPaidPolicyMissingBasisRejectsCachedFactsAndKeepsExplicitAccess(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}))
	withDeveloperAccessSetting(t, true, 0)
	user := User{Id: 918, Role: common.RoleCommonUser}
	require.NoError(t, db.Create(&TopUp{UserId: user.Id, TradeNo: "cached-payment", CreditedQuota: 500000, Money: 1, PaymentProvider: PaymentProviderStripe, Status: common.TopUpStatusSuccess}).Error)
	_, err := getPaidTopUpAggregate(user.Id)
	require.NoError(t, err)
	common.ClearCreditsPerUSD()
	_, err = getPaidTopUpAggregate(user.Id)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	_, err = GetFreshUserAccessSnapshot(&user)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	_, err = GetDeveloperAccessStateForUser(&user)
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	var count int64
	require.ErrorIs(t, applyL0UserFilter(db, db.Model(&User{})).Count(&count).Error, common.ErrCreditUnitsUnavailable)
	info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, nil, 100, true, 0, 0)
	require.Equal(t, 2, info.Level)
	require.Nil(t, info.PaidAmountUSD)
	require.Nil(t, info.NextLevelPaidAmountUSD)
	require.Nil(t, GetTrustLevelTiers()[2].MinPaidAmountUSD)
	require.Nil(t, LegacyPolicyAmountUSD(0))
	encoded, err := json.Marshal(info)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"paid_amount_usd":null`)
	one := 1
	for _, explicit := range []*User{{Role: common.RoleAdminUser}, {Role: common.RoleCommonUser, TrustLevelOverride: &one}, {Role: common.RoleCommonUser, ConsoleActivatedAt: 1}} {
		access, err := GetDeveloperAccessStateForUser(explicit)
		require.NoError(t, err)
		require.True(t, access.Granted)
	}
	require.NoError(t, EnrichUsersTrustLevels([]*User{{Role: common.RoleRootUser}, {Role: common.RoleCommonUser, TrustLevelOverride: &one}}))
}
