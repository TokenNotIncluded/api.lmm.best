package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func pendingOrphanCreditFixture(t *testing.T) (*gorm.DB, User, TopUp, ExternalTopUpSettlement, map[string]interface{}) {
	t.Helper()
	db := setupExternalTopUpSettlementDB(t, 1)
	user, order, settlement := createSettlementFixture(t, db, "orphan-quote")
	order.PaymentProvider, settlement.PaymentProvider = PaymentProviderWaffoPancake, PaymentProviderWaffoPancake
	order.Status = common.TopUpStatusFailed
	order.FailureReasonCode = string(PaymentOrderFailureCheckoutTimeout)
	order.PendingCreditRebaseKey = "migration-test"
	order.PendingCreditRebaseOriginalQuota, order.PendingCreditRebaseEffectiveQuota = 1_234, 184
	require.NoError(t, db.Save(&order).Error)
	plan := seedPendingTopUpCreditAudit(t, db, order)
	base := plan["pending_bases"].([]interface{})[0].(map[string]interface{})
	base["owner_missing_at_snapshot"] = true
	plan["user_ids"] = []int{}
	plan["orphan_pending_user_ids"] = []int{user.Id}
	require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", common.GetJsonString(plan)).Error)
	require.NoError(t, db.Unscoped().Delete(&user).Error)
	return db, user, order, settlement, plan
}

func TestPendingOrphanCreditRestoredOwnerReceivesCorrectedGrantOnce(t *testing.T) {
	db, user, before, settlement, _ := pendingOrphanCreditFixture(t)
	_, err := CompleteExternalTopUp(settlement)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound, "the audit cannot create or bypass a missing account")
	var unchanged TopUp
	require.NoError(t, db.First(&unchanged, before.Id).Error)
	require.Equal(t, before, unchanged, "a failed wallet write rolls back settlement and payment evidence")
	// Account restoration preserves the original user ID and is independent
	// from payment settlement; the audit remains frozen at the original state.
	require.NoError(t, db.Create(&user).Error)
	completed, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.EqualValues(t, 184, completed.CreditedQuota)
	require.EqualValues(t, before.ExpectedAmountMicros, completed.SettledAmountMicros)
	require.Equal(t, before.Money, completed.Money)
	require.EqualValues(t, before.PendingCreditRebaseOriginalQuota, completed.PendingCreditRebaseOriginalQuota)
	_, err = CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.EqualValues(t, 284, user.Quota)
}

func TestPendingOrphanCreditRejectsInventoryOrSourceConflict(t *testing.T) {
	for _, mode := range []string{"missing_users", "missing_orphan", "unused_orphan", "overlap", "duplicate_user", "duplicate_orphan", "unflagged", "null_flag", "string_flag", "pending_source", "settled_source", "refunded_source", "provider_source", "lost_metadata"} {
		t.Run(mode, func(t *testing.T) {
			db, user, before, settlement, plan := pendingOrphanCreditFixture(t)
			require.NoError(t, db.Create(&user).Error)
			base := plan["pending_bases"].([]interface{})[0].(map[string]interface{})
			source := base["source"].(map[string]interface{})
			switch mode {
			case "missing_users":
				delete(plan, "user_ids")
			case "missing_orphan":
				plan["orphan_pending_user_ids"] = []int{}
			case "unused_orphan":
				plan["orphan_pending_user_ids"] = []int{user.Id, user.Id + 1}
			case "overlap":
				plan["user_ids"] = []int{user.Id}
			case "duplicate_user":
				plan["user_ids"] = []int{user.Id + 1, user.Id + 1}
			case "duplicate_orphan":
				plan["orphan_pending_user_ids"] = []int{user.Id, user.Id}
			case "unflagged":
				delete(base, "owner_missing_at_snapshot")
			case "null_flag":
				base["owner_missing_at_snapshot"] = nil
			case "string_flag":
				base["owner_missing_at_snapshot"] = "true"
			case "pending_source":
				source["status"], source["failure_reason_code"] = common.TopUpStatusPending, ""
			case "settled_source":
				source["settled_amount_micros"] = before.ExpectedAmountMicros
			case "refunded_source":
				source["refunded_quota"] = 1
			case "provider_source":
				source["payment_provider"] = PaymentProviderStripe
			case "lost_metadata":
				require.NoError(t, db.Model(&before).Updates(map[string]interface{}{"pending_credit_rebase_key": "", "pending_credit_rebase_original_quota": 0, "pending_credit_rebase_effective_quota": 0}).Error)
				require.NoError(t, db.First(&before, before.Id).Error)
			}
			require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", common.GetJsonString(plan)).Error)
			_, err := CompleteExternalTopUp(settlement)
			require.ErrorIs(t, err, ErrInvalidTopUpQuota)
			var unchanged TopUp
			require.NoError(t, db.First(&unchanged, before.Id).Error)
			require.Equal(t, before, unchanged)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.EqualValues(t, 100, user.Quota)
		})
	}
}

func TestPendingNormalCreditRequiresParentOwnerMembership(t *testing.T) {
	for _, explicitFalse := range []bool{false, true} {
		t.Run(common.GetJsonString(explicitFalse), func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			user, order, settlement := createSettlementFixture(t, db, common.GetJsonString(explicitFalse))
			order.PendingCreditRebaseKey = "migration-test"
			order.PendingCreditRebaseOriginalQuota, order.PendingCreditRebaseEffectiveQuota = 1_234, 184
			require.NoError(t, db.Save(&order).Error)
			plan := seedPendingTopUpCreditAudit(t, db, order)
			if explicitFalse {
				plan["pending_bases"].([]interface{})[0].(map[string]interface{})["owner_missing_at_snapshot"] = false
			}
			plan["user_ids"] = []int{}
			require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", common.GetJsonString(plan)).Error)
			_, err := CompleteExternalTopUp(settlement)
			require.ErrorIs(t, err, ErrInvalidTopUpQuota)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.EqualValues(t, 100, user.Quota)
			plan["user_ids"] = []int{user.Id}
			require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", common.GetJsonString(plan)).Error)
			_, err = CompleteExternalTopUp(settlement)
			require.NoError(t, err)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.EqualValues(t, 284, user.Quota)
		})
	}
}
