package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestPendingTopUpCreditAuditAllSettlementEntrypoints(t *testing.T) {
	cases := []struct {
		name, provider string
		settle         func(TopUp, ExternalTopUpSettlement) error
	}{
		{"external", PaymentProviderStripe, func(_ TopUp, s ExternalTopUpSettlement) error { _, err := CompleteExternalTopUp(s); return err }},
		{"manual", PaymentProviderStripe, func(o TopUp, _ ExternalTopUpSettlement) error { return ManualCompleteTopUp(o.TradeNo, "") }},
		{"epay", PaymentProviderEpay, func(o TopUp, _ ExternalTopUpSettlement) error {
			_, err := RechargeEpay(o.TradeNo, "alipay", "")
			return err
		}},
		{"stripe", PaymentProviderStripe, func(o TopUp, _ ExternalTopUpSettlement) error { return Recharge(o.TradeNo, "", "") }},
		{"creem", PaymentProviderCreem, func(o TopUp, _ ExternalTopUpSettlement) error { return RechargeCreem(o.TradeNo, "", "", "") }},
		{"waffo", PaymentProviderWaffo, func(o TopUp, _ ExternalTopUpSettlement) error { return RechargeWaffo(o.TradeNo, "") }},
		{"pancake", PaymentProviderWaffoPancake, func(o TopUp, _ ExternalTopUpSettlement) error { return RechargeWaffoPancake(o.TradeNo) }},
	}
	for _, tc := range cases {
		for _, mode := range []string{"valid", "lost_metadata", "lost_parent", "wrong_owner", "wallet_failure"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				db := setupExternalTopUpSettlementDB(t, 1)
				user, order, settlement := createSettlementFixture(t, db, "parent-"+tc.name+"-"+mode)
				order.PaymentProvider = tc.provider
				if tc.name == "epay" {
					order.PaymentMethod = "epay"
					order.SettlementCurrency = "CNY"
				}
				order.PendingCreditRebaseKey = "migration-test"
				order.PendingCreditRebaseOriginalQuota = 1_234
				order.PendingCreditRebaseEffectiveQuota = 184
				require.NoError(t, db.Save(&order).Error)
				plan := seedPendingTopUpCreditAudit(t, db, order)
				switch mode {
				case "lost_metadata":
					require.NoError(t, db.Model(&order).Updates(map[string]interface{}{
						"pending_credit_rebase_key": "", "pending_credit_rebase_original_quota": 0, "pending_credit_rebase_effective_quota": 0,
					}).Error)
				case "lost_parent":
					require.NoError(t, db.Exec("DELETE FROM wallet_credit_rebases").Error)
				case "wrong_owner":
					base := plan["pending_bases"].([]interface{})[0].(map[string]interface{})
					base["user_id"] = user.Id + 1
					require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", common.GetJsonString(plan)).Error)
				case "wallet_failure":
					require.NoError(t, db.Exec(`CREATE TRIGGER pending_wallet_failure BEFORE UPDATE OF quota ON users
						WHEN NEW.quota > OLD.quota BEGIN SELECT RAISE(ABORT, 'forced_pending_wallet_failure'); END`).Error)
				}
				var before TopUp
				require.NoError(t, db.First(&before, order.Id).Error)
				err := tc.settle(order, settlement)
				if mode == "valid" {
					require.NoError(t, err)
					// Legacy Stripe/Creem reject success replays, while the other
					// entries return success. Both retain one durable wallet credit.
					_ = tc.settle(order, settlement)
					require.NoError(t, db.First(&user, user.Id).Error)
					require.EqualValues(t, 284, user.Quota)
					require.NoError(t, db.First(&order, order.Id).Error)
					require.EqualValues(t, 184, order.CreditedQuota)
					require.Equal(t, common.TopUpStatusSuccess, order.Status)
					if tc.name == "epay" {
						require.Equal(t, "alipay", order.PaymentMethod, "verified actual method can replace the quoted provider method")
					}
				} else {
					require.Error(t, err)
					require.NoError(t, db.First(&user, user.Id).Error)
					require.EqualValues(t, 100, user.Quota)
					var after TopUp
					require.NoError(t, db.First(&after, order.Id).Error)
					require.Equal(t, before, after, "failed audit/credit leaves quote and evidence retryable")
					if mode == "wallet_failure" {
						require.NoError(t, db.Exec("DROP TRIGGER pending_wallet_failure").Error)
						require.NoError(t, tc.settle(order, settlement))
						require.NoError(t, db.First(&user, user.Id).Error)
						require.EqualValues(t, 284, user.Quota)
					}
				}
			})
		}
	}
}

