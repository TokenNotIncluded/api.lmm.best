package model

import (
	"errors"
	"math"

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

func currentWalletQuota(tx *gorm.DB, userID int) (int, error) {
	var quota int
	err := tx.Model(&User{}).Select("quota").Where("id = ?", userID).Take(&quota).Error
	if err != nil {
		return 0, err
	}
	return quota, nil
}

// ApplyWalletQuotaDelta updates one user's wallet and turns a failed boundary
// predicate into a stable error. It does not touch Redis; callers must update
// or invalidate cache only after the surrounding database transaction commits.
func ApplyWalletQuotaDelta(tx *gorm.DB, userID int, delta int) error {
	if tx == nil || userID <= 0 {
		return gorm.ErrInvalidData
	}
	if delta == 0 {
		quota, err := currentWalletQuota(tx, userID)
		if err != nil {
			return err
		}
		if err := common.ValidateWalletQuota(quota); err != nil {
			return ErrWalletQuotaOutOfRange
		}
		return nil
	}
	result := UpdateWalletQuotaByDelta(tx.Model(&User{}).Where("id = ?", userID), delta)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	if _, err := currentWalletQuota(tx, userID); err != nil {
		return err
	}
	return ErrWalletQuotaOutOfRange
}

// boundedQuotaCounterExpr saturates cumulative non-wallet counters at the
// same JSON-safe bounds. Column names are selected from an internal allowlist
// so they can be embedded portably in a CASE expression.
func boundedQuotaCounterExpr(column string, delta int) clause.Expr {
	switch column {
	case "used_quota", "aff_quota", "aff_history":
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

// boundedInt32CounterExpr protects request_count's legacy INT column on
// MySQL/PostgreSQL instead of applying the wider wallet bounds.
func boundedInt32CounterExpr(delta int) clause.Expr {
	if delta > math.MaxInt32 {
		return gorm.Expr("?", math.MaxInt32)
	}
	if delta < math.MinInt32 {
		return gorm.Expr("?", math.MinInt32)
	}
	if delta >= 0 {
		return gorm.Expr(
			"CASE WHEN request_count < ? THEN ? WHEN request_count > ? THEN ? ELSE request_count + ? END",
			math.MinInt32, math.MinInt32, math.MaxInt32-delta, math.MaxInt32, delta,
		)
	}
	return gorm.Expr(
		"CASE WHEN request_count < ? THEN ? WHEN request_count > ? THEN ? ELSE request_count + ? END",
		math.MinInt32-delta, math.MinInt32, math.MaxInt32, math.MaxInt32, delta,
	)
}
