// Copyright (C) 2026 LIghtJUNction
// SPDX-License-Identifier: AGPL-3.0-or-later

package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupReferralTest(t *testing.T) (*gorm.DB, User, User, TopUp, ExternalTopUpSettlement) {
	t.Helper()
	db := setupExternalTopUpSettlementDB(t, 4)
	oldPolicy, err := json.Marshal(operation_setting.GetReferralRewardPolicy())
	require.NoError(t, err)
	payment := operation_setting.GetPaymentSetting()
	oldCompliance, oldTerms := payment.ComplianceConfirmed, payment.ComplianceTermsVersion
	payment.ComplianceConfirmed, payment.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
	require.NoError(t, operation_setting.UpdateReferralRewardPolicy(`{"enabled":true,"fixed_quota":100,"penalty_bps":2000}`))
	t.Cleanup(func() {
		require.NoError(t, operation_setting.UpdateReferralRewardPolicy(string(oldPolicy)))
		payment.ComplianceConfirmed, payment.ComplianceTermsVersion = oldCompliance, oldTerms
	})
	inviter := User{Username: "reward-parent", Password: "password", AffCode: "reward-parent-aff", Email: "parent@example.com", Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Quota: 9000}
	require.NoError(t, db.Create(&inviter).Error)
	invitee, order, settlement := createSettlementFixture(t, db, "reward-child")
	require.NoError(t, db.Model(&invitee).Updates(map[string]interface{}{"inviter_id": inviter.Id, "role": common.RoleCommonUser, "email": "child@example.com"}).Error)
	invitee.InviterId, invitee.Role = inviter.Id, common.RoleCommonUser
	return db, inviter, invitee, order, settlement
}

func referralUser(t *testing.T, id int) User {
	t.Helper()
	var user User
	require.NoError(t, DB.First(&user, id).Error)
	return user
}

func referralModeration(t *testing.T, id int, action string, penalty bool, key string) ReferralModerationRequest {
	t.Helper()
	reward, err := GetReferralRewardPreview(id)
	require.NoError(t, err)
	reason := "bulk_registration"
	if action == "restore_referral" {
		reason = "mistaken_ban"
	}
	return ReferralModerationRequest{UserID: id, ActorID: 999, ActorRole: common.RoleRootUser, Action: action, Reason: reason, Note: "Verified by administrator with supporting evidence", ConfirmPenalty: penalty, OperationKey: key, ExpectedRevision: reward.Revision}
}

func TestReferralFirstPaymentOnlyAndWebhookReplay(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	for i := 0; i < 3; i++ {
		_, err := completeExternalTopUpOnDB(db, settlement)
		require.NoError(t, err)
	}
	anotherUser, anotherOrder, anotherSettlement := createSettlementFixture(t, db, "reward-second")
	require.NoError(t, db.Model(&anotherOrder).Update("user_id", invitee.Id).Error)
	_ = anotherUser
	_, err := completeExternalTopUpOnDB(db, anotherSettlement)
	require.NoError(t, err)
	parent := referralUser(t, inviter.Id)
	assert.Equal(t, 100, parent.AffQuota)
	assert.Equal(t, 100, parent.AffHistoryQuota)
	assert.Equal(t, inviter.Quota, parent.Quota)
	entries, _, err := GetReferralRewardEntries(inviter.Id, 0)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "award", entries[0].Kind)
}

