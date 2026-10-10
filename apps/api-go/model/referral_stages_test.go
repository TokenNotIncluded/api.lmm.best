package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReferralCashThresholdUsesActualPayment(t *testing.T) {
	policy := ReferralPolicy{MinTopUpAmounts: map[string]string{"USD": "10", "CNY": "70"}}
	for _, tc := range []struct {
		currency string
		micros   int64
		want     bool
	}{
		{"USD", 9_999_999, false}, {"USD", 10_000_000, true}, {"usd", 10_000_001, true},
		{"CNY", 69_999_999, false}, {"CNY", 70_000_000, true}, {"EUR", 100_000_000, false}, {"USD", 0, false},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.currency, tc.micros), func(t *testing.T) {
			require.Equal(t, tc.want, referralCashMinimumMet(policy, &TopUp{SettlementCurrency: tc.currency, SettledAmountMicros: tc.micros, CreditedQuota: common.MaxWalletQuota}))
		})
	}
	require.False(t, referralCashMinimumMet(policy, nil))
	for _, raw := range []string{`{}`, `{"usd":"10"}`, `{"USD":0}`, `{"USD":"-1"}`, `{"USD":"1e2"}`, `{"USD":"1.0000001"}`, `{"USD":"10","USD":"1"}`, `{"USD":null}`, `{"USD":true}`, `{"USD":"10"} {}`} {
		_, err := parseReferralMinTopUpAmounts(raw)
		require.Error(t, err, raw)
	}
	amounts, err := parseReferralMinTopUpAmounts(`{"USD":10,"CNY":"70.000000"}`)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"USD": "10", "CNY": "70"}, amounts)
}

func registerReferralStageInvitee(t *testing.T, db *gorm.DB, inviter User, name string) User {
	t.Helper()
	user := User{Username: name, AffCode: name, Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return createUserWithInviterTx(tx, &user, inviter.Id) }))
	return user
}

func TestReferralStagesGrantAdvanceThenTailExactlyOnce(t *testing.T) {
	db, _, inviter, _, order, payment := setupReferralTest(t)
	common.OptionMap["ReferralRegistrationRewardQuota"] = "100"
	invitee := registerReferralStageInvitee(t, db, inviter, "staged-invitee")
	require.Equal(t, 100, referralUser(t, db, inviter.Id).AffQuota)
	order.UserId = invitee.Id
	require.NoError(t, db.Save(&order).Error)
	// The promised tail and floor are snapshotted with the advance.
	common.QuotaForInviter = 7
	common.OptionMap["ReferralMinTopUpAmounts"] = `{"USD":"100"}`
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	_, err = CompleteExternalTopUp(payment)
	require.NoError(t, err)
	_, err = CompleteExternalTopUp(nextReferralPayment(t, db, order, payment, "staged-second"))
	require.NoError(t, err)
	got := referralUser(t, db, inviter.Id)
	require.Equal(t, 1_000_100, got.AffQuota)
	require.Equal(t, 1_000_100, got.AffHistoryQuota)
	require.Equal(t, 777, got.Quota)
	var reward ReferralReward
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&reward).Error)
	require.Equal(t, referralStageCompleted, reward.Stage)
	require.Equal(t, 100, reward.RegistrationQuota)
	require.NotNil(t, reward.TopUpId)
	require.Equal(t, order.Id, *reward.TopUpId)
	var entries []ReferralLedgerEntry
	require.NoError(t, db.Order("id").Find(&entries).Error)
	require.Len(t, entries, 2)
	require.Equal(t, "registration_reward", entries[0].Kind)
	require.Equal(t, "reward", entries[1].Kind)
}

func TestReferralStagesBelowCashMinimumNeverCatchUp(t *testing.T) {
	db, _, inviter, _, order, payment := setupReferralTest(t)
	common.OptionMap["ReferralRegistrationRewardQuota"] = "100"
	invitee := registerReferralStageInvitee(t, db, inviter, "below-cash-floor")
	order.UserId = invitee.Id
	order.ExpectedAmountMicros = 9_999_999
	order.Money = 9.999999
	// Generous platform credit does not replace the real cash floor.
	order.CreditedQuota = 9_000_000
	require.NoError(t, db.Save(&order).Error)
	payment.SettledAmountMicros = order.ExpectedAmountMicros
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	order.ExpectedAmountMicros = 12_340_000
	order.Money = 12.34
	payment.SettledAmountMicros = 12_340_000
	_, err = CompleteExternalTopUp(nextReferralPayment(t, db, order, payment, "below-then-larger"))
	require.NoError(t, err)
	require.Equal(t, 100, referralUser(t, db, inviter.Id).AffQuota)
	require.Equal(t, order.Id, referralUser(t, db, invitee.Id).ReferralFirstTopUpId)
	var reward ReferralReward
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&reward).Error)
	require.Equal(t, referralStageIneligible, reward.Stage)
}

