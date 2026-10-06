package model

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func usageTestPlan() map[string]any {
	return map[string]any{"version": 1, "kind": "offline_credit_balance_rebase_preview", "migration_id": "usage-test", "user_ids": []int{1}, "has_complete_history": true, "usd_credit_conversion": 500000, "divisor": "6.710363", "rounding": "half-away-from-zero", "snapshot_at": 1000, "user_sources": []map[string]any{{"id": 1, "used_quota": 2491672787}}, "token_sources": []map[string]any{{"id": 11, "user_id": 1, "used_quota": 6710363}}, "other_credit_bases": []map[string]any{}}
}
func usageTestDB(t *testing.T, plan map[string]any) *gorm.DB {
	t.Helper()
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.Exec("INSERT INTO tokens(id,user_id,created_time,used_quota,key) VALUES (11,1,100,6710400,'usage-token')").Error)
	if plan != nil {
		require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY,plan TEXT NOT NULL)").Error)
		bytes, err := json.Marshal(plan)
		require.NoError(t, err)
		require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases VALUES (?,?)", "usage-test", string(bytes)).Error)
	}
	return db
}
func TestUsageProjectionSeparatesHistoricalAndNewCredits(t *testing.T) {
	db := usageTestDB(t, usageTestPlan())
	require.NoError(t, db.Create(&User{Id: 1, Username: "usage-read-only", AffCode: "usage-read-only", Quota: 198041882, UsedQuota: 2491782362}).Error)
	projector, err := LoadUsageProjector(db)
	require.NoError(t, err)
	user, err := projector.User(1, 2491782362)
	require.NoError(t, err)
	require.Equal(t, 371317138, user.HistoricalNormalizedQuota)
	require.Equal(t, 109575, user.PostMigrationQuota)
	require.Equal(t, 371426713, user.NormalizedUsedQuota)
	token, err := projector.Token(11, 1, 6710400)
	require.NoError(t, err)
	require.Equal(t, 1000037, token.NormalizedUsedQuota)
	var stored User
	require.NoError(t, db.First(&stored, 1).Error)
	require.Equal(t, 2491782362, stored.UsedQuota)
	require.Equal(t, 198041882, stored.Quota)
	var raw int
	require.NoError(t, db.Table("tokens").Select("used_quota").Where("id=11").Scan(&raw).Error)
	require.Equal(t, 6710400, raw)
	fresh, err := projector.User(2, 500001)
	require.NoError(t, err)
	require.Equal(t, 500001, fresh.NormalizedUsedQuota)
	require.NoError(t, db.Exec("INSERT INTO tokens(id,user_id,created_time,used_quota,key) VALUES (12,1,1001,123,'new-token'); INSERT INTO tokens(id,user_id,created_time,used_quota,key) VALUES (13,1,100,123,'missing-token')").Error)
	projector, err = LoadUsageProjector(db)
	require.NoError(t, err)
	freshToken, err := projector.Token(12, 1, 123)
	require.NoError(t, err)
	require.Equal(t, 123, freshToken.NormalizedUsedQuota)
	_, err = projector.Token(13, 1, 123)
	require.ErrorIs(t, err, ErrUsageProjectionUnavailable)
	_, err = projector.Token(11, 2, 123)
	require.ErrorIs(t, err, ErrUsageProjectionUnavailable)
}
func TestUsageProjectionNoAuditAndExactRounding(t *testing.T) {
	db := usageTestDB(t, nil)
	projector, err := LoadUsageProjector(db)
	require.NoError(t, err)
	value, err := projector.User(1, 6710363)
	require.NoError(t, err)
	require.Equal(t, 6710363, value.NormalizedUsedQuota)
	for _, rounding := range []string{"half-away-from-zero", "toward-zero"} {
		t.Run(rounding, func(t *testing.T) {
			plan := usageTestPlan()
			plan["divisor"] = "2"
			plan["rounding"] = rounding
			plan["user_sources"] = []map[string]any{{"id": 1, "used_quota": 3}}
			db := usageTestDB(t, plan)
			projector, err := LoadUsageProjector(db)
			require.NoError(t, err)
			got, err := projector.User(1, 4)
			require.NoError(t, err)
			expected := 2
			if rounding == "half-away-from-zero" {
				expected = 3
			}
			require.Equal(t, expected, got.NormalizedUsedQuota)
		})
	}
}
func TestUsageProjectionRejectsUnverifiableAudit(t *testing.T) {
	cases := map[string]func(map[string]any){
		"wrong-anchor":        func(p map[string]any) { p["usd_credit_conversion"] = 3359744 },
		"wrong-version":       func(p map[string]any) { p["version"] = 2 },
		"wrong-kind":          func(p map[string]any) { p["kind"] = "other" },
		"no-complete-history": func(p map[string]any) { p["has_complete_history"] = false },
		"missing-user":        func(p map[string]any) { p["user_sources"] = []any{} },
		"missing-used-field":  func(p map[string]any) { p["user_sources"] = []map[string]any{{"id": 1}} },
		"duplicate-user": func(p map[string]any) {
			p["user_sources"] = []map[string]any{{"id": 1, "used_quota": 1}, {"id": 1, "used_quota": 1}}
		},
		"duplicate-token": func(p map[string]any) {
			p["token_sources"] = []map[string]any{{"id": 11, "user_id": 1, "used_quota": 1}, {"id": 11, "user_id": 1, "used_quota": 1}}
		},
		"wrong-owner": func(p map[string]any) {
			p["token_sources"] = []map[string]any{{"id": 11, "user_id": 2, "used_quota": 1}}
		},
		"bad-divisor":  func(p map[string]any) { p["divisor"] = "6/1" },
		"bad-rounding": func(p map[string]any) { p["rounding"] = "floor" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			plan := usageTestPlan()
			mutate(plan)
			_, err := LoadUsageProjector(usageTestDB(t, plan))
			require.ErrorIs(t, err, ErrUsageProjectionUnavailable)
		})
	}
	t.Run("malformed", func(t *testing.T) {
		db := usageTestDB(t, usageTestPlan())
		require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan='broken'").Error)
		_, err := LoadUsageProjector(db)
		require.ErrorIs(t, err, ErrUsageProjectionUnavailable)
	})
	t.Run("duplicate-audit", func(t *testing.T) {
		db := usageTestDB(t, usageTestPlan())
		require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases SELECT 'second',plan FROM wallet_credit_rebases").Error)
		_, err := LoadUsageProjector(db)
		require.ErrorIs(t, err, ErrUsageProjectionUnavailable)
	})
}
func TestUsageProjectionHistoricalReversalUsesProvenSourceUnits(t *testing.T) {
	for _, moderation := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-fee", true: "moderation"}[moderation], func(t *testing.T) {
			plan := usageTestPlan()
			plan["user_sources"] = []map[string]any{{"id": 1, "used_quota": 13420726}}
			code := "legacy.violation"
			if moderation {
				code = "moderation.flagged"
			}
			source := map[string]any{"id": 7, "user_id": 1, "charged_quota": 6710363, "created_at": 900, "status": "charged", "reversed_at": 0, "error_code": code}
			plan["other_credit_bases"] = []map[string]any{{"kind": "violation_fee_refund", "source_id": "7", "user_id": 1, "original_quota": 6710363, "rebased_quota": 1000000, "source": source}}
			db := usageTestDB(t, plan)
			require.NoError(t, db.AutoMigrate(&ViolationFeeRecord{}))
			record := ViolationFeeRecord{ID: 7, UserID: 1, RequestID: "usage-reversal", ChargedQuota: 6710363, CreatedAt: 900, Status: ViolationFeeRecordStatusReversed, ReversedAt: 1001, ReversedBy: 99, ErrorCode: code}
			require.NoError(t, db.Create(&record).Error)
			current := 6710393
			if moderation {
				current = 13420756
			}
			projector, err := LoadUsageProjector(db)
			require.NoError(t, err)
			got, err := projector.User(1, current)
			require.NoError(t, err)
			expected := 1000030
			if moderation {
				expected = 2000030
			}
			require.Equal(t, expected, got.NormalizedUsedQuota)
			require.Equal(t, 30, got.PostMigrationQuota)
			originalBases := plan["other_credit_bases"]
			plan["other_credit_bases"] = []map[string]any{}
			changedPlan, marshalErr := json.Marshal(plan)
			require.NoError(t, marshalErr)
			require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan=?", string(changedPlan)).Error)
			_, missingErr := LoadUsageProjector(db)
			if !moderation {
				require.ErrorIs(t, missingErr, ErrUsageProjectionUnavailable)
			} else {
				require.NoError(t, missingErr)
			}
			plan["other_credit_bases"] = originalBases
			restoredPlan, marshalErr := json.Marshal(plan)
			require.NoError(t, marshalErr)
			require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan=?", string(restoredPlan)).Error)
			require.NoError(t, db.Model(&record).Update("charged_quota", 1).Error)
			_, err = LoadUsageProjector(db)
			require.ErrorIs(t, err, ErrUsageProjectionUnavailable)
		})
	}
}
