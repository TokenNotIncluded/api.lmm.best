package model

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

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
