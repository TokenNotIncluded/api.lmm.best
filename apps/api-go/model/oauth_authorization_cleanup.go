package model

import (
	"time"

	"gorm.io/gorm"
)

const OAuthAuthorizationCleanupBatchSize = 256

// CleanupExpiredOAuthAuthorizations removes one bounded batch of expired
// browser transactions. An empty issuer is the master cleanup's all-issuer
// mode. It never touches token families, billing bindings or replay tombstones.
func CleanupExpiredOAuthAuthorizations(db *gorm.DB, issuer string, now time.Time) (int64, error) {
	if db == nil {
		return 0, gorm.ErrInvalidDB
	}
	candidates := db.Model(&OAuthServerAuthorization{}).Select("digest").
		Where("expires_at_ms <= ?", now.UnixMilli()).Order("expires_at_ms ASC").Limit(OAuthAuthorizationCleanupBatchSize)
	if issuer != "" {
		candidates = candidates.Where("issuer = ?", issuer)
	}
	result := db.Where("digest IN (?) AND expires_at_ms <= ?", candidates, now.UnixMilli()).Delete(&OAuthServerAuthorization{})
	return result.RowsAffected, result.Error
}
