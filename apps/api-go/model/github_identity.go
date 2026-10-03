package model

import (
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrGitHubMigrationInvalid = errors.New("GitHub account verification is no longer valid")

// IsNumericGitHubID also recognizes zero/leading-zero digit strings so they
// can never be mistaken for legacy usernames.
func IsNumericGitHubID(value string) bool {
	if value == "" {
		return false
	}
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// FindGitHubIdentity refuses ambiguous bindings and ignores deleted username
// candidates. Numeric identities retain the existing deleted-account guard.
func FindGitHubIdentity(subject string, includeDeleted bool) (*User, error) {
	var users []User
	query := DB
	if includeDeleted {
		query = query.Unscoped()
	}
	if err := query.Where("github_id = ?", subject).Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(users) != 1 {
		return nil, ErrGitHubMigrationInvalid
	}
	return &users[0], nil
}

func claimGitHubIdentityWithTx(tx *gorm.DB, userID int, numericID string) error {
	if !IsNumericGitHubID(numericID) {
		return ErrGitHubMigrationInvalid
	}
	var count int64
	if err := tx.Unscoped().Model(&User{}).Where("github_id = ? AND id <> ?", numericID, userID).Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrExternalIdentityAlreadyClaimed
	}
	return ClaimExternalIdentityWithTx(tx, ExternalIdentityProviderGitHub, numericID, userID)
}

// MigrateGitHubIdentityWithTx must run inside the transaction issuing the
// verified login session. The old binding and account security version are
// checked again, so completed/replayed challenges cannot overwrite a relink.
// expectedEmail is only supplied for the verified-email proof path.
func MigrateGitHubIdentityWithTx(tx *gorm.DB, userID int, authVersion int64, legacyID, numericID, expectedEmail string) error {
	if tx == nil || userID <= 0 || authVersion <= 0 || legacyID == "" || IsNumericGitHubID(legacyID) {
		return ErrGitHubMigrationInvalid
	}
	var user User
	if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != authVersion || user.GitHubId != legacyID {
		return ErrGitHubMigrationInvalid
	}
	if expectedEmail != "" && (NormalizeEmail(user.Email) == "" || NormalizeEmail(user.Email) != NormalizeEmail(expectedEmail)) {
		return ErrGitHubMigrationInvalid
	}
	if err := claimGitHubIdentityWithTx(tx, userID, numericID); err != nil {
		return err
	}
	result := tx.Model(&User{}).Where("id = ? AND github_id = ? AND auth_version = ?", userID, legacyID, authVersion).Update("github_id", numericID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrGitHubMigrationInvalid
	}
	return nil
}

// BindGitHubIdentityWithTx claims only the immutable numeric identity; an
// unrelated legacy username is not a binding collision.
func BindGitHubIdentityWithTx(tx *gorm.DB, userID int, numericID string) error {
	return bindGitHubIdentityWithTx(tx, userID, "", numericID)
}

// BindGitHubIdentityForSessionWithTx revalidates the live session under locks
// so a revoked/expired session cannot authorize a delayed settings callback.
func BindGitHubIdentityForSessionWithTx(tx *gorm.DB, userID int, sessionID, numericID string) error {
	if sessionID == "" {
		return ErrGitHubMigrationInvalid
	}
	return bindGitHubIdentityWithTx(tx, userID, sessionID, numericID)
}

func bindGitHubIdentityWithTx(tx *gorm.DB, userID int, sessionID, numericID string) error {
	if tx == nil || userID <= 0 || !IsNumericGitHubID(strings.TrimSpace(numericID)) {
		return ErrGitHubMigrationInvalid
	}
	var user User
	if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
		return err
	}
	if user.Status != common.UserStatusEnabled {
		return ErrGitHubMigrationInvalid
	}
	if sessionID != "" {
		var session UserSession
		if err := lockForUpdate(tx).Where("sid = ? AND user_id = ?", sessionID, userID).First(&session).Error; err != nil {
			return err
		}
		if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= time.Now().Unix() || session.UserAuthVersion != user.AuthVersion {
			return ErrUserSessionInactive
		}
	}
	if err := ReleaseExternalIdentityWithTx(tx, ExternalIdentityProviderGitHub, userID); err != nil {
		return err
	}
	if err := claimGitHubIdentityWithTx(tx, userID, numericID); err != nil {
		return err
	}
	return tx.Model(&User{}).Where("id = ?", userID).Update("github_id", numericID).Error
}
