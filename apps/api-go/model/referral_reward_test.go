package model

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupReferralTest(t *testing.T) (*gorm.DB, User, User, User, TopUp, ExternalTopUpSettlement) {
	t.Helper()
	db := setupExternalTopUpSettlementDB(t, 8)
	require.NoError(t, db.AutoMigrate(&ReferralReward{}, &ReferralLedgerEntry{}, &ReferralModerationEvent{}, &UserSession{}, &FinanceLedgerEntry{}))
	preserveRegistrationRewardSettings(t)
	oldRedis, oldOptions := common.RedisEnabled, common.OptionMap
	common.RedisEnabled = false
	common.OptionMap = map[string]string{}
	common.QuotaForInviter = 1_000_000
	t.Cleanup(func() { common.RedisEnabled = oldRedis; common.OptionMap = oldOptions })
	actor := User{Username: "operator", AffCode: "operator", Role: common.RoleRootUser, Status: common.UserStatusEnabled}
	inviter := User{Username: "inviter", AffCode: "inviter", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 777}
	require.NoError(t, db.Create(&actor).Error)
	require.NoError(t, db.Create(&inviter).Error)
	invitee, order, payment := createSettlementFixture(t, db, "referral-first")
	require.NoError(t, db.Model(&User{}).Where("id = ?", invitee.Id).Update("inviter_id", inviter.Id).Error)
	return db, actor, inviter, invitee, order, payment
}

func nextReferralPayment(t *testing.T, db *gorm.DB, order TopUp, payment ExternalTopUpSettlement, suffix string) ExternalTopUpSettlement {
	t.Helper()
	order.Id = 0
	order.TradeNo = "referral-" + suffix
	require.NoError(t, db.Create(&order).Error)
	payment.TradeNo = order.TradeNo
	payment.ProviderEventId = "event-" + suffix
	payment.ProviderTransactionId = "transaction-" + suffix
	return payment
}

func referralUser(t *testing.T, db *gorm.DB, id int) User {
	t.Helper()
	var user User
	require.NoError(t, db.First(&user, id).Error)
	return user
}

func referralBan(actor, invitee User, key string, penalize bool) ReferralModerationEvent {
	return ReferralModerationEvent{RequestId: key, ActorId: actor.Id, UserId: invitee.Id, Action: "ban_abuse",
		Reason: "bulk_registration", Evidence: "confirmed registration farm; reviewed by administrator", Penalize: penalize}
}

