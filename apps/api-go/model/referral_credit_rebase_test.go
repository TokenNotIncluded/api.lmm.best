package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"math/big"
	"testing"
)

func TestReferralCreditRebaseRevokeRestoreAndTransfer(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &ReferralReward{}, &ReferralLedgerEntry{}, &WalletReferralCreditRebase{}))
	user := User{Username: "rebased-referral", Password: "password", AffQuota: 1_000_000, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	reward := ReferralReward{InviterId: user.Id, InviteeId: 99, TopUpId: 99, Quota: 6_800_000, Status: "earned", PenaltyPercent: 10}
	require.NoError(t, db.Create(&reward).Error)
	base := WalletReferralCreditRebase{RewardID: reward.Id, UserID: user.Id, MigrationID: "test", OriginalQuota: 6_800_000, Divisor: "6.8", Rounding: "half-away-from-zero", RebasedQuota: 1_000_000}
	require.NoError(t, db.Create(&base).Error)
	require.NoError(t, user.TransferAffQuotaToQuota(1_000_000))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return revokeReferralTx(tx, &reward, "abuse", true) }))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, -1_100_000, user.AffQuota)
	require.Equal(t, 1_000_000, user.Quota)
	require.Equal(t, 6_800_000, reward.Quota)
	require.Equal(t, 6_800_000, reward.RevokedQuota)
	require.Equal(t, 680_000, reward.PenaltyQuota)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return restoreReferralTx(tx, &reward) }))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Zero(t, user.AffQuota)
	require.Equal(t, 1_000_000, user.Quota)
	require.NoError(t, db.First(&base, "reward_id = ?", reward.Id).Error)
	require.Zero(t, base.RebasedRevokedQuota)
	require.Zero(t, base.RebasedPenaltyQuota)
	var ledger []ReferralLedgerEntry
	require.NoError(t, db.Order("id").Find(&ledger).Error)
	require.Len(t, ledger, 4)
	require.Equal(t, []int{-1_000_000, -100_000, 1_000_000, 100_000}, []int{ledger[0].Quota, ledger[1].Quota, ledger[2].Quota, ledger[3].Quota})
}
func TestReferralCreditRebasePreviouslyRevokedRestoresScaledCredit(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&User{}, &ReferralReward{}, &ReferralLedgerEntry{}, &WalletReferralCreditRebase{}))
	user := User{Username: "old-revocation", Password: "password", AffQuota: -100_000, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	reward := ReferralReward{InviterId: user.Id, InviteeId: 99, TopUpId: 99, Quota: 6_800_000, RevokedQuota: 6_800_000, PenaltyQuota: 680_000, Status: "revoked", Reason: "abuse", PenaltyPercent: 10}
	require.NoError(t, db.Create(&reward).Error)
	base := WalletReferralCreditRebase{RewardID: reward.Id, UserID: user.Id, MigrationID: "test", OriginalQuota: 6_800_000, Divisor: "6.8", Rounding: "half-away-from-zero", RebasedQuota: 1_000_000, RebasedRevokedQuota: 1_000_000, RebasedPenaltyQuota: 100_000}
	require.NoError(t, db.Create(&base).Error)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return restoreReferralTx(tx, &reward) }))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 1_000_000, user.AffQuota)
	require.Equal(t, 6_800_000, reward.Quota)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return revokeReferralTx(tx, &reward, "refund", false) }))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Zero(t, user.AffQuota)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return restoreReferralTx(tx, &reward) }))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Zero(t, user.AffQuota, "refunded reward must never be restored")
}
func TestReferralCreditRebaseMissingBaselineFailsClosed(t *testing.T) {
	for _, childTable := range []bool{false, true} {
		t.Run(map[bool]string{false: "table", true: "row"}[childTable], func(t *testing.T) {
			db := setupConsoleActivationTestDB(t)
			require.NoError(t, db.AutoMigrate(&User{}, &ReferralReward{}, &ReferralLedgerEntry{}))
			if childTable {
				require.NoError(t, db.AutoMigrate(&WalletReferralCreditRebase{}))
			}
			require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY, plan TEXT NOT NULL)").Error)
			require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases VALUES (?, ?)", "test", `{"user_ids":[1],"include_affiliate":true,"referral_bases":[{"reward_id":1}]}`).Error)
			user := User{Id: 1, Username: "missing-referral-basis", Password: "password", AffQuota: 100, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			reward := ReferralReward{Id: 1, InviterId: 1, InviteeId: 99, TopUpId: 99, Quota: 680, Status: "earned"}
			require.NoError(t, db.Create(&reward).Error)
			err := db.Transaction(func(tx *gorm.DB) error { return revokeReferralTx(tx, &reward, "refund", false) })
			require.ErrorIs(t, err, ErrRefundAmountInvalid)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.Equal(t, 100, user.AffQuota)
			require.NoError(t, db.First(&reward, reward.Id).Error)
			require.Equal(t, "earned", reward.Status)
		})
	}
}

func TestReferralCreditRebaseRoundingPolicy(t *testing.T) {
	divisor := big.NewRat(68, 10)
	require.EqualValues(t, 1, scaleReferralCredit(4, divisor, "half-away-from-zero"))
	require.EqualValues(t, 0, scaleReferralCredit(4, divisor, "toward-zero"))
}
