package model

import (
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestApplyViolationFeeEscalatesResetsAndNeverOverdraws(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &ViolationFeeState{}, &ViolationFeeRecord{}, &ViolationFeeAppeal{}))
	user := &User{Username: "violation-fee-user", Quota: int(common.QuotaPerUnit * 1.25), Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	policy := operation_setting.ViolationFeePolicy{
		Groups: []string{"default"}, Enabled: true, AmountsUSD: []float64{0.5, 1},
		InitialAmountUSD: 0.5, Multiplier: 2, MaxAmountUSD: 8, PeriodSeconds: 100, DrainBalanceWhenShort: true,
	}

	first, err := ApplyViolationFee(ViolationFeeChargeInput{UserID: user.Id, RequestID: "violation-1", Policy: policy, Group: "default", Now: 1000})
	require.NoError(t, err)
	require.Equal(t, 1, first.Record.Occurrence)
	require.Equal(t, int(common.QuotaPerUnit*0.5), first.Record.ChargedQuota)

	second, err := ApplyViolationFee(ViolationFeeChargeInput{UserID: user.Id, RequestID: "violation-2", Policy: policy, Group: "default", Now: 1001})
	require.NoError(t, err)
	require.Equal(t, 2, second.Record.Occurrence)
	require.Equal(t, int(common.QuotaPerUnit*0.75), second.Record.ChargedQuota, "only remaining wallet quota is charged")

	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 0, stored.Quota)

	reset, err := ApplyViolationFee(ViolationFeeChargeInput{UserID: user.Id, RequestID: "violation-3", Policy: policy, Group: "default", Now: 1100})
	require.NoError(t, err)
	require.Equal(t, 1, reset.Record.Occurrence, "counter resets at the period boundary")
	require.Equal(t, 0, reset.Record.ChargedQuota)
}

func TestApplyViolationFeeIsIdempotentAndAppealCanReverse(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &ViolationFeeState{}, &ViolationFeeRecord{}, &ViolationFeeAppeal{}))
	user := &User{Username: "violation-fee-appeal-user", Quota: int(common.QuotaPerUnit), Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(user).Error)
	policy := operation_setting.ViolationFeePolicy{Groups: []string{"*"}, AmountsUSD: []float64{0.5}, InitialAmountUSD: 0.5, Multiplier: 2, MaxAmountUSD: 2, PeriodSeconds: 100, DrainBalanceWhenShort: true}

	first, err := ApplyViolationFee(ViolationFeeChargeInput{UserID: user.Id, RequestID: "same-request", Policy: policy, Group: "default", Now: 2000})
	require.NoError(t, err)
	retry, err := ApplyViolationFee(ViolationFeeChargeInput{UserID: user.Id, RequestID: "same-request", Policy: policy, Group: "default", Now: 2001})
	require.NoError(t, err)
	require.True(t, retry.AlreadyExist)
	require.Equal(t, first.Record.ID, retry.Record.ID)

	appeal, err := SubmitViolationFeeAppeal(user.Id, first.Record.ID, "这是误判，请复核")
	require.NoError(t, err)
	_, err = ReviewViolationFeeAppeal(99, appeal.ID, true, "确认误判，退回处罚")
	require.NoError(t, err)
	var record ViolationFeeRecord
	require.NoError(t, db.First(&record, first.Record.ID).Error)
	require.Equal(t, ViolationFeeRecordStatusReversed, record.Status)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, int(common.QuotaPerUnit), stored.Quota)
}

