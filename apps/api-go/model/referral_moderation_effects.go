package model

import (
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

type referralModerationEffects struct {
	publish    func(int) error
	invalidate func(int) error
	revoke     func(int, int64) (int64, error)
}

// Retry only the pre-decision sessions, even if the ban was overturned while
// an earlier response was lost. Existing generic revoke-all APIs are unchanged.
func revokeReferralSessionsThroughVersion(userID int, throughVersion int64) (int64, error) {
	if userID <= 0 || throughVersion <= 0 {
		// Legacy events without an authoritative boundary need reconciliation;
		// never substitute the current user's version or an unbounded revoke.
		return 0, ErrUserSessionInvalid
	}
	now := common.GetTimestamp()
	var total int64
	for {
		var sessions []UserSession
		var affected int64
		err := DB.Transaction(func(tx *gorm.DB) error {
			if err := lockForUpdate(tx).
				Where("user_id = ? AND status = ? AND expires_at > ? AND user_auth_version <= ?",
					userID, UserSessionStatusActive, now, throughVersion).
				Order("sid").Limit(userSessionRevokeBatchSize).Find(&sessions).Error; err != nil {
				return err
			}
			if len(sessions) == 0 {
				return nil
			}
			sids := make([]string, 0, len(sessions))
			for i := range sessions {
				// Lock before fencing: a concurrent refresh cannot turn the
				// selected SID into a newer session between these operations.
				if err := writeUserSessionDenyFence(&sessions[i], UserSessionStatusRevoking, now, "confirmed_referral_abuse"); err != nil {
					return err
				}
				sids = append(sids, sessions[i].SID)
			}
			result := tx.Model(&UserSession{}).
				Where("sid IN ? AND user_id = ? AND status = ? AND user_auth_version <= ?",
					sids, userID, UserSessionStatusActive, throughVersion).
				Updates(map[string]interface{}{
					"status": UserSessionStatusRevoked, "revoked_at": now,
					"revoked_reason": "confirmed_referral_abuse",
				})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != int64(len(sessions)) {
				return ErrUserSessionInactive
			}
			affected = result.RowsAffected
			return nil
		})
		if err != nil {
			return total, err
		}
		if len(sessions) == 0 {
			return total, nil
		}
		total += affected
		for i := range sessions {
			sessions[i].Status = UserSessionStatusRevoked
			sessions[i].RevokedAt = now
			sessions[i].RevokedReason = "confirmed_referral_abuse"
			if err := writeUserSessionCache(sessions[i].cacheEntry(), time.Time{}); err != nil {
				// As in revokeUserSessions, the pre-commit deny fence and
				// durable revoked row still prevent stale-session resurrection.
				common.SysLog("failed to finalize referral session revoke tombstone: " + err.Error())
			}
		}
	}
}
