package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const adminTopupTestMigration = "credit-financial-test"

func setupAdminUserTopupProjection(t *testing.T) (*gorm.DB, User) {
	t.Helper()
	previousScale := common.QuotaPerUnit
	common.QuotaPerUnit = 500_000
	t.Cleanup(func() { common.QuotaPerUnit = previousScale })
	db := setupExternalTopUpSettlementDB(t, 1)
	user := User{Id: 1, Username: "admin-topup-fixture", Password: "password", AffCode: "admin-topup-fixture", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 777, UsedQuota: 333}
	require.NoError(t, db.Create(&user).Error)
	return db, user
}

func adminTopupProjectionOrder(tradeNo string, credits int64) TopUp {
	return TopUp{
		UserId: 1, TradeNo: tradeNo, Amount: credits / 500_000,
		PlatformAmountMicros: credits * 2, CreditedQuota: credits,
		ExpectedAmountMicros: 10_000_000, SettledAmountMicros: 10_000_000,
		SettlementCurrency: "USD", Money: 10,
		PaymentMethod: PaymentMethodStripe, PaymentProvider: PaymentProviderStripe,
		CreateTime: 100, CompleteTime: 200, Status: common.TopUpStatusSuccess,
	}
}

func adminTopupProjectionSource(order TopUp) map[string]any {
	paid := order.SettledAmountMicros
	if paid == 0 {
		paid = order.ExpectedAmountMicros
	}
	return map[string]any{
		"id": order.Id, "user_id": order.UserId, "status": order.Status,
		"failure_reason_code": order.FailureReasonCode,
		"credited_quota":      order.CreditedQuota, "amount": order.Amount,
		"platform_amount_micros": order.PlatformAmountMicros,
		"expected_amount_micros": order.ExpectedAmountMicros, "settled_amount_micros": order.SettledAmountMicros,
		"refunded_quota": order.RefundedQuota, "refunded_amount_micros": order.RefundedAmountMicros,
		"money": common.GetJsonString(order.Money), "payment_provider": order.PaymentProvider,
		"payment_method": order.PaymentMethod, "settlement_currency": order.SettlementCurrency,
		"create_time": order.CreateTime, "complete_time": order.CompleteTime,
		"effective_credited_quota": order.CreditedQuota, "paid_amount_micros": paid,
		"is_legacy_linuxdo_credit_topup":        false,
		"pending_credit_rebase_key":             order.PendingCreditRebaseKey,
		"pending_credit_rebase_original_quota":  order.PendingCreditRebaseOriginalQuota,
		"pending_credit_rebase_effective_quota": order.PendingCreditRebaseEffectiveQuota,
	}
}

func adminTopupProjectionPlan() map[string]any {
	return map[string]any{
		"version": 1, "kind": "offline_credit_balance_rebase_preview",
		"migration_id": adminTopupTestMigration, "plan_sha256": strings.Repeat("a", 64),
		"usd_credit_conversion": 500_000, "snapshot_state": "frozen_writers_stopped",
		"has_complete_history": true, "snapshot_at": 1000,
		"divisor": "6.710363", "rounding": "half-away-from-zero",
		"exact_factor": map[string]any{"numerator": 1_000_000, "denominator": 6_710_363},
		"fx_source":    map[string]any{"kind": "frozen_production_option", "key": "USDExchangeRate", "value": "6.710363"},
		"user_ids":     []int{1}, "refund_bases": []any{}, "pending_bases": []any{},
		"blocked_pending_bases": []any{}, "noncash_topups": []any{},
		"include_pending_topups": true,
		"option_entries":         []any{map[string]any{"key": "QuotaPerUnit", "before": "500000", "after": "500000"}},
	}
}

func addAdminTopupRefundBasis(t *testing.T, db *gorm.DB, plan map[string]any, order TopUp, refundable int64) {
	t.Helper()
	base := WalletTopUpCreditRebase{
		TopUpID: order.Id, UserID: order.UserId, MigrationID: adminTopupTestMigration,
		OriginalCreditedQuota: order.CreditedQuota, OriginalRefundedQuota: order.RefundedQuota,
		OriginalRefundedAmountMicros: order.RefundedAmountMicros, OriginalPaidAmountMicros: order.SettledAmountMicros,
		RefundableQuota: refundable,
	}
	require.NoError(t, db.AutoMigrate(&WalletTopUpCreditRebase{}))
	require.NoError(t, db.Create(&base).Error)
	plan["refund_bases"] = append(plan["refund_bases"].([]any), map[string]any{
		"source": adminTopupProjectionSource(order), "top_up_id": order.Id, "user_id": order.UserId,
		"original_credited_quota": base.OriginalCreditedQuota, "original_refunded_quota": base.OriginalRefundedQuota,
		"original_refunded_amount_micros": base.OriginalRefundedAmountMicros, "original_paid_amount_micros": base.OriginalPaidAmountMicros,
		"refundable_quota": refundable, "rebased_debited_quota": int64(0),
	})
}