func TestReferralFirstRealPaymentExactlyOnce(t *testing.T) {
	db, _, inviter, invitee, order, payment := setupReferralTest(t)
	require.NoError(t, grantFirstTopUpReferralTx(db, &order), "pending orders have no signed payment evidence")
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	_, err = CompleteExternalTopUp(payment)
	require.NoError(t, err)
	second := nextReferralPayment(t, db, order, payment, "second")
	_, err = CompleteExternalTopUp(second)
	require.NoError(t, err)
	got := referralUser(t, db, inviter.Id)
	require.Equal(t, 1_000_000, got.AffQuota)
	require.Equal(t, 1_000_000, got.AffHistoryQuota)
	require.Equal(t, order.Id, referralUser(t, db, invitee.Id).ReferralFirstTopUpId)
	var count int64
	require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestReferralConcurrentDistinctOrdersHaveOneWinner(t *testing.T) {
	db, _, inviter, invitee, order, payment := setupReferralTest(t)
	second := nextReferralPayment(t, db, order, payment, "concurrent")
	var wg sync.WaitGroup
	errorsFound := make(chan error, 2)
	for _, p := range []ExternalTopUpSettlement{payment, second} {
		wg.Add(1)
		go func(p ExternalTopUpSettlement) {
			defer wg.Done()
			_, err := completeExternalTopUpOnDB(db.Session(&gorm.Session{NewDB: true}), p)
			errorsFound <- err
		}(p)
	}
	wg.Wait()
	close(errorsFound)
	for err := range errorsFound {
		require.NoError(t, err)
	}
	require.Equal(t, 1_000_000, referralUser(t, db, inviter.Id).AffQuota)
	require.Equal(t, invitee.Quota+2*int(order.CreditedQuota), referralUser(t, db, invitee.Id).Quota)
}

func TestReferralDisabledOrBelowMinimumFirstPaymentCannotBeRetried(t *testing.T) {
	for _, mode := range []string{"disabled", "below_minimum", "historical"} {
		t.Run(mode, func(t *testing.T) {
			db, _, inviter, _, order, payment := setupReferralTest(t)
			switch mode {
			case "disabled":
				common.QuotaForInviter = 0
			case "below_minimum":
				common.OptionMap["ReferralMinTopUpQuota"] = "2000"
			case "historical":
				prior := order
				prior.Id = 0
				prior.TradeNo = "old-real-payment"
				prior.Status = common.TopUpStatusSuccess
				require.NoError(t, db.Create(&prior).Error)
			}
			_, err := CompleteExternalTopUp(payment)
			require.NoError(t, err)
			common.QuotaForInviter = 1_000_000
			delete(common.OptionMap, "ReferralMinTopUpQuota")
			_, err = CompleteExternalTopUp(nextReferralPayment(t, db, order, payment, "later"))
			require.NoError(t, err)
			require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
		})
	}
}

func TestReferralExcludesInternalAndManualCredits(t *testing.T) {
	db, _, inviter, invitee, order, payment := setupReferralTest(t)
	require.NoError(t, ManualCompleteTopUp(order.TradeNo, "127.0.0.1"))
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
	require.Zero(t, referralUser(t, db, invitee.Id).ReferralFirstTopUpId)
	_, err := CompleteExternalTopUp(nextReferralPayment(t, db, order, payment, "after-manual"))
	require.NoError(t, err)
	require.Equal(t, 1_000_000, referralUser(t, db, inviter.Id).AffQuota)
	for _, method := range []string{"gift", "admin", "bonus", "ldc", "linuxdo", "balance"} {
		candidate := order
		candidate.PaymentProvider = PaymentProviderEpay
		candidate.PaymentMethod = method
		candidate.SettledAmountMicros = 1
		candidate.SettlementCurrency = "LDC"
		candidate.ProviderEventId = optionalEvidence("evt")
		require.NoError(t, grantFirstTopUpReferralTx(db, &candidate))
	}
}

func TestReferralBanDebtAndIdempotentMistakenBanRestore(t *testing.T) {
	db, actor, inviter, invitee, _, payment := setupReferralTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	require.NoError(t, inviter.TransferAffQuotaToQuota(1_000_000))
	ban := referralBan(actor, invitee, "case-one", true)
	require.NoError(t, ModerateReferralUser(ban))
	require.NoError(t, ModerateReferralUser(ban))
	got := referralUser(t, db, inviter.Id)
	require.Equal(t, -1_200_000, got.AffQuota)
	require.Equal(t, 1_000_777, got.Quota)
	require.Error(t, got.TransferAffQuotaToQuota(500_000))
	require.Equal(t, common.UserStatusDisabled, referralUser(t, db, invitee.Id).Status)
	restore := ReferralModerationEvent{RequestId: "appeal-one", ActorId: actor.Id, UserId: invitee.Id, Action: "restore_referral", Reason: "mistaken_ban", Evidence: "appeal upheld"}
	require.NoError(t, ModerateReferralUser(restore))
	require.NoError(t, ModerateReferralUser(restore))
	require.NoError(t, ModerateReferralUser(ban), "old ban retry after appeal must not reapply")
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
	require.Equal(t, common.UserStatusEnabled, referralUser(t, db, invitee.Id).Status)
	var count int64
	require.NoError(t, db.Model(&ReferralLedgerEntry{}).Count(&count).Error)
	require.EqualValues(t, 5, count)
	ban.Evidence = "altered request"
	require.ErrorIs(t, ModerateReferralUser(ban), ErrReferralConflict)
}

func TestReferralBanRequiresExplicitPenaltyAndCannotChargeTwice(t *testing.T) {
	db, actor, inviter, invitee, _, payment := setupReferralTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	require.NoError(t, ModerateReferralUser(referralBan(actor, invitee, "without-penalty", false)))
	require.NoError(t, ModerateReferralUser(referralBan(actor, invitee, "duplicate-ban", true)))
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
	require.Equal(t, 777, referralUser(t, db, inviter.Id).Quota)
}

func TestReferralNewRewardsRepayDebt(t *testing.T) {
	db, actor, inviter, invitee, _, payment := setupReferralTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	require.NoError(t, inviter.TransferAffQuotaToQuota(1_000_000))
	require.NoError(t, ModerateReferralUser(referralBan(actor, invitee, "debt", true)))
	other, _, otherPayment := createSettlementFixture(t, db, "another-invitee")
	require.NoError(t, db.Model(&User{}).Where("id = ?", other.Id).Update("inviter_id", inviter.Id).Error)
	_, err = CompleteExternalTopUp(otherPayment)
	require.NoError(t, err)
	require.Equal(t, -200_000, referralUser(t, db, inviter.Id).AffQuota)
}

func TestReferralFullRefundReclaimsWithoutPenalty(t *testing.T) {
	db, actor, inviter, _, order, payment := setupReferralTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	for i := 1; i <= 2; i++ {
		_, err = ApplyPaymentRefund(order.TradeNo, false, payment.SettledAmountMicros/2, "USD", fmt.Sprintf("refund-%d", i), PaymentMethodStripe, PaymentProviderStripe, "refund", actor.Id)
		require.NoError(t, err)
		expected := 1_000_000
		if i == 2 {
			expected = 0
		}
		require.Equal(t, expected, referralUser(t, db, inviter.Id).AffQuota)
	}
	_, err = ApplyPaymentRefund(order.TradeNo, false, payment.SettledAmountMicros/2, "USD", "refund-2", PaymentMethodStripe, PaymentProviderStripe, "refund", actor.Id)
	require.NoError(t, err)
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
}

func TestReferralRefundAfterBanCannotResurrectRewardOnAppeal(t *testing.T) {
	db, actor, inviter, invitee, order, payment := setupReferralTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	require.NoError(t, ModerateReferralUser(referralBan(actor, invitee, "ban-before-refund", true)))
	_, err = ApplyPaymentRefund(order.TradeNo, false, payment.SettledAmountMicros, "USD", "refund-after-ban", PaymentMethodStripe, PaymentProviderStripe, "refund", actor.Id)
	require.NoError(t, err)
	require.Equal(t, -200_000, referralUser(t, db, inviter.Id).AffQuota)
	require.NoError(t, ModerateReferralUser(ReferralModerationEvent{RequestId: "appeal-refunded", ActorId: actor.Id, UserId: invitee.Id, Action: "restore_referral", Reason: "mistaken_ban", Evidence: "mistaken ban confirmed"}))
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota, "only the incorrect penalty is restored")
}

