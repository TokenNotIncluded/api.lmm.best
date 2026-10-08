package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func trustLifecycleConfiguration() TrustLevelConfiguration {
	config := trustConfigurationFixture()
	for level, credits := range []int64{0, 500000, 1000000, 1500000, 2000000} {
		config.Tiers[level].MinPaidCredits = credits
	}
	config.DecayPeriodDays = 1
	return config
}

func TestTrustLifecycleNetRechargeRefundAndRoleJourney(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	require.NoError(t, db.AutoMigrate(&FinanceLedgerEntry{}, &ReferralReward{}, &ReferralLedgerEntry{}))
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(common.FixedCreditsPerUSD), decimal.NewFromInt(common.FixedCreditsPerUSD)))
	preserveRegistrationRewardSettings(t)
	withInviteRegistrationOption(t, "true")
	installTrustConfiguration(t, trustLifecycleConfiguration())

	inviter := User{Username: "trust-journey-inviter", AffCode: "trust-journey-inviter", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&inviter).Error)
	ordinary := User{Username: "trust-journey-ordinary", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	invited := User{Username: "trust-journey-invited", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return ordinary.InsertWithTx(tx, 0) }))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return invited.InsertWithTx(tx, inviter.Id) }))
	check := func(user *User, level int, credits string, paid bool) {
		t.Helper()
		require.NoError(t, db.First(user, user.Id).Error)
		snapshot, err := GetFreshUserAccessSnapshot(user)
		require.NoError(t, err)
		require.Equal(t, level, snapshot.TrustLevel.Level)
		require.Equal(t, paid, snapshot.PaidActivationComplete)
		require.Equal(t, level > 0, snapshot.DeveloperAccess.Granted)
		if level < TrustLevelAdmin {
			require.Equal(t, credits, *snapshot.TrustLevel.PaidCredits)
		}
		cached, err := GetTrustLevelInfoForUserBase(user.ToBaseUser())
		require.NoError(t, err)
		require.Equal(t, snapshot.TrustLevel.Level, cached.Level)
		access, err := GetDeveloperAccessStateForUser(user)
		require.NoError(t, err)
		require.Equal(t, snapshot.DeveloperAccess.Granted, access.Granted)
	}
	check(&ordinary, 0, "0", false)
	check(&invited, 1, "0", false)
	require.Nil(t, invited.TrustLevelOverride)

	settle := func(user *User, suffix string) string {
		t.Helper()
		tradeNo := "trust-journey-" + suffix
		order := TopUp{UserId: user.Id, TradeNo: tradeNo, Amount: 1, CreditedQuota: 500000,
			ExpectedAmountMicros: 1000000, Money: 1, SettlementCurrency: "USD",
			PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe,
			Status: common.TopUpStatusPending}
		require.NoError(t, db.Create(&order).Error)
		evidence := ExternalTopUpSettlement{TradeNo: tradeNo, PaymentProvider: PaymentProviderStripe,
			PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", SettledAmountMicros: 1000000,
			ProviderEventId: "evt-" + suffix, ProviderTransactionId: "pi-" + suffix}
		_, err := CompleteExternalTopUp(evidence)
		require.NoError(t, err)
		_, err = CompleteExternalTopUp(evidence)
		require.NoError(t, err, "a repeated verified callback must not double credit or progress")
		return tradeNo
	}
	orders := make([]string, 4)
	for index := range orders {
		orders[index] = settle(&ordinary, fmt.Sprintf("ordinary-%d", index))
		check(&ordinary, index+1, fmt.Sprint((index+1)*500000), true)
	}
	invitedOrder := settle(&invited, "invited")
	check(&invited, 1, "500000", true)
	refund := func(user *User, tradeNo string, micros int64, event string) {
		t.Helper()
		_, err := ApplyPaymentRefund(tradeNo, false, micros, "USD", event, PaymentMethodStripe, PaymentProviderStripe, "journey refund", user.Id)
		require.NoError(t, err)
	}
	refund(&ordinary, orders[0], 500000, "trust-journey-partial")
	check(&ordinary, 3, "1750000", true)
	refund(&ordinary, orders[0], 500000, "trust-journey-remainder")
	for index := 1; index < len(orders); index++ {
		refund(&ordinary, orders[index], 1000000, fmt.Sprintf("trust-journey-full-%d", index))
	}
	check(&ordinary, 0, "0", false)
	refund(&invited, invitedOrder, 1000000, "trust-journey-invited-full")
	check(&invited, 1, "0", false)

	for _, role := range []int{common.RoleAdminUser, common.RoleRootUser, common.RoleCommonUser} {
		require.NoError(t, db.Model(&User{}).Where("id = ?", ordinary.Id).Update("role", role).Error)
		level := 0
		if role == common.RoleAdminUser {
			level = 5
		} else if role == common.RoleRootUser {
			level = 6
		}
		check(&ordinary, level, "0", false)
	}
}

func TestTrustLifecycleAccountingCorrectionsDoNotRestoreDecayedLevel(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	installTrustConfiguration(t, trustLifecycleConfiguration())
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	oldBatch := common.BatchUpdateEnabled
	t.Cleanup(func() { common.BatchUpdateEnabled = oldBatch })
	now := time.Now().Unix()
	oldActivity := now - 4*86400
	user := User{Username: "trust-activity-journey", AffCode: "trust-activity-journey", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, CreatedAt: oldActivity, LastAPIActivityAt: oldActivity, AuthVersion: 1, UsedQuota: 1000}
	require.NoError(t, db.Create(&user).Error)
	order := TopUp{UserId: user.Id, TradeNo: "trust-activity-paid", CreditedQuota: 2000000, Money: 4,
		Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe,
		CreateTime: oldActivity, CompleteTime: oldActivity}
	require.NoError(t, db.Create(&order).Error)
	level, err := GetTrustLevelInfoByUserID(user.Id)
	require.NoError(t, err)
	require.Equal(t, 1, level.Level)
	require.Equal(t, 4, level.AutomaticLevel)

	common.BatchUpdateEnabled = true
	UpdateUserUsedQuota(user.Id, -200)
	FlushBatchUpdates()
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, oldActivity, user.LastAPIActivityAt, "a refund/correction is not a new request")
	level, err = GetTrustLevelInfoByUserID(user.Id)
	require.NoError(t, err)
	require.Equal(t, 1, level.Level)

	// A real, zero-charge request restores the paid tier through the cached
	// relay lookup as well as the fresh account response, in both write modes.
	for _, batch := range []bool{false, true} {
		require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("last_api_activity_at", oldActivity).Error)
		require.NoError(t, invalidateUserCache(user.Id))
		level, err = GetTrustLevelInfoByUserID(user.Id)
		require.NoError(t, err)
		require.Equal(t, 1, level.Level)
		common.BatchUpdateEnabled = batch
		UpdateUserUsedQuotaAndRequestCount(user.Id, 0)
		if batch {
			FlushBatchUpdates()
		}
		level, err = GetTrustLevelInfoByUserID(user.Id)
		require.NoError(t, err)
		require.Equal(t, 4, level.Level, "committed request activity must replace the stale cached anchor")
		require.Equal(t, 0, level.InactivityDecaySteps)
	}
}