func putAdminTopupProjectionAudit(t *testing.T, db *gorm.DB, plan map[string]any) {
	t.Helper()
	require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY, plan_sha256 TEXT NOT NULL, plan TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT '2026-10-07T00:00:00Z')").Error)
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases (migration_id,plan_sha256,plan) VALUES (?,?,?)", adminTopupTestMigration, strings.Repeat("a", 64), string(encoded)).Error)
}

func adminTopupSummaryJSON(t *testing.T, summary *UserTopupSummary) map[string]any {
	t.Helper()
	require.NotNil(t, summary)
	encoded, err := json.Marshal(summary)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(encoded, &body))
	return body
}

func populateAdminTopupFixture(t *testing.T, userID int) (*UserTopupSummary, map[string]any) {
	t.Helper()
	users := []*User{{Id: userID}}
	require.NoError(t, PopulateUserTopups(users))
	return users[0].TopupSummary, adminTopupSummaryJSON(t, users[0].TopupSummary)
}

func TestAdminUserTopupProjectsOldAndNewCreditsWithoutCurrentFX(t *testing.T) {
	db, _ := setupAdminUserTopupProjection(t)
	old := adminTopupProjectionOrder("projection-old", 6_710_363)
	newOrder := adminTopupProjectionOrder("projection-new", 500_001)
	newOrder.CreateTime, newOrder.CompleteTime = 2000, 2001
	require.NoError(t, db.Create(&old).Error)
	require.NoError(t, db.Create(&newOrder).Error)
	plan := adminTopupProjectionPlan()
	addAdminTopupRefundBasis(t, db, plan, old, 1_000_000)
	putAdminTopupProjectionAudit(t, db, plan)
	previousFX := operation_setting.USDExchangeRate
	t.Cleanup(func() { operation_setting.USDExchangeRate = previousFX })
	for _, fx := range []float64{6.710363, 9.9} {
		operation_setting.USDExchangeRate = fx
		summary, body := populateAdminTopupFixture(t, 1)
		assert.EqualValues(t, 7_210_364, summary.Quota)
		assert.Equal(t, true, body["quota_projection_available"])
		assert.Equal(t, float64(1_500_001), body["normalized_quota"])
		require.Len(t, summary.Methods, 1)
		methods := body["methods"].([]any)
		assert.Equal(t, float64(1_500_001), methods[0].(map[string]any)["normalized_quota"])
		assert.Equal(t, true, methods[0].(map[string]any)["quota_projection_available"])
	}
}

func TestAdminUserTopupAnonymousTwoOrderProjectionPreserves154CNY(t *testing.T) {
	db, _ := setupAdminUserTopupProjection(t)
	plan := adminTopupProjectionPlan()
	for index, fixture := range []struct{ raw, normalized, paid int64 }{
		{5_000_000, 745_116, 10_000_000}, {100_000_000, 14_902_323, 144_000_000},
	} {
		order := adminTopupProjectionOrder([]string{"anonymous-small", "anonymous-large"}[index], fixture.raw)
		order.Id = 101 + index
		order.PaymentMethod, order.PaymentProvider = PaymentMethodWaffoPancake, PaymentProviderWaffoPancake
		order.SettlementCurrency = "CNY"
		order.ExpectedAmountMicros, order.SettledAmountMicros = fixture.paid, fixture.paid
		order.Money = float64(fixture.paid) / 1_000_000
		require.NoError(t, db.Create(&order).Error)
		addAdminTopupRefundBasis(t, db, plan, order, fixture.normalized)
	}
	putAdminTopupProjectionAudit(t, db, plan)
	var beforeOrders []TopUp
	var beforeBases []WalletTopUpCreditRebase
	var beforeUser User
	require.NoError(t, db.Order("id").Find(&beforeOrders).Error)
	require.NoError(t, db.Order("top_up_id").Find(&beforeBases).Error)
	require.NoError(t, db.First(&beforeUser, 1).Error)
	var beforePlan string
	require.NoError(t, db.Table("wallet_credit_rebases").Select("plan").Scan(&beforePlan).Error)
	summary, body := populateAdminTopupFixture(t, 1)
	assert.EqualValues(t, 105_000_000, summary.Quota)
	assert.Equal(t, float64(15_647_439), body["normalized_quota"])
	assert.Equal(t, true, body["quota_projection_available"])
	assert.Equal(t, "CNY", summary.Currency)
	assert.EqualValues(t, 154_000_000, summary.MoneyMicros)
	assert.Equal(t, float64(154_000_000), body["settled_money_micros"])
	assert.Equal(t, float64(0), body["historical_money_micros"])
	assert.Equal(t, float64(2), body["settled_orders"])
	assert.Equal(t, "settled", body["payment_basis"])
	var afterOrders []TopUp
	var afterBases []WalletTopUpCreditRebase
	var afterUser User
	var afterPlan string
	require.NoError(t, db.Order("id").Find(&afterOrders).Error)
	require.NoError(t, db.Order("top_up_id").Find(&afterBases).Error)
	require.NoError(t, db.First(&afterUser, 1).Error)
	require.NoError(t, db.Table("wallet_credit_rebases").Select("plan").Scan(&afterPlan).Error)
	assert.Equal(t, beforeOrders, afterOrders)
	assert.Equal(t, beforeBases, afterBases)
	assert.Equal(t, beforeUser, afterUser)
	assert.Equal(t, beforePlan, afterPlan)
}

