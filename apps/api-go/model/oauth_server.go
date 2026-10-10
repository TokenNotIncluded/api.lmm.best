package model

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// OAuthServerAuthorization is a short-lived, browser-bound consent transaction.
// All bearer capabilities (including the browser binding) are stored as digests.
// These tables deliberately have no relation to dashboard JWTs or API key tokens.
type OAuthServerAuthorization struct {
	Digest        string `gorm:"primaryKey;size:64"`
	Issuer        string `gorm:"not null;size:512"`
	ClientID      string `gorm:"not null;size:128"`
	RedirectURI   string `gorm:"not null;size:1024"`
	Resource      string `gorm:"not null;size:1024"`
	Scope         string `gorm:"not null;size:2048"`
	State         string `gorm:"not null;size:512"`
	CodeChallenge string `gorm:"not null;size:43"`
	BrowserDigest string `gorm:"not null;size:64"`
	ConsentDigest string `gorm:"not null;size:64"`
	UserID        int64  `gorm:"not null"`
	CreatedAtMs   int64  `gorm:"not null"`
	ExpiresAtMs   int64  `gorm:"not null;index"`
	ConsumedAtMs  int64  `gorm:"not null"`
	LockVersion   int64  `gorm:"not null"`
}

func (OAuthServerAuthorization) TableName() string { return "oauth_server_authorizations" }

// OAuthServerGrant is the serialization/revocation root of one token family.
// Every mutation locks this row with a write before reading any family member.
// Sender binding columns are reserved: phase one only accepts empty bindings.
type OAuthServerGrant struct {
	ID                  string `gorm:"primaryKey;size:43"`
	Issuer              string `gorm:"not null;size:512"`
	ClientID            string `gorm:"not null;size:128;index"`
	UserID              int64  `gorm:"not null;index"`
	RedirectURI         string `gorm:"not null;size:1024"`
	Resource            string `gorm:"not null;size:1024"`
	Scope               string `gorm:"not null;size:2048"`
	BindingMethod       string `gorm:"not null;size:32"`
	BindingThumbprint   string `gorm:"not null;size:128"`
	CreatedAtMs         int64  `gorm:"not null"`
	AbsoluteExpiresAtMs int64  `gorm:"not null;index"`
	RevokedAtMs         int64  `gorm:"not null"`
	RevocationReason    string `gorm:"not null;size:64"`
	LockVersion         int64  `gorm:"not null"`
}

func (OAuthServerGrant) TableName() string { return "oauth_server_grants" }

// OAuthServerCode retains its used tombstone until the family can be purged.
// Retaining it permits a correctly bound replay to revoke issued credentials.
type OAuthServerCode struct {
	Digest        string `gorm:"primaryKey;size:64"`
	Issuer        string `gorm:"not null;size:512"`
	FamilyID      string `gorm:"not null;size:43;index"`
	CodeChallenge string `gorm:"not null;size:43"`
	CreatedAtMs   int64  `gorm:"not null"`
	ExpiresAtMs   int64  `gorm:"not null;index"`
	UsedAtMs      int64  `gorm:"not null"`
}

func (OAuthServerCode) TableName() string { return "oauth_server_codes" }

// OAuthServerToken contains neither the raw access/refresh token nor a
// recoverable encrypted copy. Used refresh digests MUST NOT be deleted early.
type OAuthServerToken struct {
	Digest      string `gorm:"primaryKey;size:64"`
	Issuer      string `gorm:"not null;size:512"`
	FamilyID    string `gorm:"not null;size:43;index"`
	Kind        string `gorm:"not null;size:16"`
	Scope       string `gorm:"not null;size:2048"`
	CreatedAtMs int64  `gorm:"not null"`
	ExpiresAtMs int64  `gorm:"not null;index"`
	UsedAtMs    int64  `gorm:"not null"`
}

func (OAuthServerToken) TableName() string { return "oauth_server_tokens" }

// MigrateOAuthServer is explicitly opt-in. It is NOT registered with the main
// migration path. Pass the authoritative writer DB, never a read replica.
func MigrateOAuthServer(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("oauth server migration: nil database")
	}
	switch db.Dialector.Name() {
	case "sqlite", "postgres":
		return db.AutoMigrate(&OAuthServerAuthorization{}, &OAuthServerGrant{}, &OAuthServerCode{}, &OAuthServerToken{})
	default:
		return fmt.Errorf("oauth server migration: unsupported dialect %q", db.Dialector.Name())
	}
}

// CleanupExpiredOAuthServerArtifacts removes short-lived authorization rows
// and whole token families after they can no longer authorize a request. Family
// members are retained together until absolute expiry (including revoked
// families), preserving refresh-replay detection for every live descendant.
func CleanupExpiredOAuthServerArtifacts(ctx context.Context, db *gorm.DB, now time.Time, batchSize int) (int64, error) {
	if db == nil || batchSize <= 0 {
		return 0, fmt.Errorf("oauth server cleanup: a database and positive batch size are required")
	}
	cutoff := now.UnixMilli()
	var authorizationDigests []string
	if err := db.WithContext(ctx).Model(&OAuthServerAuthorization{}).Where("expires_at_ms <= ?", cutoff).Limit(batchSize).Pluck("digest", &authorizationDigests).Error; err != nil {
		return 0, err
	}
	var familyIDs []string
	if err := db.WithContext(ctx).Model(&OAuthServerGrant{}).Where("absolute_expires_at_ms <= ?", cutoff).Limit(batchSize).Pluck("id", &familyIDs).Error; err != nil {
		return 0, err
	}
	var deleted int64
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(authorizationDigests) != 0 {
			result := tx.Where("digest IN ? AND expires_at_ms <= ?", authorizationDigests, cutoff).Delete(&OAuthServerAuthorization{})
			if result.Error != nil {
				return result.Error
			}
			deleted += result.RowsAffected
		}
		if len(familyIDs) == 0 {
			return nil
		}
		// Recheck the immutable absolute deadline on the parent delete. Tokens and
		// codes cannot be issued once that deadline has passed.
		if err := tx.Where("family_id IN ?", familyIDs).Delete(&OAuthServerToken{}).Error; err != nil {
			return err
		}
		if err := tx.Where("family_id IN ?", familyIDs).Delete(&OAuthServerCode{}).Error; err != nil {
			return err
		}
		result := tx.Where("id IN ? AND absolute_expires_at_ms <= ?", familyIDs, cutoff).Delete(&OAuthServerGrant{})
		if result.Error != nil {
			return result.Error
		}
		deleted += result.RowsAffected
		return nil
	})
	return deleted, err
}
