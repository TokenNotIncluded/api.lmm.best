package model

import (
	cryptorand "crypto/rand"
	"crypto/subtle"
	"errors"
	"math/big"
	"net/mail"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrMerchantStoreEmailUnverified           = errors.New("current store email is not verified")
	ErrMerchantStoreEmailVerificationInvalid  = errors.New("invalid or expired store email verification code")
	ErrMerchantStoreEmailVerificationCooldown = errors.New("store email verification cooldown")
)

// Address-bound facts are never backfilled from legacy account flags.
type MerchantStoreVerifiedEmail struct {
	UserID     int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	EmailHash  string `json:"-" gorm:"size:64;not null"`
	VerifiedAt int64  `json:"-"`
}

// This shared challenge works across nodes and contains neither raw email nor code.
type MerchantStoreEmailVerificationChallenge struct {
	UserID         int    `json:"-" gorm:"primaryKey;autoIncrement:false"`
	EmailHash      string `json:"-" gorm:"size:64;not null"`
	CodeCiphertext string `json:"-" gorm:"type:text;not null"`
	ExpiresAt      int64  `json:"-"`
	Attempts       int    `json:"-"`
	SentAt         int64  `json:"-"`
}

func storeEmailValid(email string) bool {
	if email == "" || len(email) > 254 || strings.TrimSpace(email) != email {
		return false
	}
	address, e := mail.ParseAddress(email)
	return e == nil && address.Address == email && address.Name == ""
}
func storeVerifiedEmailAddress(tx *gorm.DB, userID int) (string, error) {
	u, e := storeUser(tx, userID, common.RoleCommonUser)
	if e != nil || !storeEmailValid(u.Email) {
		return "", ErrMerchantStoreEmailUnverified
	}
	var fact MerchantStoreVerifiedEmail
	if e = tx.First(&fact, "user_id = ?", userID).Error; errors.Is(e, gorm.ErrRecordNotFound) {
		return "", ErrMerchantStoreEmailUnverified
	} else if e != nil {
		return "", e
	}
	if fact.VerifiedAt <= 0 || fact.EmailHash != storeHash(u.Email) {
		return "", ErrMerchantStoreEmailUnverified
	}
	return u.Email, nil
}
func GetMerchantStoreVerifiedEmailAddress(userID int) (string, error) {
	return storeVerifiedEmailAddress(DB, userID)
}
func storeMarkEmailVerified(tx *gorm.DB, userID int, email string) error {
	now := common.GetTimestamp()
	fact := MerchantStoreVerifiedEmail{UserID: userID, EmailHash: storeHash(email), VerifiedAt: now}
	if e := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.AssignmentColumns([]string{"email_hash", "verified_at"})}).Create(&fact).Error; e != nil {
		return e
	}
	// Only this buyer's paid, email-enabled orders may resume. No plaintext address
	// or delivery payload is copied into the durable queue.
	paid := tx.Model(&MerchantStoreOrder{}).Select("id").Where("buyer_id = ? AND status = ? AND email_pickup_link = ?", userID, "paid", true)
	return tx.Model(&MerchantStoreEmailDelivery{}).Where("buyer_id = ? AND state = ? AND order_id IN (?)", userID, "awaiting_verification", paid).Updates(map[string]any{"state": "pending", "next_attempt": now, "last_error_code": ""}).Error
}

// Only a trusted successful-code verifier may call this primitive.
func MarkMerchantStoreEmailVerified(userID int, currentEmail string) error {
	if !storeEmailValid(currentEmail) {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if e := marketLockUsers(tx, userID); e != nil {
			return e
		}
		u, e := storeUser(tx, userID, common.RoleCommonUser)
		if e != nil {
			return e
		}
		if u.Email != currentEmail {
			return ErrMerchantStoreEmailUnverified
		}
		return storeMarkEmailVerified(tx, userID, currentEmail)
	})
}
func BeginMerchantStoreEmailVerification(userID int) (string, string, error) {
	var email, code string
	e := marketTransaction(DB, func(tx *gorm.DB) error {
		if e := marketLockUsers(tx, userID); e != nil {
			return e
		}
		u, e := storeUser(tx, userID, common.RoleCommonUser)
		if e != nil {
			return e
		}
		if !storeEmailValid(u.Email) {
			return ErrMerchantStoreInput
		}
		now := common.GetTimestamp()
		var challenge MerchantStoreEmailVerificationChallenge
		e = lockForUpdate(tx).First(&challenge, "user_id = ?", userID).Error
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e == nil && challenge.SentAt+60 > now {
			return ErrMerchantStoreEmailVerificationCooldown
		}
		n, e := cryptorand.Int(cryptorand.Reader, big.NewInt(1000000))
		if e != nil {
			return e
		}
		digits := strconv.FormatInt(n.Int64(), 10)
		code = strings.Repeat("0", 6-len(digits)) + digits
		email = u.Email
		hash := storeHash(email)
		cipher, e := storeEncrypt("email-verification", fmtStoreActor(userID)+":"+hash, code)
		if e != nil {
			return e
		}
		challenge = MerchantStoreEmailVerificationChallenge{UserID: userID, EmailHash: hash, CodeCiphertext: cipher, ExpiresAt: now + 600, SentAt: now}
		return tx.Save(&challenge).Error
	})
	if e != nil {
		return "", "", e
	}
	return email, code, nil
}
func VerifyMerchantStoreEmailVerification(userID int, code string) error {
	validShape := len(code) == 6
	for _, r := range code {
		if r < '0' || r > '9' {
			validShape = false
		}
	}
	var resultError error
	e := marketTransaction(DB, func(tx *gorm.DB) error {
		if e := marketLockUsers(tx, userID); e != nil {
			return e
		}
		u, e := storeUser(tx, userID, common.RoleCommonUser)
		if e != nil {
			return e
		}
		var challenge MerchantStoreEmailVerificationChallenge
		if e = lockForUpdate(tx).First(&challenge, "user_id = ?", userID).Error; errors.Is(e, gorm.ErrRecordNotFound) {
			resultError = ErrMerchantStoreEmailVerificationInvalid
			return nil
		} else if e != nil {
			return e
		}
		now := common.GetTimestamp()
		if !storeEmailValid(u.Email) || challenge.EmailHash != storeHash(u.Email) || challenge.ExpiresAt <= now || challenge.Attempts >= 5 {
			resultError = ErrMerchantStoreEmailVerificationInvalid
			return nil
		}
		expected, e := storeDecrypt("email-verification", fmtStoreActor(userID)+":"+challenge.EmailHash, challenge.CodeCiphertext)
		if e != nil {
			return e
		}
		if !validShape || subtle.ConstantTimeCompare([]byte(expected), []byte(code)) != 1 {
			resultError = ErrMerchantStoreEmailVerificationInvalid
			return tx.Model(&challenge).Update("attempts", challenge.Attempts+1).Error
		}
		if e = storeMarkEmailVerified(tx, userID, u.Email); e != nil {
			return e
		}
		return tx.Where("user_id = ?", userID).Delete(&MerchantStoreEmailVerificationChallenge{}).Error
	})
	if e != nil {
		return e
	}
	return resultError
}
