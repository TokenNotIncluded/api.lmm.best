package model

import (
	"context"
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// cacheQuotaResult is deliberately separate from bool: a cache miss must not
// be treated as an insufficient balance.  A miss can be caused by Redis being
// unavailable, an expired hash, or a cache created by an older version.
type cacheQuotaResult int

const (
	cacheQuotaInsufficient cacheQuotaResult = iota
	cacheQuotaOK
	cacheQuotaMiss
)

// The user cache is a complete, versioned hash.  These guards prevent a raw
// increment from creating a partial hash and prevent a stale hash belonging to
// another user/schema from authorizing a spend.
const userQuotaReserveScript = `
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= tonumber(ARGV[2])
  or tonumber(redis.call('HGET', KEYS[1], 'CacheSchema') or '0') ~= tonumber(ARGV[3])
  or redis.call('HEXISTS', KEYS[1], 'Quota') == 0 then
  return -1
end
local quota = tonumber(redis.call('HGET', KEYS[1], 'Quota'))
local amount = tonumber(ARGV[1])
local minQuota = tonumber(ARGV[4])
local maxQuota = tonumber(ARGV[5])
if quota == nil or quota < minQuota or quota > maxQuota then
  return -1
end
if quota < amount or quota - amount < minQuota then
  return 0
end
redis.call('HINCRBY', KEYS[1], 'Quota', -amount)
return 1`

const userQuotaDeltaScript = `
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= tonumber(ARGV[2])
  or tonumber(redis.call('HGET', KEYS[1], 'CacheSchema') or '0') ~= tonumber(ARGV[3])
  or redis.call('HEXISTS', KEYS[1], 'Quota') == 0 then
  return -1
end
local quota = tonumber(redis.call('HGET', KEYS[1], 'Quota'))
local delta = tonumber(ARGV[1])
local minQuota = tonumber(ARGV[4])
local maxQuota = tonumber(ARGV[5])
if quota == nil or quota < minQuota or quota > maxQuota
  or quota + delta < minQuota or quota + delta > maxQuota then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'Quota', delta)
return 1`

// Token hashes do not carry the user-cache schema, but they must contain the
// identity and both accounting fields before an atomic operation is allowed.
const tokenQuotaReserveScript = `
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= tonumber(ARGV[2])
  or redis.call('HEXISTS', KEYS[1], 'RemainQuota') == 0
  or redis.call('HEXISTS', KEYS[1], 'UsedQuota') == 0 then
  return -1
end
local remain = tonumber(redis.call('HGET', KEYS[1], 'RemainQuota'))
if remain == nil or remain < tonumber(ARGV[1]) then
  return 0
end
redis.call('HINCRBY', KEYS[1], 'RemainQuota', -tonumber(ARGV[1]))
redis.call('HINCRBY', KEYS[1], 'UsedQuota', tonumber(ARGV[1]))
redis.call('HSET', KEYS[1], 'AccessedTime', ARGV[3])
return 1`

const tokenQuotaDeltaScript = `
if tonumber(redis.call('HGET', KEYS[1], 'Id') or '0') ~= tonumber(ARGV[2])
  or redis.call('HEXISTS', KEYS[1], 'RemainQuota') == 0
  or redis.call('HEXISTS', KEYS[1], 'UsedQuota') == 0 then
  return -1
end
redis.call('HINCRBY', KEYS[1], 'RemainQuota', tonumber(ARGV[1]))
redis.call('HINCRBY', KEYS[1], 'UsedQuota', -tonumber(ARGV[1]))
redis.call('HSET', KEYS[1], 'AccessedTime', ARGV[3])
return 1`

func quotaResultFromLua(result int, err error) (cacheQuotaResult, error) {
	if err != nil {
		return cacheQuotaMiss, err
	}
	switch result {
	case 1:
		return cacheQuotaOK, nil
	case 0:
		return cacheQuotaInsufficient, nil
	default:
		return cacheQuotaMiss, nil
	}
}

func cacheTryReserveUserQuota(userID int, amount int64) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), userQuotaReserveScript,
		[]string{getUserCacheKey(userID)}, amount, userID, userCacheSchemaVersion,
		common.MinWalletQuota, common.MaxWalletQuota).Int()
	return quotaResultFromLua(result, err)
}

func cacheApplyUserQuotaDelta(userID int, delta int64) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), userQuotaDeltaScript,
		[]string{getUserCacheKey(userID)}, delta, userID, userCacheSchemaVersion,
		common.MinWalletQuota, common.MaxWalletQuota).Int()
	return quotaResultFromLua(result, err)
}

func cacheTryReserveTokenQuota(id int, key string, amount int64) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), tokenQuotaReserveScript,
		[]string{getTokenCacheKey(key)}, amount, id, common.GetTimestamp()).Int()
	return quotaResultFromLua(result, err)
}

func cacheApplyTokenQuotaDelta(id int, key string, delta int64) (cacheQuotaResult, error) {
	result, err := common.RDB.Eval(context.Background(), tokenQuotaDeltaScript,
		[]string{getTokenCacheKey(key)}, delta, id, common.GetTimestamp()).Int()
	return quotaResultFromLua(result, err)
}

