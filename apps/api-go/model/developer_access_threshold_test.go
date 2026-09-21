package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withDeveloperAccessSetting installs a boundary policy for one test and
// restores whatever the process was configured with afterwards.
func withDeveloperAccessSetting(t *testing.T, enabled bool, minAmount float64) {
	t.Helper()
	setting := operation_setting.GetDeveloperAccessSetting()
	previous := *setting
	setting.PaidActivationEnabled = enabled
	setting.PaidActivationMinAmount = minAmount
	t.Cleanup(func() { *setting = previous })
}

func creditedTopUp(userID int, tradeNo string, usd float64) *TopUp {
	return &TopUp{
		UserId:          userID,
		TradeNo:         tradeNo,
		Amount:          int64(usd),
		CreditedQuota:   int64(usd * common.QuotaPerUnit),
		Money:           usd,
		Status:          common.TopUpStatusSuccess,
		PaymentProvider: PaymentProviderStripe,
	}
}

func TestPaidActivationRequiresTheConfiguredCumulativeAmount(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	withDeveloperAccessSetting(t, true, 1)
	const userID = 701

	require.NoError(t, db.Create(&[]*TopUp{
		creditedTopUp(userID, "half-one", 0.4),
		creditedTopUp(userID, "half-two", 0.3),
	}).Error)

	aggregate, err := getFreshPaidTopUpAggregate(userID)
	require.NoError(t, err)
	assert.InDelta(t, 0.7, aggregate.PaidAmount, 0.0001)
	// A real payment was made, so the account is no longer a stranger, but it
	// has not reached the boundary that replaces manual review.
	assert.False(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))

	granted, err := HasSuccessfulPaidTopUp(userID)
	require.NoError(t, err)
	assert.True(t, granted, "the raw fact query still reports the payment")

	state, err := developerAccessStateForUserBase(db, &UserBase{Id: userID, Role: common.RoleCommonUser}, CurrentDeveloperAccessPolicy())
	require.NoError(t, err)
	assert.False(t, state.Granted)
	assert.False(t, state.PaidActivationComplete)

	// Crossing the threshold with a third payment is what upgrades the account.
	require.NoError(t, db.Create(creditedTopUp(userID, "remainder", 0.3)).Error)
	invalidatePaidTopUpAggregate(userID)

	aggregate, err = getFreshPaidTopUpAggregate(userID)
	require.NoError(t, err)
	assert.InDelta(t, 1, aggregate.PaidAmount, 0.0001)
	assert.True(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))

	state, err = developerAccessStateForUserBase(db, &UserBase{Id: userID, Role: common.RoleCommonUser}, CurrentDeveloperAccessPolicy())
	require.NoError(t, err)
	assert.True(t, state.Granted)
	assert.True(t, state.PaidActivationComplete)

	info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, nil, aggregate.PaidAmount, true, 0, 0)
	assert.Equal(t, TrustLevelMinUser+1, info.Level)
}

func TestPaidActivationCanBeTurnedOffEntirely(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	withDeveloperAccessSetting(t, false, 1)
	const userID = 702

	require.NoError(t, db.Create(creditedTopUp(userID, "well-past-threshold", 50)).Error)

	aggregate, err := getFreshPaidTopUpAggregate(userID)
	require.NoError(t, err)
	assert.InDelta(t, 50, aggregate.PaidAmount, 0.0001)
	assert.False(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))

	state, err := developerAccessStateForUserBase(db, &UserBase{Id: userID, Role: common.RoleCommonUser}, CurrentDeveloperAccessPolicy())
	require.NoError(t, err)
	assert.False(t, state.Granted, "every account goes through review while paid activation is off")

	// An approved review still lets the account through.
	state, err = developerAccessStateForUserBase(db, &UserBase{Id: userID, Role: common.RoleCommonUser, ConsoleActivatedAt: 1}, CurrentDeveloperAccessPolicy())
	require.NoError(t, err)
	assert.True(t, state.Granted)
}

func TestZeroThresholdKeepsAnySuccessfulRechargeQualifying(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	withDeveloperAccessSetting(t, true, 0)
	const userID = 703

	require.NoError(t, db.Create(creditedTopUp(userID, "one-cent", 0.01)).Error)

	aggregate, err := getFreshPaidTopUpAggregate(userID)
	require.NoError(t, err)
	assert.True(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))

	state, err := developerAccessStateForUserBase(db, &UserBase{Id: userID, Role: common.RoleCommonUser}, CurrentDeveloperAccessPolicy())
	require.NoError(t, err)
	assert.True(t, state.Granted)
}

func TestThresholdChangeIsNotMaskedByACachedAggregate(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	withDeveloperAccessSetting(t, true, 0)
	const userID = 704

	require.NoError(t, db.Create(creditedTopUp(userID, "below-the-new-bar", 0.5)).Error)

	// Warm the cache under the permissive policy.
	aggregate, err := getPaidTopUpAggregate(userID)
	require.NoError(t, err)
	assert.True(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))

	// Raising the bar has to take effect without waiting for the cache TTL,
	// because the cached value records the facts and not the verdict.
	operation_setting.GetDeveloperAccessSetting().PaidActivationMinAmount = 1
	cached, err := getPaidTopUpAggregate(userID)
	require.NoError(t, err)
	assert.False(t, cached.paidActivationComplete(CurrentDeveloperAccessPolicy()))
}
