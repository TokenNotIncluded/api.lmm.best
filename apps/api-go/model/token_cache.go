package model

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
)

// cacheSetToken is retained for tests and explicit complete-cache seeding.
// Production DB reads use cacheInitToken so an old snapshot cannot overwrite
// an already reserved quota in a live hash.
func cacheSetToken(token Token) error {
	key := getTokenCacheKey(token.Key)
	token.Clean()
	err := common.RedisHSetObj(key, &token, time.Duration(common.RedisKeyCacheSeconds())*time.Second)
	if err != nil {
		return err
	}
	return nil
}

func getTokenCacheKey(key string) string {
	return fmt.Sprintf("token:%s", common.GenerateHMAC(key))
}

func getTokenCacheFenceKey(key string) string {
	return fmt.Sprintf("token:fence:%s", common.GenerateHMAC(key))
}

func tokenCacheTTLSeconds() int {
	ttl := common.RedisKeyCacheSeconds()
	if ttl <= 0 {
		return 60
	}
	return ttl
}

// tokenCacheFenceSeconds must outlive a token mutation's database write plus
// any in-flight reader's DB-read-to-cache-init gap. The fence expires
// naturally after commit so a reader holding a pre-mutation snapshot cannot
// publish it immediately after the mutation cleared the cache.
const tokenCacheFenceSeconds = 10

func invalidateTokenCacheForMutation(key string) error {
	if !common.RedisEnabled || common.RDB == nil || key == "" {
		return nil
	}
	ctx := context.Background()
	if err := common.RDB.Set(ctx, getTokenCacheFenceKey(key), 1,
		time.Duration(tokenCacheFenceSeconds)*time.Second).Err(); err != nil {
		return err
	}
	return common.RDB.Del(ctx, getTokenCacheKey(key)).Err()
}

// cacheInitToken hydrates only a cold token hash.  If a hash already exists,
// only its TTL is refreshed, preserving quota changes made by atomic reserve
// scripts.  The fence makes a pre-mutation DB snapshot fail closed.
func cacheInitToken(token Token) (int, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return 0, nil
	}
	allowIps := ""
	if token.AllowIps != nil {
		allowIps = *token.AllowIps
	}
	const script = `
if redis.call('EXISTS', KEYS[2]) == 1 then
  return 0
end
if redis.call('EXISTS', KEYS[1]) == 1 then
  if redis.call('HGET', KEYS[1], 'Id') ~= false
    and redis.call('HEXISTS', KEYS[1], 'RemainQuota') == 1
    and redis.call('HEXISTS', KEYS[1], 'UsedQuota') == 1 then
    redis.call('EXPIRE', KEYS[1], ARGV[17])
    return 2
  end
  redis.call('DEL', KEYS[1])
end
redis.call('HSET', KEYS[1],
  'Id', ARGV[1], 'UserId', ARGV[2], 'Status', ARGV[3], 'Name', ARGV[4],
  'CreatedTime', ARGV[5], 'AccessedTime', ARGV[6], 'ExpiredTime', ARGV[7],
  'UnlimitedQuota', ARGV[8], 'ModelLimitsEnabled', ARGV[9], 'ModelLimits', ARGV[10],
  'AllowIps', ARGV[11], 'Group', ARGV[12], 'CrossGroupRetry', ARGV[13],
  'AutoGroups', ARGV[14], 'RemainQuota', ARGV[15], 'UsedQuota', ARGV[16])
redis.call('EXPIRE', KEYS[1], ARGV[17])
return 1`

	return common.RDB.Eval(context.Background(), script, []string{
		getTokenCacheKey(token.Key), getTokenCacheFenceKey(token.Key),
	},
		token.Id, token.UserId, token.Status, token.Name,
		token.CreatedTime, token.AccessedTime, token.ExpiredTime,
		strconv.FormatBool(token.UnlimitedQuota), strconv.FormatBool(token.ModelLimitsEnabled),
		token.ModelLimits, allowIps, token.Group, strconv.FormatBool(token.CrossGroupRetry),
		token.AutoGroups, token.RemainQuota, token.UsedQuota,
		tokenCacheTTLSeconds(),
	).Int()
}

func cacheDeleteToken(key string) error {
	err := common.RedisDelKey(getTokenCacheKey(key))
	if err != nil {
		return err
	}
	return nil
}

func cacheSetTokenField(key string, field string, value string) error {
	err := common.RedisHSetField(getTokenCacheKey(key), field, value)
	if err != nil {
		return err
	}
	return nil
}

// CacheGetTokenByKey 从缓存中获取 token；不完整的旧 hash 会被拒绝，随后
// GetTokenByKey 会从数据库重新水合完整数据。
func cacheGetTokenByKey(key string) (*Token, error) {
	if !common.RedisEnabled || common.RDB == nil {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var token Token
	if err := common.RedisHGetObj(getTokenCacheKey(key), &token); err != nil {
		return nil, err
	}
	if token.Id <= 0 {
		return nil, fmt.Errorf("token cache is incomplete")
	}
	token.Key = key
	return &token, nil
}