func TestReferralRewardLedgerFailureRollsBackPayment(t *testing.T) {
	db, _, inviter, invitee, order, payment := setupReferralTest(t)
	injected := errors.New("ledger unavailable")
	name := "test:referral-ledger-failure"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "referral_ledger_entries" {
			tx.AddError(injected)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(name) })
	_, err := CompleteExternalTopUp(payment)
	require.ErrorIs(t, err, injected)
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
	got := referralUser(t, db, invitee.Id)
	require.Zero(t, got.ReferralFirstTopUpId)
	require.Equal(t, invitee.Quota, got.Quota)
	var stored TopUp
	require.NoError(t, db.First(&stored, order.Id).Error)
	require.Equal(t, common.TopUpStatusPending, stored.Status)
}

func TestReferralPolicyRejectsInvalidValuesAndUsesSafeArithmetic(t *testing.T) {
	for _, value := range []string{"-1", "NaN", "1.5", "9007199254740992"} {
		require.Error(t, validateReferralOption("ReferralMinTopUpQuota", value))
	}
	require.Error(t, validateReferralOption("ReferralPenaltyPercent", "101"))
	require.NoError(t, validateReferralOption("ReferralPenaltyPercent", "20"))
	require.Equal(t, common.MaxWalletQuota, referralPenalty(common.MaxWalletQuota, 100, 0))
	require.Equal(t, 50, referralPenalty(1000, 20, 50))
}

func TestReferralBindingAndFirstTopUpFactCannotBeChangedByProfileUpdate(t *testing.T) {
	db, _, inviter, invitee, order, payment := setupReferralTest(t)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	changed := referralUser(t, db, invitee.Id)
	changed.InviterId = 12345
	changed.ReferralFirstTopUpId = 999
	changed.DisplayName = "Updated profile"
	require.NoError(t, changed.Update(false))
	actual := referralUser(t, db, invitee.Id)
	require.Equal(t, inviter.Id, actual.InviterId)
	require.Equal(t, order.Id, actual.ReferralFirstTopUpId)
	require.Equal(t, "Updated profile", actual.DisplayName)
}

func TestReferralInternalAndUnverifiedOrdersDoNotConsumeFirstTopUp(t *testing.T) {
	for _, kind := range []string{"internal", "unverified", "unsupported_provider", "self_invite", "banned_inviter", "same_customer"} {
		t.Run(kind, func(t *testing.T) {
			db, _, inviter, invitee, order, payment := setupReferralTest(t)
			switch kind {
			case "internal", "unverified", "unsupported_provider":
				candidate := order
				candidate.SettledAmountMicros = 1
				candidate.ProviderEventId = optionalEvidence("event")
				if kind == "internal" {
					candidate.PaymentProvider = PaymentProviderEpay
					candidate.PaymentMethod = "ldc"
					candidate.SettlementCurrency = "LDC"
				}
				if kind == "unverified" {
					candidate.ProviderEventId = nil
				}
				if kind == "unsupported_provider" {
					candidate.PaymentProvider = "bonus"
				}
				require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return grantFirstTopUpReferralTx(tx, &candidate) }))
				require.Zero(t, referralUser(t, db, invitee.Id).ReferralFirstTopUpId)
			case "self_invite":
				require.NoError(t, db.Model(&User{}).Where("id = ?", invitee.Id).Update("inviter_id", invitee.Id).Error)
			case "banned_inviter":
				require.NoError(t, db.Model(&User{}).Where("id = ?", inviter.Id).Update("status", common.UserStatusDisabled).Error)
			case "same_customer":
				require.NoError(t, db.Model(&User{}).Where("id = ?", inviter.Id).Update("stripe_customer", payment.StripeCustomer).Error)
			}
			if kind == "self_invite" || kind == "banned_inviter" || kind == "same_customer" {
				_, err := CompleteExternalTopUp(payment)
				require.NoError(t, err)
			}
			require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
		})
	}
}

func TestReferralPolicyNormalizesValidatedWhitespace(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	previous := common.OptionMap
	common.OptionMap = map[string]string{"ReferralPenaltyPercent": " 35 ", "ReferralMinTopUpQuota": " 200 "}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previous
		common.OptionMapRWMutex.Unlock()
	})
	policy := GetReferralPolicy()
	require.Equal(t, 35, policy.PenaltyPercent)
	require.Equal(t, 200, policy.MinTopUpQuota)
}
