package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTrustPaidCreditsHistoricalRefundNullPostgres(t *testing.T) {
	for _, state := range []string{"fully_refunded", "refund_mismatch", "rebase_debit_mismatch", "missing_order"} {
		t.Run(state, func(t *testing.T) {
			db := openIsolatedPostgresCacheTestDB(t, &User{}, &TopUp{})
			previousDB := DB
			DB = db
			t.Cleanup(func() { DB = previousDB })
			usePostgresDatabaseType(t)
			assertTrustPaidCreditsHistoricalRefundNull(t, db, state)
		})
	}
}

func TestTrustPaidCreditsHistoricalRefundNullSQLite(t *testing.T) {
	for _, state := range []string{"fully_refunded", "refund_mismatch", "rebase_debit_mismatch", "missing_order"} {
		t.Run(state, func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			assertTrustPaidCreditsHistoricalRefundNull(t, db, state)
		})
	}
}

func assertTrustPaidCreditsHistoricalRefundNull(t *testing.T, db *gorm.DB, state string) {
	t.Helper()
	installPaidPolicyCurrencyFixture(t, 500000)
	users := []User{
		{Id: 1, Username: "historical-paid", AffCode: "historical-paid", Role: common.RoleCommonUser},
		{Id: 2, Username: "current-paid", AffCode: "current-paid", Role: common.RoleCommonUser},
	}
	require.NoError(t, db.Create(&users).Error)
	old := adminTopupProjectionOrder("historical-partial", 6710363)
	full := adminTopupProjectionOrder("historical-fully-refunded", 6710363)
	full.RefundedQuota, full.RefundedAmountMicros = full.CreditedQuota, full.SettledAmountMicros
	next := adminTopupProjectionOrder("current-unrelated-user", 500001)
	next.UserId, next.CreateTime, next.CompleteTime = 2, 2000, 2001
	for _, order := range []*TopUp{&old, &full, &next} {
		require.NoError(t, db.Create(order).Error)
	}
	plan := adminTopupProjectionPlan()
	addAdminTopupRefundBasis(t, db, plan, old, 1000000)
	addAdminTopupRefundBasis(t, db, plan, full, 0)
	putAdminTopupProjectionAudit(t, db, plan)
	switch state {
	case "refund_mismatch":
		require.NoError(t, db.Model(&TopUp{}).Where("id = ?", old.Id).Update("refunded_quota", 1).Error)
	case "rebase_debit_mismatch":
		require.NoError(t, db.Model(&WalletTopUpCreditRebase{}).Where("top_up_id = ?", old.Id).Update("rebased_debited_quota", 1).Error)
	case "missing_order":
		require.NoError(t, db.Delete(&old).Error)
	}
	var ordersBefore, ordersAfter []TopUp
	var basesBefore, basesAfter []WalletTopUpCreditRebase
	require.NoError(t, db.Order("id").Find(&ordersBefore).Error)
	require.NoError(t, db.Order("top_up_id").Find(&basesBefore).Error)

	// SQL parsing must succeed even when a fully refunded or unprovable
	// historical order belongs to a different user from the requested user.
	aggregate, err := getFreshPaidTopUpAggregate(2)
	require.NoError(t, err)
	require.EqualValues(t, 500001, aggregate.PaidCredits)
	require.EqualValues(t, 1, aggregate.PaidRows)
	aggregates, err := getFreshPaidTopUpAggregates([]int{1, 2})
	require.NoError(t, err)
	require.EqualValues(t, 500001, aggregates[2].PaidCredits)

	unknown := state == "refund_mismatch" || state == "rebase_debit_mismatch"
	require.Equal(t, unknown, aggregates[1].ProjectionUnavailable)
	if unknown {
		_, err = getFreshPaidTopUpAggregate(1)
		require.ErrorIs(t, err, ErrPaidCreditProjectionUnavailable)
	} else if state == "missing_order" {
		require.Zero(t, aggregates[1].PaidCredits)
		require.Zero(t, aggregates[1].PaidRows)
	} else {
		require.EqualValues(t, 1000000, aggregates[1].PaidCredits)
		require.EqualValues(t, 1, aggregates[1].PaidRows)
	}

	policy := CurrentDeveloperAccessPolicy()
	policy.paidActivationEnabled = true
	policy.trustConfiguration.Tiers[1].MinPaidCredits = 500001
	policy.paidActivationMinMicros = 1000002
	access, err := developerAccessStateForUserBase(db, users[1].ToBaseUser(), policy)
	require.NoError(t, err)
	require.True(t, access.Granted)
	access, err = developerAccessStateForUserBase(db, users[0].ToBaseUser(), policy)
	if unknown {
		require.ErrorIs(t, err, ErrPaidCreditProjectionUnavailable)
		require.False(t, access.Granted)
	} else {
		require.NoError(t, err)
		require.Equal(t, state != "missing_order", access.Granted)
	}
	var l0 []int
	require.NoError(t, applyL0UserFilterWithPolicy(db, db.Model(&User{}), policy).Order("users.id").Pluck("users.id", &l0).Error)
	if state == "fully_refunded" {
		require.Empty(t, l0)
	} else {
		require.Equal(t, []int{1}, l0)
	}
	require.NoError(t, db.Order("id").Find(&ordersAfter).Error)
	require.NoError(t, db.Order("top_up_id").Find(&basesAfter).Error)
	require.Equal(t, ordersBefore, ordersAfter, "entitlement reads must not change settled orders")
	require.Equal(t, basesBefore, basesAfter, "entitlement reads must not change refund audit facts")
}