func persistUserQuotaDelta(id int, delta int) error {
	// Redis is only a fast reservation hint. The durable update repeats both
	// the sufficient-balance predicate and the wallet bounds, so a stale cache
	// fill can never authorize an overdraft.
	query := DB.Model(&User{}).Where("id = ?", id)
	if delta < 0 {
		if err := common.ValidateWalletQuota(delta); err != nil {
			return err
		}
		query = query.Where("quota >= ?", -delta)
	}
	result := UpdateWalletQuotaByDelta(query, delta)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 1 {
		return nil
	}
	if _, err := currentWalletQuota(DB, id); err != nil {
		return err
	}
	return ErrWalletQuotaOutOfRange
}

func persistTokenQuotaDelta(id int, delta int) error {
	result := DB.Model(&Token{}).Where("id = ?", id).Updates(map[string]interface{}{
		"remain_quota":  gorm.Expr("remain_quota + ?", delta),
		"used_quota":    gorm.Expr("used_quota - ?", delta),
		"accessed_time": common.GetTimestamp(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func reserveUserQuotaDB(id int, quota int) (bool, error) {
	return reserveUserQuotaDBWithMinimum(id, quota, 0)
}

func reserveUserQuotaDBWithMinimum(id, amount, minimum int) (bool, error) {
	result := UpdateWalletQuotaByDelta(
		DB.Model(&User{}).Where("id = ? AND quota >= ? AND quota >= ?", id, amount, minimum),
		-amount,
	)
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	current, err := currentWalletQuota(DB, id)
	if err != nil {
		return false, err
	}
	if err := common.ValidateWalletQuota(current); err != nil {
		return false, ErrWalletQuotaOutOfRange
	}
	return false, nil
}

func reserveTokenQuotaDB(id int, quota int) (bool, error) {
	result := DB.Model(&Token{}).
		Where("id = ? AND remain_quota >= ?", id, quota).
		Updates(map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": common.GetTimestamp(),
		})
	return result.RowsAffected == 1, result.Error
}

// TryReserveUserQuota atomically checks and deducts a wallet balance. The
// database conditional UPDATE is the sole authorization decision; Redis is
// invalidated only after that durable update succeeds. This fail-closed order
// prevents a delayed cache fill from authorizing an overdraft.
func TryReserveUserQuota(id int, quota int) (bool, error) {
	return TryReserveUserQuotaWithMinimum(id, quota, 0)
}

// TryReserveUserQuotaWithMinimum also requires the starting wallet balance to
// meet minimum. Only amount is deducted; the remaining balance may fall below
// minimum. Both predicates are checked in the same durable database UPDATE.
func TryReserveUserQuotaWithMinimum(id, amount, minimum int) (bool, error) {
	if amount < 0 {
		return false, errors.New("quota 不能为负数！")
	}
	if minimum < 0 {
		return false, errors.New("minimum quota must not be negative")
	}
	if err := common.ValidateWalletQuota(amount); err != nil {
		return false, err
	}
	if err := common.ValidateWalletQuota(minimum); err != nil {
		return false, err
	}
	if amount == 0 {
		current, err := currentWalletQuota(DB, id)
		if err != nil || minimum == 0 {
			// Preserve the zero-amount API's existing user-existence check,
			// including users with an already negative wallet balance.
			return err == nil, err
		}
		if err := common.ValidateWalletQuota(current); err != nil {
			return false, ErrWalletQuotaOutOfRange
		}
		return current >= minimum, nil
	}
	reserved, err := reserveUserQuotaDBWithMinimum(id, amount, minimum)
	if err != nil {
		return false, err
	}
	if common.RedisEnabled && common.RDB != nil {
		if cacheErr := invalidateUserCache(id); cacheErr != nil {
			common.SysLog("failed to invalidate user quota cache after reserve decision: " + cacheErr.Error())
		}
	}
	return reserved, nil
}

// TryReserveTokenQuota atomically checks and deducts a token balance. Unlimited
// tokens retain the existing accounting behavior and bypass only the balance
// predicate.
func TryReserveTokenQuota(id int, key string, quota int, unlimited bool) (bool, error) {
	if quota < 0 {
		return false, errors.New("quota 不能为负数！")
	}
	if quota == 0 {
		return true, nil
	}
	// Redis may be evicted or hydrated at any time. Only the durable balance
	// can authorize a spend shared by wallet and subscription transactions.
	var reserved bool
	var err error
	if unlimited {
		err = persistTokenQuotaDelta(id, -quota)
		reserved = err == nil
	} else {
		reserved, err = reserveTokenQuotaDB(id, quota)
	}
	if err != nil {
		return false, err
	}
	if cacheErr := invalidateTokenCacheForMutation(key); cacheErr != nil {
		common.SysLog("invalidate token after reserve: " + cacheErr.Error())
	}
	return reserved, nil
}
