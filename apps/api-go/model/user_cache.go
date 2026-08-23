package model

import (
	"errors"
	"fmt"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"

	"github.com/gin-gonic/gin"
)

// Bump this whenever an authorization-relevant field is added to the cache.
// Older hashes did not carry console activation or trust overrides and would
// incorrectly downgrade an activated account to L0.
const userCacheSchemaVersion = 5

type UserBase struct {
	Id                 int    `json:"id"`
	Group              string `json:"group"`
	Email              string `json:"email"`
	Quota              int    `json:"quota"`
	Status             int    `json:"status"`
	Role               int    `json:"role"`
	Username           string `json:"username"`
	Setting            string `json:"setting"`
	CreatedAt          int64  `json:"created_at"`
	LastAPIActivityAt  int64  `json:"last_api_activity_at"`
	TrustLevelOverride *int   `json:"trust_level_override"`
	ConsoleActivatedAt int64  `json:"console_activated_at"`
	AuthVersion        int64  `json:"-"`
	CacheSchema        int    `json:"-"`
}

func (user *UserBase) WriteContext(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

func userCacheTTLSeconds() int {
	ttl := common.RedisKeyCacheSeconds()
	if ttl <= 0 {
		return 60
	}
	return ttl
}

// invalidateUserCache clears user cache
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisDelKey(getUserCacheKey(userId))
}

// InvalidateUserCache lets higher layers evict authorization state after a
// status, role, or other access-affecting mutation.
func InvalidateUserCache(userId int) error {
	return invalidateUserCache(userId)
}

func populateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}
	return writeUserCache(user.ToBaseUser(), true)
}

// updateUserCache refreshes non-quota user cache fields.
// Quota is maintained by atomic quota delta paths and must not be overwritten
// by stale user snapshots from profile/settings updates.
func updateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}
	return writeUserCache(user.ToBaseUser(), false)
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (*UserBase, error) {
	// Try getting from Redis first
	userCache, err := cacheGetUserBase(userId)
	if err == nil {
		return userCache, nil
	}

	// Redis misses and read failures both fall back to the shared database. A
	// version fence newer than the database is the one exception: allowing that
	// snapshot would re-authorize a user while a restrictive update is pending.
	user, err := GetUserById(userId, false)
	if err != nil {
		return nil, err
	}
	if common.RedisEnabled {
		floor, floorErr := getUserAuthVersionFloor(userId)
		if floorErr == nil && floor > user.AuthVersion {
			return nil, ErrUserAuthCachePending
		}
		if err := populateUserCache(*user); err != nil {
			if errors.Is(err, ErrUserAuthCachePending) {
				return nil, err
			}
			common.SysLog("failed to synchronously populate user cache: " + err.Error())
		}
	}
	return user.ToBaseUser(), nil
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var userCache UserBase
	// Try getting from Redis first
	err := common.RedisHGetObj(getUserCacheKey(userId), &userCache)
	if err != nil {
		return nil, err
	}
	if userCache.Id != userId || userCache.CacheSchema != userCacheSchemaVersion || userCache.AuthVersion <= 0 {
		return nil, fmt.Errorf("user cache schema is stale")
	}
	floor, err := getUserAuthVersionFloor(userId)
	if err != nil {
		return nil, err
	}
	if floor > userCache.AuthVersion {
		return nil, ErrUserAuthCachePending
	}
	return &userCache, nil
}

// Quota deltas are applied through guarded Lua in quota_reserve.go.  A cache
// miss is intentionally a no-op: the database mutation remains authoritative
// and the next cache read will hydrate the complete user hash.
func cacheIncrUserQuota(userId int, delta int64) error {
	if !common.RedisEnabled {
		return nil
	}
	_, err := cacheApplyUserQuotaDelta(userId, delta)
	return err
}

func cacheDecrUserQuota(userId int, delta int64) error {
	return cacheIncrUserQuota(userId, -delta)
}

// syncCreditUserQuotaCache 在授信事务（充值/兑换等）提交后同步把增量补进缓存
// 余额。预扣以缓存值为准（存在期间），授信不能绕过它，否则新到账的额度在
// 缓存过期前不可用；缓存未命中无需处理，下次读取会从已提交的数据库余额水合。
func syncCreditUserQuotaCache(userId int, quota int, operation string) {
	if quota <= 0 {
		return
	}
	if err := cacheIncrUserQuota(userId, int64(quota)); err != nil {
		common.SysLog(fmt.Sprintf("failed to sync %s credit to user quota cache: %s", operation, err.Error()))
	}
}

// Helper functions to get individual fields if needed
func getUserGroupCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Group, nil
}

func getUserQuotaCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Quota, nil
}

// updateUserQuotaCache invalidates the full hash after an absolute quota
// rewrite. Writing only Quota could create a partial cache, while overwriting
// an existing value could race with an atomic reservation.
func updateUserQuotaCache(userId int, _ int) error {
	return invalidateUserCache(userId)
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

// RefreshUserGroupCache writes the database-authoritative group into an
// existing user hash without changing the user's authentication version.
func RefreshUserGroupCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	if userId <= 0 {
		return fmt.Errorf("invalid user id")
	}
	var authoritative User
	if err := DB.Select("id", "auth_version", commonGroupCol).Where("id = ?", userId).First(&authoritative).Error; err != nil {
		return err
	}
	// Group transitions intentionally keep the same authentication version. A
	// refresh that read the previous group can therefore arrive after a newer
	// refresh and still pass the auth-version fence. Re-read after every write
	// and repair the cache when the authoritative group changed in between.
	for range 3 {
		if err := updateUserCacheFieldAtVersion(userId, "Group", authoritative.Group, authoritative.AuthVersion); err != nil {
			return err
		}

		var verified User
		if err := DB.Select("id", "auth_version", commonGroupCol).Where("id = ?", userId).First(&verified).Error; err != nil {
			return err
		}
		if verified.AuthVersion == authoritative.AuthVersion && verified.Group == authoritative.Group {
			return nil
		}
		authoritative = verified
	}

	// Preserve the freshest snapshot observed even when the row was too busy to
	// stabilize within the bounded retries. Returning an error lets best-effort
	// callers emit an operation-specific warning.
	if err := updateUserCacheFieldAtVersion(userId, "Group", authoritative.Group, authoritative.AuthVersion); err != nil {
		return err
	}
	return fmt.Errorf("user group changed repeatedly during cache refresh")
}

func updateUserEmailCache(userId int, email string) error {
	return updateUserCacheField(userId, "Email", email)
}

func updateUserNameCache(userId int, username string) error {
	return updateUserCacheField(userId, "Username", username)
}

func updateUserSettingCache(userId int, setting string) error {
	return updateUserCacheField(userId, "Setting", setting)
}

// updateUserCacheField prevents individual cache refreshes from bypassing the
// auth-version fence. It intentionally does nothing when the complete hash is
// absent; the next GetUserCache call will repopulate it from the database.
func updateUserCacheField(userId int, field string, value interface{}) error {
	if !common.RedisEnabled {
		return nil
	}
	var user User
	if err := DB.Select("id", "auth_version").Where("id = ?", userId).First(&user).Error; err != nil {
		return err
	}
	if user.AuthVersion <= 0 {
		return fmt.Errorf("invalid user auth version")
	}
	return updateUserCacheFieldAtVersion(userId, field, value, user.AuthVersion)
}

// GetUserLanguage returns the user's language preference from cache
// Uses the existing GetUserCache mechanism for efficiency
func GetUserLanguage(userId int) string {
	userCache, err := GetUserCache(userId)
	if err != nil {
		return ""
	}
	return userCache.GetSetting().Language
}