func TestAdminUserTopupPendingCompletedCreditsAreNotRebasedTwice(t *testing.T) {
	db, _ := setupAdminUserTopupProjection(t)
	quoted := adminTopupProjectionOrder("projection-pending-completed", 1_234)
	quoted.Status, quoted.CompleteTime, quoted.SettledAmountMicros = common.TopUpStatusPending, 0, 0
	require.NoError(t, db.Create(&quoted).Error)
	plan := adminTopupProjectionPlan()
	source := adminTopupProjectionSource(quoted)
	plan["pending_bases"] = []any{map[string]any{
		"top_up_id": quoted.Id, "user_id": quoted.UserId,
		"original_credited_quota": int64(1_234), "effective_credited_quota": int64(184), "source": source,
	}}
	putAdminTopupProjectionAudit(t, db, plan)
	require.NoError(t, db.Model(&quoted).Updates(map[string]any{
		"status": common.TopUpStatusSuccess, "complete_time": 2001, "credited_quota": 184,
		"settled_amount_micros":                quoted.ExpectedAmountMicros,
		"pending_credit_rebase_key":            adminTopupTestMigration,
		"pending_credit_rebase_original_quota": 1_234, "pending_credit_rebase_effective_quota": 184,
	}).Error)
	_, body := populateAdminTopupFixture(t, 1)
	assert.Equal(t, true, body["quota_projection_available"])
	assert.Equal(t, float64(184), body["normalized_quota"])
}

func TestAdminUserTopupHistoricalGrossIsNotRefundableNet(t *testing.T) {
	db, _ := setupAdminUserTopupProjection(t)
	order := adminTopupProjectionOrder("projection-partially-refunded", 6_710_363)
	order.RefundedQuota, order.RefundedAmountMicros = 3_355_181, 5_000_000
	require.NoError(t, db.Create(&order).Error)
	plan := adminTopupProjectionPlan()
	addAdminTopupRefundBasis(t, db, plan, order, 500_000)
	putAdminTopupProjectionAudit(t, db, plan)
	_, body := populateAdminTopupFixture(t, 1)
	assert.Equal(t, true, body["quota_projection_available"])
	assert.Equal(t, float64(1_000_000), body["normalized_quota"])
	assert.Equal(t, float64(10_000_000), body["settled_money_micros"])
}

func TestAdminUserTopupRoundsEachHistoricalOrderBeforeSumming(t *testing.T) {
	db, _ := setupAdminUserTopupProjection(t)
	plan := adminTopupProjectionPlan()
	for _, tradeNo := range []string{"round-small-a", "round-small-b"} {
		order := adminTopupProjectionOrder(tradeNo, 4)
		require.NoError(t, db.Create(&order).Error)
		addAdminTopupRefundBasis(t, db, plan, order, 1)
	}
	putAdminTopupProjectionAudit(t, db, plan)
	_, body := populateAdminTopupFixture(t, 1)
	assert.Equal(t, true, body["quota_projection_available"])
	// Each 4 / 6.710363 rounds to 1; rounding their raw sum would give only 1.
	assert.Equal(t, float64(2), body["normalized_quota"])
}

