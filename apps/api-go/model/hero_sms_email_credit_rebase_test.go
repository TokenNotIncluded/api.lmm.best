package model

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupEmailCreditRebaseFixture(t *testing.T) (*gorm.DB, User, HeroSMSEmailOrder) {
	t.Helper()
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 1, 149)
	order := HeroSMSEmailOrder{ID: "rebased-email", UserID: user.Id, ChargeQuota: 100, RefundedQuota: 20,
		Quantity: 2, CreatedAt: 10, Status: HeroSMSEmailOrderStatusCompleted,
		Operation: "purchase", IdempotencyKeyHash: "rebase-email", RequestPayloadHash: "rebase-payload"}
	require.NoError(t, db.Create(&order).Error)
	old := HeroSMSEmailQuotaLedger{UserID: user.Id, OrderID: order.ID, EntryType: HeroSMSEmailLedgerRefund,
		AmountQuota: 20, IdempotencyKey: "old-refund", CreatedAt: 11}
	require.NoError(t, db.Create(&old).Error)
	require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY, plan TEXT NOT NULL)").Error)
	source, err := json.Marshal(map[string]any{"id": order.ID, "user_id": user.Id, "charge_quota": 100,
		"refunded_quota": 20, "created_at": 10, "last_refund_ledger_id": old.ID})
	require.NoError(t, err)
	plan, err := json.Marshal(map[string]any{"user_ids": []int{user.Id}, "snapshot_at": 100,
		"include_other_rights": true, "divisor": "10", "rounding": "half-away-from-zero",
		"other_credit_bases": []walletFutureCreditBasis{{Kind: "email_refund_pool", SourceID: order.ID,
			UserID: user.Id, OriginalQuota: 80, RebasedQuota: 8, Source: source}}})
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases VALUES (?,?)", "email-migration", string(plan)).Error)
	return db, user, order
}

func TestEmailCreditRebaseCumulativePartialRefundPreservesSourceFacts(t *testing.T) {
	db, user, order := setupEmailCreditRebaseFixture(t)
	activation := HeroSMSEmailActivation{ID: "rebased-activation", OrderID: order.ID, UserID: user.Id}
	refund := func(quota int, key string) error {
		return db.Transaction(func(tx *gorm.DB) error {
			return heroSMSRefundActivationTx(tx, &order, &activation, quota, key)
		})
	}
	require.NoError(t, refund(5, "first")) // 8 * 5/80 is rounded once to 1.
	require.NoError(t, refund(5, "first"))
	require.NoError(t, refund(15, "second"))
	require.NoError(t, refund(60, "last"))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 157, user.Quota)
	require.NoError(t, db.First(&order, "id = ?", order.ID).Error)
	require.Equal(t, 100, order.ChargeQuota)
	require.Equal(t, 100, order.RefundedQuota)
	var entries []HeroSMSEmailQuotaLedger
	require.NoError(t, db.Order("id").Find(&entries).Error)
	require.Len(t, entries, 4)
	require.Equal(t, []int{20, 1, 1, 6}, []int{entries[0].AmountQuota, entries[1].AmountQuota, entries[2].AmountQuota, entries[3].AmountQuota})
	require.Nil(t, entries[0].OriginalAmountQuota)
	require.Equal(t, 5, *entries[1].OriginalAmountQuota)
	require.Equal(t, 15, *entries[2].OriginalAmountQuota)
	require.Equal(t, 60, *entries[3].OriginalAmountQuota)
}

func TestEmailCreditRebaseZeroRoundedRefundKeepsExactSourceCounter(t *testing.T) {
	db, user, order := setupEmailCreditRebaseFixture(t)
	refund := func(quota int, key string) error {
		return db.Transaction(func(tx *gorm.DB) error { return heroSMSRefundOrderTx(tx, &order, quota, key) })
	}
	require.NoError(t, refund(1, "rounded-zero"))
	require.NoError(t, refund(1, "rounded-zero"))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 149, user.Quota)
	var entry HeroSMSEmailQuotaLedger
	require.NoError(t, db.Where("idempotency_key LIKE ?", "%:rounded-zero").First(&entry).Error)
	require.Zero(t, entry.AmountQuota)
	require.Equal(t, 1, *entry.OriginalAmountQuota)
	require.NoError(t, db.Model(&HeroSMSEmailOrder{}).Where("id = ?", order.ID).UpdateColumn("refunded_quota", 22).Error)
	require.Error(t, refund(1, "changed-zero-counter"))
	require.NoError(t, db.Model(&HeroSMSEmailOrder{}).Where("id = ?", order.ID).UpdateColumn("refunded_quota", 21).Error)
	require.NoError(t, refund(79, "rest"))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, 157, user.Quota)
}

func TestEmailCreditRebaseMissingOrChangedBasisRollsBack(t *testing.T) {
	for _, mutation := range []string{
		"UPDATE wallet_credit_rebases SET plan=json_set(plan,'$.other_credit_bases',json('[]'))",
		"UPDATE hero_sms_email_orders SET charge_quota=101",
		"UPDATE hero_sms_email_orders SET refunded_quota=21",
		"UPDATE hero_sms_email_quota_ledgers SET amount_quota=21",
	} {
		t.Run(mutation, func(t *testing.T) {
			db, user, order := setupEmailCreditRebaseFixture(t)
			require.NoError(t, db.Exec(mutation).Error)
			err := db.Transaction(func(tx *gorm.DB) error {
				return heroSMSRefundOrderTx(tx, &order, 5, "invalid")
			})
			require.Error(t, err)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.Equal(t, 149, user.Quota)
			var count int64
			require.NoError(t, db.Model(&HeroSMSEmailQuotaLedger{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestEmailCreditRebaseWalletOverflowRollsBackCountersAndLedger(t *testing.T) {
	db, user, order := setupEmailCreditRebaseFixture(t)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", common.MaxWalletQuota).Error)
	err := db.Transaction(func(tx *gorm.DB) error { return heroSMSRefundOrderTx(tx, &order, 80, "overflow") })
	require.Error(t, err)
	require.NoError(t, db.First(&order, "id = ?", order.ID).Error)
	require.Equal(t, 20, order.RefundedQuota)
	var count int64
	require.NoError(t, db.Model(&HeroSMSEmailQuotaLedger{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