func TestPendingTopUpCreditAuditRejectsCorruptParentOrQuote(t *testing.T) {
	for _, mode := range []string{"id", "source_id", "owner", "migration_id", "target", "source_quote", "source_money", "method_drift", "failure_reason", "duplicate", "missing_source_field", "missing_inventory", "malformed", "missing_table"} {
		t.Run(mode, func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			user, order, settlement := createSettlementFixture(t, db, "corrupt-parent-"+mode)
			order.PendingCreditRebaseKey = "migration-test"
			order.PendingCreditRebaseOriginalQuota, order.PendingCreditRebaseEffectiveQuota = 1_234, 184
			require.NoError(t, db.Save(&order).Error)
			plan := seedPendingTopUpCreditAudit(t, db, order)
			base := plan["pending_bases"].([]interface{})[0].(map[string]interface{})
			source := base["source"].(map[string]interface{})
			switch mode {
			case "id":
				base["top_up_id"] = order.Id + 1
			case "source_id":
				source["id"] = order.Id + 1
			case "owner":
				base["user_id"], source["user_id"] = user.Id+1, user.Id+1
			case "migration_id":
				plan["migration_id"] = "other-migration"
			case "target":
				base["effective_credited_quota"] = 185
			case "source_quote":
				source["credited_quota"] = 1_235
			case "source_money":
				source["money"] = "NaN"
			case "duplicate":
				plan["pending_bases"] = []interface{}{base, base}
			case "missing_source_field":
				delete(source, "amount")
			case "missing_inventory":
				delete(plan, "pending_bases")
				require.NoError(t, db.Model(&order).Updates(map[string]interface{}{
					"pending_credit_rebase_key": "", "pending_credit_rebase_original_quota": 0, "pending_credit_rebase_effective_quota": 0,
				}).Error)
			case "method_drift":
				require.NoError(t, db.Model(&order).Update("payment_method", "balance").Error)
			case "failure_reason":
				require.NoError(t, db.Model(&order).Update("failure_reason_code", "unbound_failure_reason").Error)
			}
			if mode == "missing_table" {
				require.NoError(t, db.Exec("DROP TABLE wallet_credit_rebases").Error)
			} else if mode == "malformed" {
				require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = 'invalid-json'").Error)
			} else {
				require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", common.GetJsonString(plan)).Error)
			}
			_, err := CompleteExternalTopUp(settlement)
			require.ErrorIs(t, err, ErrInvalidTopUpQuota)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.EqualValues(t, 100, user.Quota)
			require.NoError(t, db.First(&order, order.Id).Error)
			require.Equal(t, common.TopUpStatusPending, order.Status)
		})
	}
}

func TestPendingTopUpCreditAuditNewOrderKeepsQuotedGrant(t *testing.T) {
	for _, otherAudit := range []bool{false, true} {
		t.Run(fmt.Sprint(otherAudit), func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			if otherAudit {
				_, old, _ := createSettlementFixture(t, db, "other-migrated-order")
				old.PendingCreditRebaseKey = "migration-test"
				old.PendingCreditRebaseOriginalQuota, old.PendingCreditRebaseEffectiveQuota = 1_234, 184
				require.NoError(t, db.Save(&old).Error)
				seedPendingTopUpCreditAudit(t, db, old)
			}
			user, _, settlement := createSettlementFixture(t, db, "normal-new-order")
			_, err := CompleteExternalTopUp(settlement)
			require.NoError(t, err)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.EqualValues(t, 1_334, user.Quota)
		})
	}
}