func TestAdminUserTopupUsesFrozenRoundingPolicy(t *testing.T) {
	for _, fixture := range []struct {
		policy string
		want   int64
	}{{"half-away-from-zero", 2}, {"toward-zero", 1}} {
		t.Run(fixture.policy, func(t *testing.T) {
			db, _ := setupAdminUserTopupProjection(t)
			order := adminTopupProjectionOrder("round-policy", 11)
			require.NoError(t, db.Create(&order).Error)
			plan := adminTopupProjectionPlan()
			plan["rounding"] = fixture.policy
			addAdminTopupRefundBasis(t, db, plan, order, fixture.want)
			putAdminTopupProjectionAudit(t, db, plan)
			_, body := populateAdminTopupFixture(t, 1)
			assert.Equal(t, true, body["quota_projection_available"])
			assert.Equal(t, float64(fixture.want), body["normalized_quota"])
		})
	}
}

func TestAdminUserTopupRejectsUnprovenProjectionWithoutChangingRawCredits(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *gorm.DB, map[string]any, TopUp)
	}{
		{"missing child", func(t *testing.T, db *gorm.DB, _ map[string]any, order TopUp) {
			require.NoError(t, db.Delete(&WalletTopUpCreditRebase{}, "top_up_id = ?", order.Id).Error)
		}},
		{"child owner mismatch", func(t *testing.T, db *gorm.DB, _ map[string]any, order TopUp) {
			require.NoError(t, db.Model(&WalletTopUpCreditRebase{}).Where("top_up_id = ?", order.Id).Update("user_id", 2).Error)
		}},
		{"current owner mismatch", func(t *testing.T, db *gorm.DB, _ map[string]any, order TopUp) {
			require.NoError(t, db.Model(&order).Update("user_id", 2).Error)
		}},
		{"child quota mismatch", func(t *testing.T, db *gorm.DB, _ map[string]any, order TopUp) {
			require.NoError(t, db.Model(&WalletTopUpCreditRebase{}).Where("top_up_id = ?", order.Id).Update("original_credited_quota", 6_710_364).Error)
		}},
		{"child refundable mismatch", func(t *testing.T, db *gorm.DB, _ map[string]any, order TopUp) {
			require.NoError(t, db.Model(&WalletTopUpCreditRebase{}).Where("top_up_id = ?", order.Id).Update("refundable_quota", 999_999).Error)
		}},
		{"current credited quota changed", func(t *testing.T, db *gorm.DB, _ map[string]any, order TopUp) {
			require.NoError(t, db.Model(&order).Update("credited_quota", 6_710_364).Error)
		}},
		{"current settled money changed", func(t *testing.T, db *gorm.DB, _ map[string]any, order TopUp) {
			require.NoError(t, db.Model(&order).Update("settled_amount_micros", 11_000_000).Error)
		}},
		{"zero snapshot timestamp", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			plan["snapshot_at"] = 0
		}},
		{"negative snapshot timestamp", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			plan["snapshot_at"] = -1
		}},
		{"writers not frozen", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			plan["snapshot_state"] = "writers_running"
		}},
		{"source money is not a string", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			basis := plan["refund_bases"].([]any)[0].(map[string]any)
			basis["source"].(map[string]any)["money"] = 10
		}},
		{"source credited quota missing", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			basis := plan["refund_bases"].([]any)[0].(map[string]any)
			delete(basis["source"].(map[string]any), "credited_quota")
		}},
		{"factor numerator is not numeric", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			plan["exact_factor"] = map[string]any{"numerator": "not-an-integer", "denominator": 6_710_363}
		}},
		{"bad factor", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			plan["exact_factor"] = map[string]any{"numerator": 1, "denominator": 6}
		}},
		{"bad rounding", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) { plan["rounding"] = "nearest" }},
		{"incomplete audit", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) { delete(plan, "refund_bases") }},
		{"parent identity mismatch", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) { plan["migration_id"] = "other-migration" }},
		{"parent hash mismatch", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			plan["plan_sha256"] = strings.Repeat("b", 64)
		}},
		{"parent hash shape invalid", func(_ *testing.T, _ *gorm.DB, plan map[string]any, _ TopUp) {
			plan["plan_sha256"] = "not-a-sha256"
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			db, _ := setupAdminUserTopupProjection(t)
			order := adminTopupProjectionOrder("projection-invalid", 6_710_363)
			require.NoError(t, db.Create(&order).Error)
			plan := adminTopupProjectionPlan()
			addAdminTopupRefundBasis(t, db, plan, order, 1_000_000)
			test.change(t, db, plan, order)
			putAdminTopupProjectionAudit(t, db, plan)
			if test.name == "parent hash shape invalid" {
				// Matching parent and embedded strings still need a valid SHA shape.
				require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan_sha256=?", plan["plan_sha256"]).Error)
			}
			var before TopUp
			require.NoError(t, db.First(&before, order.Id).Error)
			userID := 1
			if test.name == "current owner mismatch" {
				userID = 2
			}
			summary, body := populateAdminTopupFixture(t, userID)
			assert.EqualValues(t, before.CreditedQuota, summary.Quota)
			assert.Equal(t, false, body["quota_projection_available"])
			var stored TopUp
			require.NoError(t, db.First(&stored, order.Id).Error)
			assert.Equal(t, before, stored)
		})
	}
}

