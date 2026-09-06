package model

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const UserSessionAutoLogoutAge = 7 * 24 * time.Hour

// UpdateUserSessionAutoLogout changes only the session preference, preserving
// other settings, including unknown fields, under the user's row lock.
func UpdateUserSessionAutoLogout(userID int, enabled bool) error {
	if userID <= 0 {
		return ErrUserSessionInvalid
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "setting").First(&user, userID).Error; err != nil {
			return err
		}
		setting := make(map[string]json.RawMessage)
		if user.Setting != "" {
			if err := json.Unmarshal([]byte(user.Setting), &setting); err != nil {
				return err
			}
		}
		if setting == nil {
			setting = make(map[string]json.RawMessage)
		}
		value, err := json.Marshal(enabled)
		if err != nil {
			return err
		}
		setting["session_auto_logout"] = value
		encoded, err := json.Marshal(setting)
		if err != nil {
			return err
		}
		return tx.Model(&user).Update("setting", string(encoded)).Error
	}); err != nil {
		return err
	}
	return invalidateUserCache(userID)
}

// RevokeWeekOldUserSession rechecks the preference under the same user lock
// used by its setter. A sweep or cache snapshot observed before opt-out must
// not revoke a session after that opt-out has committed.
func RevokeWeekOldUserSession(userID int, sid string, now int64) (bool, error) {
	if userID <= 0 || sid == "" {
		return false, ErrUserSessionInvalid
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	var session UserSession
	var revoked bool
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).Select("id", "setting").First(&user, userID).Error; err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("sid = ? AND user_id = ?", sid, userID).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserSessionInactive
			}
			return err
		}
		if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now {
			return ErrUserSessionInactive
		}
		if !user.GetSetting().IsSessionAutoLogoutEnabled() || session.CreatedAt >= now-int64(UserSessionAutoLogoutAge/time.Second) {
			return nil
		}
		if err := writeUserSessionDenyFence(&session, UserSessionStatusRevoking, now, "weekly_auto_logout"); err != nil {
			return err
		}
		result := tx.Model(&UserSession{}).
			Where("sid = ? AND user_id = ? AND status = ? AND revoked_at = 0", sid, userID, UserSessionStatusActive).
			Updates(map[string]interface{}{
				"status":         UserSessionStatusRevoked,
				"revoked_at":     now,
				"revoked_reason": "weekly_auto_logout",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrUserSessionInactive
		}
		revoked = true
		session.Status = UserSessionStatusRevoked
		session.RevokedAt = now
		session.RevokedReason = "weekly_auto_logout"
		return nil
	})
	if err != nil {
		return false, err
	}
	if revoked {
		if err := writeUserSessionCache(session.cacheEntry(), time.Time{}); err != nil {
			common.SysLog("failed to finalize weekly user session revoke tombstone: " + err.Error())
		}
	}
	return revoked, nil
}

// RevokeWeekOldUserSessions uses login time, never refresh/activity time.
// A zero userID is the periodic global sweep. Keyset pagination advances past
// opted-out users, so they cannot starve later candidates. Revocation uses
// the existing Redis fences and audit trail.
func RevokeWeekOldUserSessions(userID int, now int64) error {
	if userID < 0 {
		return ErrUserSessionInvalid
	}
	if now <= 0 {
		now = time.Now().Unix()
	}
	cutoff := now - int64(UserSessionAutoLogoutAge/time.Second)
	after := ""
	for {
		query := DB.Model(&UserSession{}).
			Select("user_sessions.sid, user_sessions.user_id, users.setting").
			Joins("JOIN users ON users.id = user_sessions.user_id").
			Where("users.deleted_at IS NULL AND user_sessions.status = ? AND user_sessions.revoked_at = 0 AND user_sessions.expires_at > ? AND user_sessions.created_at < ? AND user_sessions.sid > ?", UserSessionStatusActive, now, cutoff, after)
		if userID > 0 {
			query = query.Where("user_sessions.user_id = ?", userID)
		}
		var candidates []struct {
			SID     string `gorm:"column:sid"`
			UserID  int
			Setting string
		}
		if err := query.Order("user_sessions.sid").Limit(userSessionRevokeBatchSize).Scan(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}
		for _, candidate := range candidates {
			user := User{Setting: candidate.Setting}
			if user.GetSetting().IsSessionAutoLogoutEnabled() {
				if _, err := RevokeWeekOldUserSession(candidate.UserID, candidate.SID, now); err != nil && !errors.Is(err, ErrUserSessionInactive) {
					return err
				}
			}
		}
		after = candidates[len(candidates)-1].SID
	}
}
