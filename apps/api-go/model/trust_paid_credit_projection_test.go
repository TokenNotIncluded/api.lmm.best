package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTrustPaidCreditsCurrentOrdersUseNetIntegerEntitlement(t *testing.T) {
	db, user := setupAdminUserTopupProjection(t)
	partial := adminTopupProjectionOrder("trust-current-partial", 500001)
	partial.RefundedQuota = 1
	full := adminTopupProjectionOrder("trust-current-full", 900000)
	full.RefundedQuota = full.CreditedQuota
	pending := adminTopupProjectionOrder("trust-current-pending", 10000000)
	pending.Status = common.TopUpStatusPending
	for _, order := range []*TopUp{&partial, &full, &pending} {
		require.NoError(t, db.Create(order).Error)
	}
	aggregate, err := getFreshPaidTopUpAggregate(user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 500000, aggregate.PaidCredits)
	require.EqualValues(t, 1, aggregate.PaidRows)
	policy := CurrentDeveloperAccessPolicy()
	policy.paidActivationEnabled = true
	policy.trustConfiguration.Tiers[1].MinPaidCredits = 500001
	policy.paidActivationMinMicros = 1000002
	access, err := developerAccessStateForUserBase(db, user.ToBaseUser(), policy)
	require.NoError(t, err)
	require.False(t, access.Granted)
	var count int64
	require.NoError(t, applyL0UserFilterWithPolicy(db, db.Model(&User{}), policy).Count(&count).Error)
	require.EqualValues(t, 1, count)
	policy.trustConfiguration.Tiers[1].MinPaidCredits = 500000
	policy.paidActivationMinMicros = 1000000
	access, err = developerAccessStateForUserBase(db, user.ToBaseUser(), policy)
	require.NoError(t, err)
	require.True(t, access.Granted)
	require.NoError(t, applyL0UserFilterWithPolicy(db, db.Model(&User{}), policy).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&TopUp{}).Where("id = ?", partial.Id).Update("refunded_quota", 500002).Error)
	_, err = getFreshPaidTopUpAggregate(user.Id)
	require.ErrorIs(t, err, ErrPaidCreditProjectionUnavailable)
}

func TestTrustPaidCreditsFrozenNetBasisAndRefundInvalidateCache(t *testing.T) {
	db, user := setupAdminUserTopupProjection(t)
	require.NoError(t, db.AutoMigrate(&FinanceLedgerEntry{}, &SubscriptionOrder{}))
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 1000000).Error)
	old := adminTopupProjectionOrder("trust-frozen-partial", 6710363)
	// Original refunded counters retain their historical unit. The remaining
	// canonical pool supplies subsequent net entitlement, without current FX.
	old.RefundedAmountMicros = 2000000
	old.RefundedQuota = 1342073
	next := adminTopupProjectionOrder("trust-frozen-new", 500001)
	next.CreateTime, next.CompleteTime = 2000, 2001
	require.NoError(t, db.Create(&old).Error)
	require.NoError(t, db.Create(&next).Error)
	plan := adminTopupProjectionPlan()
	addAdminTopupRefundBasis(t, db, plan, old, 800000)
	putAdminTopupProjectionAudit(t, db, plan)
	previousFX := operation_setting.USDExchangeRate
	t.Cleanup(func() { operation_setting.USDExchangeRate = previousFX; InvalidatePaidTopUpAggregate(user.Id) })
	InvalidatePaidTopUpAggregate(user.Id)
	operation_setting.USDExchangeRate = 99
	warmed, err := getPaidTopUpAggregate(user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 1300001, warmed.PaidCredits)
	result, err := ApplyPaymentRefund(old.TradeNo, false, 2000000, "USD", "trust-net-refund", PaymentMethodStripe, PaymentProviderStripe, "fixture", user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 200000, result.QuotaDebited)
	after, err := getPaidTopUpAggregate(user.Id)
	require.NoError(t, err)
	require.EqualValues(t, 1100001, after.PaidCredits)
	var stored TopUp
	require.NoError(t, db.First(&stored, old.Id).Error)
	require.Equal(t, old.CreditedQuota, stored.CreditedQuota)
	require.EqualValues(t, 2684145, stored.RefundedQuota)
	require.NoError(t, db.Model(&WalletTopUpCreditRebase{}).Where("top_up_id = ?", old.Id).Update("rebased_debited_quota", 200001).Error)
	_, err = getFreshPaidTopUpAggregate(user.Id)
	require.ErrorIs(t, err, ErrPaidCreditProjectionUnavailable)
}

func TestTrustPaidCreditsMissingFrozenOrderFailsClosed(t *testing.T) {
	db, user := setupAdminUserTopupProjection(t)
	old := adminTopupProjectionOrder("trust-missing-freeze", 6710363)
	require.NoError(t, db.Create(&old).Error)
	putAdminTopupProjectionAudit(t, db, adminTopupProjectionPlan())
	_, err := getFreshPaidTopUpAggregate(user.Id)
	require.ErrorIs(t, err, ErrPaidCreditProjectionUnavailable)
}

func TestFreshUserAccessSnapshotDBUsesCallerDatabaseAndRejectsInvalidRefund(t *testing.T) {
	db, user := setupAdminUserTopupProjection(t)
	order := adminTopupProjectionOrder("trust-caller-db", 500000)
	require.NoError(t, db.Create(&order).Error)
	previousDB := DB
	DB = nil
	defer func() { DB = previousDB }()

	snapshot, err := getFreshUserAccessSnapshotDB(db, &user)
	require.NoError(t, err, "the caller's database must supply both aggregate and refund audit reads")
	require.NotNil(t, snapshot.TrustLevel.PaidCredits)
	require.Equal(t, "500000", *snapshot.TrustLevel.PaidCredits)

	require.NoError(t, db.Model(&TopUp{}).Where("id = ?", order.Id).Update("refunded_quota", 500001).Error)
	_, err = getFreshUserAccessSnapshotDB(db, &user)
	require.ErrorIs(t, err, ErrPaidCreditProjectionUnavailable)
	_, err = AssistantToolLevelDB(db, user.Id)
	require.ErrorIs(t, err, ErrPaidCreditProjectionUnavailable, "an invalid financial projection must not authorize assistant writes")
}