func TestAdminUserTopupPartialMigrationKeepsDTOUsable(t *testing.T) {
	for _, stage := range []string{"no audit", "audit missing hash column", "partial child audit schema", "malformed audit JSON"} {
		t.Run(stage, func(t *testing.T) {
			db, _ := setupAdminUserTopupProjection(t)
			order := adminTopupProjectionOrder("projection-partial", 500_001)
			order.CreateTime, order.CompleteTime = 2000, 2001
			require.NoError(t, db.Create(&order).Error)
			switch stage {
			case "audit missing hash column":
				require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY,plan TEXT NOT NULL)").Error)
				require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases VALUES (?,?)", adminTopupTestMigration, common.GetJsonString(adminTopupProjectionPlan())).Error)
			case "malformed audit JSON":
				putAdminTopupProjectionAudit(t, db, adminTopupProjectionPlan())
				require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan='broken'").Error)
			case "partial child audit schema":
				putAdminTopupProjectionAudit(t, db, adminTopupProjectionPlan())
				require.NoError(t, db.Exec("CREATE TABLE wallet_topup_credit_rebases (top_up_id INTEGER PRIMARY KEY, migration_id TEXT NOT NULL)").Error)
			}
			summary, body := populateAdminTopupFixture(t, 1)
			assert.EqualValues(t, 500_001, summary.Quota)
			assert.EqualValues(t, 10_000_000, summary.MoneyMicros)
			assert.EqualValues(t, 1, summary.Orders)
			assert.Equal(t, false, body["quota_projection_available"])
		})
	}
}

func TestAdminUserTopupMoneyCategoriesDoNotPromoteHistoricalQuotes(t *testing.T) {
	db, _ := setupAdminUserTopupProjection(t)
	settled := adminTopupProjectionOrder("money-settled", 500_000)
	settled.Money, settled.ExpectedAmountMicros, settled.SettledAmountMicros = 999, 5_000_000, 4_000_000
	historical := adminTopupProjectionOrder("money-historical", 500_000)
	historical.Money, historical.ExpectedAmountMicros, historical.SettledAmountMicros = 3, 3_000_000, 0
	require.NoError(t, db.Create(&[]TopUp{settled, historical}).Error)
	summary, body := populateAdminTopupFixture(t, 1)
	assert.EqualValues(t, 7_000_000, summary.MoneyMicros)
	assert.Equal(t, float64(4_000_000), body["settled_money_micros"])
	assert.Equal(t, float64(3_000_000), body["historical_money_micros"])
	assert.Equal(t, float64(1), body["settled_orders"])
	assert.Equal(t, float64(1), body["historical_orders"])
	assert.Equal(t, "mixed", body["payment_basis"])
	require.Len(t, summary.Methods, 1)
	method := body["methods"].([]any)[0].(map[string]any)
	assert.Equal(t, body["settled_money_micros"], method["settled_money_micros"])
	assert.Equal(t, body["historical_money_micros"], method["historical_money_micros"])
	assert.Equal(t, "mixed", method["payment_basis"])
}