func TestPendingTopUpCreditAuditTimeoutThenLatePayment(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, order, settlement := createSettlementFixture(t, db, "audit-late-timeout")
	order.PaymentProvider, settlement.PaymentProvider = PaymentProviderWaffoPancake, PaymentProviderWaffoPancake
	order.PendingCreditRebaseKey = "migration-test"
	order.PendingCreditRebaseOriginalQuota, order.PendingCreditRebaseEffectiveQuota = 1_234, 184
	require.NoError(t, db.Save(&order).Error)
	seedPendingTopUpCreditAudit(t, db, order)
	require.NoError(t, FailPendingTopUpForCheckout(order.TradeNo, PaymentProviderWaffoPancake, PaymentOrderFailureCheckoutTimeout))
	_, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	_, err = CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.EqualValues(t, 284, user.Quota)
	require.NoError(t, db.First(&order, order.Id).Error)
	require.EqualValues(t, 184, order.CreditedQuota)
	require.Equal(t, common.TopUpStatusSuccess, order.Status)
}

func TestPendingTopUpCreditAuditRecoverableFailureInOriginalSnapshot(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, order, settlement := createSettlementFixture(t, db, "audit-original-timeout")
	order.PaymentProvider, settlement.PaymentProvider = PaymentProviderWaffoPancake, PaymentProviderWaffoPancake
	order.Status = common.TopUpStatusFailed
	order.FailureReasonCode = string(PaymentOrderFailureCheckoutTimeout)
	order.PendingCreditRebaseKey = "migration-test"
	order.PendingCreditRebaseOriginalQuota, order.PendingCreditRebaseEffectiveQuota = 1_234, 184
	require.NoError(t, db.Save(&order).Error)
	seedPendingTopUpCreditAudit(t, db, order)
	_, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.NoError(t, db.First(&user, user.Id).Error)
	require.EqualValues(t, 284, user.Quota)
}

func TestBlockedPendingTopUpCreditAuditAlwaysRejectsSettlement(t *testing.T) {
	for _, lostMetadata := range []bool{false, true} {
		t.Run(fmt.Sprint(lostMetadata), func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			user, order, settlement := createSettlementFixture(t, db, "blocked-old-order")
			order.PendingCreditRebaseKey = "migration-test"
			order.PendingCreditRebaseOriginalQuota, order.PendingCreditRebaseEffectiveQuota = 0, 0
			require.NoError(t, db.Save(&order).Error)
			plan := seedPendingTopUpCreditAudit(t, db, order)
			base := plan["pending_bases"].([]interface{})[0].(map[string]interface{})
			base["source"].(map[string]interface{})["effective_credited_quota"] = 0
			base["reason"] = "authority_zero_not_settleable"
			base["future_settlement"] = "blocked_until_separate_audited_payment_reconciliation"
			plan["pending_bases"] = []interface{}{}
			plan["blocked_pending_bases"] = []interface{}{base}
			require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", common.GetJsonString(plan)).Error)
			newUser, _, newSettlement := createSettlementFixture(t, db, "new-with-blocked-legacy-audit")
			_, err := CompleteExternalTopUp(newSettlement)
			require.NoError(t, err, "an unrelated blocked legacy order does not block a new paid order")
			require.NoError(t, db.First(&newUser, newUser.Id).Error)
			require.EqualValues(t, 1_334, newUser.Quota)
			if lostMetadata {
				require.NoError(t, db.Model(&order).Update("pending_credit_rebase_key", "").Error)
			}
			_, err = CompleteExternalTopUp(settlement)
			require.ErrorIs(t, err, ErrInvalidTopUpQuota)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.EqualValues(t, 100, user.Quota)
			require.NoError(t, db.First(&order, order.Id).Error)
			require.Equal(t, common.TopUpStatusPending, order.Status)
		})
	}
}