func TestReferralConcurrentOrdersCannotAwardTwice(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	_, second, secondSettlement := createSettlementFixture(t, db, "reward-concurrent")
	require.NoError(t, db.Model(&second).Update("user_id", invitee.Id).Error)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, evidence := range []ExternalTopUpSettlement{settlement, secondSettlement} {
		wg.Add(1)
		go func(e ExternalTopUpSettlement) {
			defer wg.Done()
			_, err := completeExternalTopUpOnDB(db, e)
			errs <- err
		}(evidence)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, 100, referralUser(t, inviter.Id).AffQuota)
	var count int64
	require.NoError(t, db.Model(&ReferralReward{}).Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestReferralBelowMinimumConsumesFirstPayment(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	require.NoError(t, operation_setting.UpdateReferralRewardPolicy(`{"enabled":true,"fixed_quota":100,"minimum_topup_quota":5000}`))
	_, err := completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	require.NoError(t, operation_setting.UpdateReferralRewardPolicy(`{"enabled":true,"fixed_quota":100}`))
	_, second, secondSettlement := createSettlementFixture(t, db, "after-minimum")
	require.NoError(t, db.Model(&second).Update("user_id", invitee.Id).Error)
	_, err = completeExternalTopUpOnDB(db, secondSettlement)
	require.NoError(t, err)
	assert.Zero(t, referralUser(t, inviter.Id).AffQuota)
	reward, err := GetReferralRewardPreview(invitee.Id)
	require.NoError(t, err)
	assert.Equal(t, "below_minimum", reward.Reason)
}

func TestReferralDisabledAndHistoricalPaymentsAreNotRetroactive(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	require.NoError(t, operation_setting.UpdateReferralRewardPolicy(operation_setting.DefaultReferralRewardPolicyJSON))
	_, err := completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	require.NoError(t, operation_setting.UpdateReferralRewardPolicy(`{"enabled":true,"fixed_quota":100}`))
	_, second, secondSettlement := createSettlementFixture(t, db, "after-enable")
	require.NoError(t, db.Model(&second).Update("user_id", invitee.Id).Error)
	_, err = completeExternalTopUpOnDB(db, secondSettlement)
	require.NoError(t, err)
	assert.Zero(t, referralUser(t, inviter.Id).AffQuota)
}

func TestReferralExcludesInternalFundsAndIneligibleAccounts(t *testing.T) {
	cases := []string{"gift", "admin", "linuxdo", "disposable", "self", "banned-parent", "banned-child", "compliance"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			db, inviter, invitee, order, settlement := setupReferralTest(t)
			switch name {
			case "gift", "admin":
				settlement.PaymentMethod = name
				require.NoError(t, db.Model(&order).Update("payment_method", name).Error)
			case "linuxdo":
				// Internal Epay LDC points are accepted by settlement but are not paid referrals.
				settlement.PaymentProvider, settlement.PaymentMethod, settlement.SettlementCurrency = PaymentProviderEpay, "ldc", "LDC"
				require.NoError(t, db.Model(&order).Updates(map[string]interface{}{"payment_provider": PaymentProviderEpay, "payment_method": "ldc", "settlement_currency": "LDC", "platform_amount_micros": 1234}).Error)
			case "disposable":
				require.NoError(t, db.Model(&invitee).Update("email", "test@mailinator.com").Error)
			case "self":
				require.NoError(t, db.Model(&invitee).Update("inviter_id", invitee.Id).Error)
			case "banned-parent":
				require.NoError(t, db.Model(&inviter).Update("status", common.UserStatusDisabled).Error)
			case "banned-child":
				require.NoError(t, db.Model(&invitee).Update("status", common.UserStatusDisabled).Error)
			case "compliance":
				operation_setting.GetPaymentSetting().ComplianceConfirmed = false
			}
			_, err := completeExternalTopUpOnDB(db, settlement)
			require.NoError(t, err)
			assert.Zero(t, referralUser(t, inviter.Id).AffQuota)
		})
	}
}

func TestReferralBanClawbackPenaltyDebtReplayAndReversal(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	_, err := completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	// Rewards have already been transferred/spent; purchased wallet funds stay intact.
	require.NoError(t, db.Model(&inviter).Update("aff_quota", 0).Error)
	ban := referralModeration(t, invitee.Id, "disable_abuse", true, "ban-operation-00001")
	require.NoError(t, ModerateReferralUser(ban))
	require.NoError(t, ModerateReferralUser(ban))
	parent := referralUser(t, inviter.Id)
	assert.Zero(t, parent.AffQuota)
	assert.EqualValues(t, 120, parent.AffDebtQuota)
	assert.Equal(t, inviter.Quota, parent.Quota)
	assert.Equal(t, common.UserStatusDisabled, referralUser(t, invitee.Id).Status)
	require.NoError(t, ModerateReferralUser(referralModeration(t, invitee.Id, "disable_abuse", true, "ban-operation-00002")))
	assert.EqualValues(t, 120, referralUser(t, inviter.Id).AffDebtQuota)
	changed := ban
	changed.Note = "different payload"
	require.ErrorIs(t, ModerateReferralUser(changed), ErrReferralConflict)
	require.NoError(t, ModerateReferralUser(referralModeration(t, invitee.Id, "restore_referral", false, "restore-operation-01")))
	parent = referralUser(t, inviter.Id)
	assert.Zero(t, parent.AffDebtQuota)
	assert.Zero(t, parent.AffQuota)
	assert.Equal(t, 100, parent.AffHistoryQuota)
	assert.Equal(t, common.UserStatusEnabled, referralUser(t, invitee.Id).Status)
}

func TestReferralPenaltyRequiresExplicitConfirmationAndUsesSnapshot(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	_, err := completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	require.NoError(t, operation_setting.UpdateReferralRewardPolicy(`{"enabled":true,"fixed_quota":999,"penalty_bps":10000}`))
	require.NoError(t, ModerateReferralUser(referralModeration(t, invitee.Id, "disable_abuse", false, "no-penalty-operation")))
	parent := referralUser(t, inviter.Id)
	assert.Zero(t, parent.AffQuota)
	assert.Zero(t, parent.AffDebtQuota)
	require.NoError(t, ModerateReferralUser(referralModeration(t, invitee.Id, "disable_abuse", true, "confirmed-penalty-01")))
	assert.EqualValues(t, 20, referralUser(t, inviter.Id).AffDebtQuota)
}