func TestAdminUserTopupCurrencyCategoriesStaySeparate(t *testing.T) {
	db, _ := setupAdminUserTopupProjection(t)
	usd := adminTopupProjectionOrder("money-usd", 500_000)
	usd.SettledAmountMicros = 12_340_000
	cny := adminTopupProjectionOrder("money-cny", 500_000)
	cny.SettlementCurrency, cny.Money, cny.SettledAmountMicros = "CNY", 56.78, 0
	require.NoError(t, db.Create(&[]TopUp{usd, cny}).Error)
	summary, body := populateAdminTopupFixture(t, 1)
	assert.Equal(t, "MULTIPLE", summary.Currency)
	assert.Zero(t, summary.MoneyMicros)
	assert.Equal(t, float64(12_340_000), body["settled_money_micros"])
	assert.Equal(t, float64(56_780_000), body["historical_money_micros"])
	assert.Equal(t, "mixed", body["payment_basis"])
	unknown := adminTopupProjectionOrder("money-unknown", 500_000)
	unknown.SettlementCurrency, unknown.Money, unknown.SettledAmountMicros = "", 9.876543, 0
	require.NoError(t, db.Create(&unknown).Error)
	summary, body = populateAdminTopupFixture(t, 1)
	assert.Equal(t, "MULTIPLE", summary.Currency)
	assert.Zero(t, summary.MoneyMicros)
	assert.Equal(t, float64(0), body["historical_money_micros"])
	assert.Equal(t, float64(2), body["historical_orders"])
	for _, method := range body["methods"].([]any) {
		item := method.(map[string]any)
		if item["settlement_currency"] == "UNKNOWN" {
			assert.Equal(t, float64(9_876_543), item["historical_money_micros"])
			assert.Equal(t, "historical", item["payment_basis"])
		}
	}
}

func TestAdminUserTopupNoOrdersHasNoPaymentBasis(t *testing.T) {
	setupAdminUserTopupProjection(t)
	summary, body := populateAdminTopupFixture(t, 1)
	assert.Zero(t, summary.Quota)
	assert.Zero(t, summary.Orders)
	assert.Empty(t, summary.Methods)
	assert.Equal(t, "none", body["payment_basis"])
	assert.Equal(t, float64(0), body["settled_orders"])
	assert.Equal(t, float64(0), body["historical_orders"])
}

func TestAdminUserTopupQuotaSortProjectsBeforePagination(t *testing.T) {
	db, _ := setupAdminUserTopupProjection(t)
	for _, id := range []int{2, 3, 4} {
		user := User{Id: id, Username: "projection-page-" + string(rune('a'+id)), AffCode: "projection-page-" + string(rune('a'+id)), Role: common.RoleCommonUser, Status: common.UserStatusEnabled}
		require.NoError(t, db.Create(&user).Error)
	}
	old := adminTopupProjectionOrder("sort-old", 6_710_363)
	require.NoError(t, db.Create(&old).Error)
	plan := adminTopupProjectionPlan()
	addAdminTopupRefundBasis(t, db, plan, old, 1_000_000)
	putAdminTopupProjectionAudit(t, db, plan)
	for _, fixture := range []struct {
		user  int
		quota int64
		post  bool
	}{{2, 500_001, true}, {3, 1_000_000, true}, {4, 100_000_000, false}} {
		order := adminTopupProjectionOrder("sort-user-"+string(rune('a'+fixture.user)), fixture.quota)
		order.UserId = fixture.user
		if fixture.post {
			order.CreateTime, order.CompleteTime = 2000, 2001
		}
		require.NoError(t, db.Create(&order).Error)
	}
	for _, direction := range []string{"asc", "desc"} {
		all, total, err := GetAllUsers(&common.PageInfo{Page: 1, PageSize: 4}, false, NewUserSortOptions("topup_quota", direction))
		require.NoError(t, err)
		assert.EqualValues(t, 4, total)
		ids := collectUserIDs(all)
		require.Len(t, ids, 4)
		assert.Equal(t, 4, ids[3], "unproven large raw quota must sort last in both directions")
		if direction == "asc" {
			assert.Equal(t, 2, ids[0])
			assert.ElementsMatch(t, []int{1, 3}, ids[1:3])
		} else {
			assert.ElementsMatch(t, []int{1, 3}, ids[:2])
			assert.Equal(t, 2, ids[2])
		}
		var paged, searched []int
		for page := 1; page <= 2; page++ {
			users, count, err := GetAllUsers(&common.PageInfo{Page: page, PageSize: 2}, false, NewUserSortOptions("topup_quota", direction))
			require.NoError(t, err)
			assert.EqualValues(t, 4, count)
			paged = append(paged, collectUserIDs(users)...)
			users, count, err = SearchUsers("", "", nil, nil, false, (page-1)*2, 2, NewUserSortOptions("topup_quota", direction))
			require.NoError(t, err)
			assert.EqualValues(t, 4, count)
			searched = append(searched, collectUserIDs(users)...)
		}
		assert.Equal(t, ids, paged)
		assert.Equal(t, ids, searched)
	}
}
