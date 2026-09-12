package model

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const OAuthBillingKeyPrefix = "oauth_managed_"

// OAuthBillingBinding is a billing identity, not an API credential. The unique
// grant/group pair is serialized by the OAuth family row on the writer DB.
// Do not delete bindings while a grant or its in-flight refunds still exist.
type OAuthBillingBinding struct {
	GrantID string `gorm:"primaryKey;size:43"`
	Group   string `gorm:"primaryKey;size:64"`
	TokenID int    `gorm:"not null;uniqueIndex"`
	UserID  int    `gorm:"not null;index"`
}

func MigrateOAuthBilling(db *gorm.DB) error {
	if err := MigrateOAuthServer(db); err != nil {
		return err
	}
	return db.AutoMigrate(&OAuthBillingBinding{})
}

// EnsureOAuthBillingToken must only be called after live resource validation.
// A second family check under the same write lock as revocation prevents token
// creation after a committed revocation. Nothing here returns a usable key.
func EnsureOAuthBillingToken(ctx context.Context, db *gorm.DB, grantID string, userID int, group, scope string) (*Token, error) {
	if grantID == "" || userID <= 0 || group == "" || group == "auto" || scope == "" {
		return nil, ErrTokenInvalid
	}
	var token Token
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UnixMilli()
		result := tx.Model(&OAuthServerGrant{}).Where("id = ? AND user_id = ? AND revoked_at_ms = 0 AND absolute_expires_at_ms > ?", grantID, userID, now).
			UpdateColumn("lock_version", gorm.Expr("lock_version + 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrTokenInvalid
		}
		var grant OAuthServerGrant
		if err := tx.First(&grant, "id = ?", grantID).Error; err != nil {
			return err
		}
		if !containsOAuthScope(grant.Scope, scope) {
			return ErrTokenInvalid
		}
		var binding OAuthBillingBinding
		err := tx.Where("grant_id = ? AND "+quoteOAuthGroup(db)+" = ?", grantID, group).First(&binding).Error
		if err == nil {
			if binding.UserID != userID {
				return ErrTokenInvalid
			}
			return tx.Where("id = ? AND user_id = ? AND oauth_managed = ? AND "+quoteOAuthGroup(db)+" = ? AND status = ?", binding.TokenID, userID, true, group, common.TokenStatusEnabled).First(&token).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		digest := sha256.Sum256([]byte(grantID + "\x00" + group))
		token = Token{UserId: userID, Key: OAuthBillingKeyPrefix + hex.EncodeToString(digest[:]), OAuthManaged: true, Status: common.TokenStatusEnabled,
			Name: "OAuth / LMM for Pi", Group: group, CreatedTime: now / 1000, AccessedTime: now / 1000, ExpiredTime: grant.AbsoluteExpiresAtMs / 1000, UnlimitedQuota: true, CrossGroupRetry: false}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		return tx.Create(&OAuthBillingBinding{GrantID: grantID, Group: group, TokenID: token.Id, UserID: userID}).Error
	})
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func quoteOAuthGroup(db *gorm.DB) string {
	// This integration supports only SQLite/PostgreSQL, just like oauthserver.
	return `"group"`
}

func containsOAuthScope(scopes, wanted string) bool {
	for _, scope := range strings.Fields(scopes) {
		if scope == wanted {
			return true
		}
	}
	return false
}

// GetRelayBillingToken is an internal billing lookup. Unlike GetTokenByKey it
// permits OAuth-managed rows, but requires the real TokenId from trusted relay
// context as well as its non-public accounting key. Never use for authentication.
func GetRelayBillingToken(tokenID int, key string) (*Token, error) {
	if tokenID <= 0 || key == "" {
		return nil, ErrTokenInvalid
	}
	var token Token
	if err := DB.Where("id = ?", tokenID).Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).First(&token).Error; err != nil {
		return nil, err
	}
	return &token, nil
}