func TestViolationFeeAppealUsesFutureCreditBasisAndKeepsChargedFacts(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&ViolationFeeRecord{}, &ViolationFeeAppeal{}))
	user := User{Username: "violation-rebased", AffCode: "violation-rebased", Quota: 123, UsedQuota: 6_710_363}
	require.NoError(t, db.Create(&user).Error)
	record := ViolationFeeRecord{UserID: user.Id, RequestID: "violation-rebased-0001",
		ChargedQuota: 6_710_363, RequestedQuota: 6_710_368, RequestedAmountUSD: 12.25,
		ChargedAmountUSD: 11.75, AmountCurrency: "USD", Status: ViolationFeeRecordStatusCharged, CreatedAt: 1_699_999_999}
	require.NoError(t, db.Create(&record).Error)
	seedAdViolationFutureCreditAudit(t, db, user.Id, []map[string]any{{
		"kind": "violation_fee_refund", "source_id": strconv.FormatUint(uint64(record.ID), 10), "user_id": user.Id,
		"original_quota": record.ChargedQuota, "rebased_quota": 1_000_000,
		"source": map[string]any{"id": record.ID, "user_id": user.Id, "status": record.Status,
			"charged_quota": record.ChargedQuota, "requested_quota": record.RequestedQuota,
			"created_at": record.CreatedAt, "reversed_at": record.ReversedAt, "reversed_by": record.ReversedBy},
	}})
	// No appeal existed at freeze time; the historical charge remains appealable.
	appeal, err := SubmitViolationFeeAppeal(user.Id, record.ID, "历史扣费需要复核")
	require.NoError(t, err)
	_, err = ReviewViolationFeeAppeal(99, appeal.ID, true, "误判退款")
	require.NoError(t, err)
	_, err = ReviewViolationFeeAppeal(99, appeal.ID, true, "重复退款")
	require.ErrorIs(t, err, ErrViolationFeeRecordReviewed)
	var wallet User
	require.NoError(t, db.First(&wallet, user.Id).Error)
	require.Equal(t, 1_000_123, wallet.Quota)
	require.Zero(t, wallet.UsedQuota)
	var stored ViolationFeeRecord
	require.NoError(t, db.First(&stored, record.ID).Error)
	require.Equal(t, ViolationFeeRecordStatusReversed, stored.Status)
	require.Equal(t, record.ChargedQuota, stored.ChargedQuota)
	require.Equal(t, record.RequestedQuota, stored.RequestedQuota)
	require.Equal(t, record.ChargedAmountUSD, stored.ChargedAmountUSD)
	require.Equal(t, record.RequestedAmountUSD, stored.RequestedAmountUSD)
	require.Equal(t, record.AmountCurrency, stored.AmountCurrency)
}

func TestViolationFeeAppealMissingOldBasisRollsBackReview(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&ViolationFeeRecord{}, &ViolationFeeAppeal{}))
	user := User{Username: "violation-missing", AffCode: "violation-missing", Quota: 123, UsedQuota: 6_710_363}
	require.NoError(t, db.Create(&user).Error)
	record := ViolationFeeRecord{UserID: user.Id, RequestID: "violation-missing-0001",
		ChargedQuota: 6_710_363, Status: ViolationFeeRecordStatusCharged, CreatedAt: 1_699_999_999}
	require.NoError(t, db.Create(&record).Error)
	seedAdViolationFutureCreditAudit(t, db, user.Id, nil)
	appeal, err := SubmitViolationFeeAppeal(user.Id, record.ID, "历史扣费需要复核")
	require.NoError(t, err)
	_, err = ReviewViolationFeeAppeal(99, appeal.ID, true, "误判退款")
	require.Error(t, err)
	var wallet User
	require.NoError(t, db.First(&wallet, user.Id).Error)
	require.Equal(t, 123, wallet.Quota)
	require.Equal(t, 6_710_363, wallet.UsedQuota)
	var stored ViolationFeeRecord
	require.NoError(t, db.First(&stored, record.ID).Error)
	require.Equal(t, ViolationFeeRecordStatusCharged, stored.Status)
	require.Zero(t, stored.ReversedAt)
	var storedAppeal ViolationFeeAppeal
	require.NoError(t, db.First(&storedAppeal, appeal.ID).Error)
	require.Equal(t, ViolationFeeAppealStatusPending, storedAppeal.Status)
}