func TestReferralStagesTotalCapAndFullRefund(t *testing.T) {
	db, actor, inviter, _, order, payment := setupReferralTest(t)
	common.OptionMap["ReferralRegistrationRewardQuota"] = "100"
	common.OptionMap["ReferralMaxRewardQuota"] = "250"
	invitee := registerReferralStageInvitee(t, db, inviter, "capped-stages")
	order.UserId = invitee.Id
	require.NoError(t, db.Save(&order).Error)
	_, err := CompleteExternalTopUp(payment)
	require.NoError(t, err)
	require.Equal(t, 250, referralUser(t, db, inviter.Id).AffQuota)
	_, err = ApplyPaymentRefund(order.TradeNo, false, payment.SettledAmountMicros, "USD", "stages-full-refund", PaymentMethodStripe, PaymentProviderStripe, "refund", actor.Id)
	require.NoError(t, err)
	got := referralUser(t, db, inviter.Id)
	require.Zero(t, got.AffQuota)
	require.Equal(t, 777, got.Quota)
	require.Equal(t, 250, got.AffHistoryQuota)
	var reward ReferralReward
	require.NoError(t, db.Where("invitee_id = ?", invitee.Id).First(&reward).Error)
	require.Equal(t, 250, reward.RevokedQuota)
	require.Equal(t, "refund", reward.Reason)
}

func TestReferralRegistrationRollbackAndMultipleUnpaidAbuseCases(t *testing.T) {
	db, actor, inviter, _, _, _ := setupReferralTest(t)
	common.OptionMap["ReferralRegistrationRewardQuota"] = "100"
	first := registerReferralStageInvitee(t, db, inviter, "unpaid-first")
	second := registerReferralStageInvitee(t, db, inviter, "unpaid-second")
	require.NoError(t, ModerateReferralUser(referralBan(actor, first, "unpaid-case-one", false)))
	require.NoError(t, ModerateReferralUser(referralBan(actor, second, "unpaid-case-two", false)))
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
	var unpaid int64
	require.NoError(t, db.Model(&ReferralReward{}).Where("top_up_id IS NULL").Count(&unpaid).Error)
	require.EqualValues(t, 2, unpaid, "saving an unpaid reward must keep NULL, not a shared zero order ID")
	rollback := errors.New("rollback user creation")
	user := User{Username: "rollback-stage", AffCode: "rollback-stage", Status: common.UserStatusEnabled}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := createUserWithInviterTx(tx, &user, inviter.Id); err != nil {
			return err
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	require.Zero(t, referralUser(t, db, inviter.Id).AffQuota)
	var count int64
	require.NoError(t, db.Model(&ReferralReward{}).Where("invitee_id = ?", user.Id).Count(&count).Error)
	require.Zero(t, count)
}

// Model the old required order ID and check the actual upgrade, not only a fresh DB.
type referralRequiredOrderFixture struct {
	Id        int
	InviteeId int    `gorm:"uniqueIndex;not null"`
	InviterId int    `gorm:"index;not null"`
	TopUpId   int    `gorm:"uniqueIndex;not null"`
	Quota     int    `gorm:"type:bigint;not null"`
	Status    string `gorm:"type:varchar(24);not null"`
}

func (referralRequiredOrderFixture) TableName() string { return "referral_rewards" }

func TestReferralStagesUpgradeAllowsUnpaidRows(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	checkReferralStagesUpgrade(t, db)
}

func checkReferralStagesUpgrade(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&referralRequiredOrderFixture{}))
	require.NoError(t, db.Create(&referralRequiredOrderFixture{InviteeId: 1, InviterId: 9, TopUpId: 7, Quota: 10, Status: "earned"}).Error)
	require.NoError(t, migrateReferralOrderNullability(db))
	require.NoError(t, db.AutoMigrate(&ReferralReward{}))
	require.NoError(t, migrateReferralOrderNullability(db))
	for _, id := range []int{2, 3} {
		reward := ReferralReward{InviteeId: id, InviterId: 9, Quota: 1, Status: "earned"}
		require.NoError(t, db.Create(&reward).Error)
		reward.Status = "revoked"
		require.NoError(t, db.Save(&reward).Error)
	}
	var historical ReferralReward
	require.NoError(t, db.Where("invitee_id = ?", 1).First(&historical).Error)
	require.NotNil(t, historical.TopUpId)
	require.Equal(t, 7, *historical.TopUpId)
	require.Equal(t, 10, historical.Quota)
	require.True(t, db.Migrator().HasIndex(&ReferralReward{}, "idx_referral_rewards_top_up_id"))
	require.Error(t, db.Create(&ReferralReward{InviteeId: 4, InviterId: 9, TopUpId: referralTestTopUpID(7), Quota: 1, Status: "earned"}).Error)
	require.Error(t, db.Create(&ReferralReward{InviteeId: 1, InviterId: 9, Quota: 1, Status: "earned"}).Error)
}
