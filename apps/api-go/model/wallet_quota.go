package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrWalletQuotaOutOfRange means a wallet mutation would leave the balance
// outside the symmetric JavaScript-safe integer domain.
var ErrWalletQuotaOutOfRange = errors.New("wallet quota would exceed the safe range")

func walletQuotaCurrentBounds(delta int) (int, int, error) {
	if err := common.ValidateWalletQuota(delta); err != nil {
		return 0, 0, err
	}
	minCurrent := common.MinWalletQuota
	maxCurrent := common.MaxWalletQuota
	if delta > 0 {
		maxCurrent -= delta
	} else if delta < 0 {
		minCurrent -= delta
	}
	return minCurrent, maxCurrent, nil
}

// GuardWalletQuotaDelta adds the symmetric final-balance predicates to an
// already scoped User query without executing it. This is used by callers
// that must update wallet and state-machine columns atomically.
func GuardWalletQuotaDelta(query *gorm.DB, delta int) (*gorm.DB, error) {
	minCurrent, maxCurrent, err := walletQuotaCurrentBounds(delta)
	if err != nil {
		return query, err
	}
	return query.Where("quota >= ? AND quota <= ?", minCurrent, maxCurrent), nil
}

// UpdateWalletQuotaByDelta applies a guarded quota delta to an already scoped
// User update query. Callers may add state-machine, ownership, or sufficient-
// balance predicates before calling this helper; all of them remain part of
// the same conditional UPDATE. RowsAffected is zero when any predicate fails.
func UpdateWalletQuotaByDelta(query *gorm.DB, delta int) *gorm.DB {
	guarded, err := GuardWalletQuotaDelta(query, delta)
	if err != nil {
		query.AddError(err)
		return query
	}
	return guarded.UpdateColumn("quota", gorm.Expr("quota + ?", delta))
}

// ApplyWalletQuotaDelta updates one user's wallet and turns a failed boundary
// predicate into a stable error. It does not touch Redis; callers must update
// or invalidate cache only after the surrounding database transaction commits.
func ApplyWalletQuotaDelta(tx *gorm.DB, userID int, delta int) error {
	if delta == 0 {
		return nil
	}
	result := UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ?", userID), delta)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWalletQuotaOutOfRange
	}
	return nil
}

// boundedQuotaCounterExpr saturates cumulative non-wallet counters at the
// same JSON-safe bounds. Column names are selected from an internal allowlist
// so they can be embedded portably in a CASE expression.
func boundedQuotaCounterExpr(column string, delta int) clause.Expr {
	switch column {
	case "used_quota", "request_count", "aff_quota", "aff_history":
	default:
		panic("unsupported bounded quota counter column")
	}
	if delta >= 0 {
		return gorm.Expr(
			"CASE WHEN "+column+" < ? THEN ? WHEN "+column+" > ? THEN ? ELSE "+column+" + ? END",
			common.MinWalletQuota, common.MinWalletQuota, common.MaxWalletQuota-delta, common.MaxWalletQuota, delta,
		)
	}
	return gorm.Expr(
		"CASE WHEN "+column+" < ? THEN ? WHEN "+column+" > ? THEN ? ELSE "+column+" + ? END",
		common.MinWalletQuota-delta, common.MinWalletQuota, common.MaxWalletQuota, common.MaxWalletQuota, delta,
	)
}