func TestReferralFutureRewardsOffsetDebt(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	_, err := completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	require.NoError(t, db.Model(&inviter).Update("aff_quota", 0).Error)
	require.NoError(t, ModerateReferralUser(referralModeration(t, invitee.Id, "disable_abuse", true, "offset-ban-operation")))
	child, _, evidence := createSettlementFixture(t, db, "next-referral")
	require.NoError(t, db.Model(&child).Update("inviter_id", inviter.Id).Error)
	_, err = completeExternalTopUpOnDB(db, evidence)
	require.NoError(t, err)
	parent := referralUser(t, inviter.Id)
	assert.Zero(t, parent.AffQuota)
	assert.EqualValues(t, 20, parent.AffDebtQuota)
	assert.Equal(t, inviter.Quota, parent.Quota)
	assert.Equal(t, 200, parent.AffHistoryQuota)
}

func TestReferralPartialRefundDuringBanDoesNotDoubleChargeOrRestoreRefund(t *testing.T) {
	db, inviter, invitee, order, settlement := setupReferralTest(t)
	completed, err := completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return refundReferralRewardTx(tx, completed, completed.SettledAmountMicros/2, 999)
	}))
	assert.Equal(t, 50, referralUser(t, inviter.Id).AffQuota)
	require.NoError(t, ModerateReferralUser(referralModeration(t, invitee.Id, "disable_abuse", true, "refund-ban-operation")))
	assert.EqualValues(t, 20, referralUser(t, inviter.Id).AffDebtQuota)
	require.NoError(t, operation_setting.UpdateReferralRewardPolicy(operation_setting.DefaultReferralRewardPolicyJSON))
	for i := 0; i < 2; i++ {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return refundReferralRewardTx(tx, completed, completed.SettledAmountMicros, 999)
		}))
	}
	assert.EqualValues(t, 20, referralUser(t, inviter.Id).AffDebtQuota)
	require.NoError(t, ModerateReferralUser(referralModeration(t, invitee.Id, "restore_referral", false, "refund-restore-00001")))
	parent := referralUser(t, inviter.Id)
	assert.Zero(t, parent.AffQuota)
	assert.Zero(t, parent.AffDebtQuota)
	reward, err := GetReferralRewardPreview(invitee.Id)
	require.NoError(t, err)
	assert.EqualValues(t, 100, reward.RefundRevokedQuota)
	assert.Equal(t, order.Id, reward.TopUpID)
}

func TestReferralStalePreviewAndInsufficientPermissionAreAtomic(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	_, err := completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	request := referralModeration(t, invitee.Id, "disable_abuse", true, "stale-preview-000001")
	request.ExpectedRevision = 0
	require.ErrorIs(t, ModerateReferralUser(request), ErrReferralConflict)
	request.ExpectedRevision = 1
	request.ActorRole = common.RoleCommonUser
	require.Error(t, ModerateReferralUser(request))
	assert.Equal(t, common.UserStatusEnabled, referralUser(t, invitee.Id).Status)
	assert.Equal(t, 100, referralUser(t, inviter.Id).AffQuota)
}

func TestReferralLedgerFailureRollsBackPaymentAndBan(t *testing.T) {
	db, inviter, invitee, _, settlement := setupReferralTest(t)
	sentinel := errors.New("injected ledger write failure")
	installFailure := func() {
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("test:referral-failure", func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "referral_reward_entries" {
				tx.AddError(sentinel)
			}
		}))
	}
	installFailure()
	_, err := completeExternalTopUpOnDB(db, settlement)
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, invitee.Quota, referralUser(t, invitee.Id).Quota)
	assert.Zero(t, referralUser(t, inviter.Id).AffQuota)
	require.NoError(t, db.Callback().Create().Remove("test:referral-failure"))
	_, err = completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	installFailure()
	t.Cleanup(func() { _ = db.Callback().Create().Remove("test:referral-failure") })
	require.ErrorIs(t, ModerateReferralUser(referralModeration(t, invitee.Id, "disable_abuse", true, "failed-ban-operation")), sentinel)
	assert.Equal(t, common.UserStatusEnabled, referralUser(t, invitee.Id).Status)
	assert.Equal(t, 100, referralUser(t, inviter.Id).AffQuota)
}

func TestReferralHistoryPaginationAndPrivacy(t *testing.T) {
	db, inviter, invitee, _, _ := setupReferralTest(t)
	for i := 0; i < 55; i++ {
		require.NoError(t, db.Create(&ReferralRewardEntry{OperationKey: fmt.Sprintf("page-%d", i), InviteeID: invitee.Id, InviterID: inviter.Id, Kind: "award", Reason: "first_topup", ActorID: 999}).Error)
	}
	entries, next, err := GetReferralRewardEntries(inviter.Id, 0)
	require.NoError(t, err)
	require.Len(t, entries, 50)
	assert.Positive(t, next)
	rest, next, err := GetReferralRewardEntries(inviter.Id, next)
	require.NoError(t, err)
	require.Len(t, rest, 5)
	assert.Zero(t, next)
	other, _, err := GetReferralRewardEntries(invitee.Id, 0)
	require.NoError(t, err)
	assert.Empty(t, other)
	encoded, err := json.Marshal(entries)
	require.NoError(t, err)
	for _, secret := range []string{"actor_id", "operation_key", "inviter_id"} {
		assert.False(t, strings.Contains(string(encoded), secret))
	}
}
