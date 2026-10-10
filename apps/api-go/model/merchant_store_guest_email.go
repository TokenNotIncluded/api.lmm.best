package model

import (
	cryptorand "crypto/rand"
	"crypto/subtle"
	"errors"
	"math/big"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// One address fact belongs to one durable guest. Neither an account's email
// verification nor an order-search capability can establish this fact.
// Keeping past verified addresses preserves already-paid delivery snapshots.
type MerchantStoreGuestEmailVerification struct {
	GuestID        string  `json:"-" gorm:"primaryKey;size:36"`
	EmailHash      string  `json:"-" gorm:"primaryKey;size:64"`
	ChallengeID    string  `json:"-" gorm:"size:36"`
	CodeCiphertext string  `json:"-" gorm:"type:text"`
	ExpiresAt      int64   `json:"-"`
	Attempts       int     `json:"-"`
	SentAt         int64   `json:"-"`
	SentTimes      []int64 `json:"-" gorm:"serializer:json;type:text"`
	VerifiedAt     int64   `json:"-"`
}

func (MerchantStoreGuestEmailVerification) TableName() string {
	return "merchant_store_guest_email_verifications"
}

// Phase-5 qualification uses the exact exported model set, not a guessed
// combined table count. Registering the model never activates the capability.
func MerchantStoreGuestEmailModels() []interface{} {
	return []interface{}{&MerchantStoreGuestEmailVerification{}}
}

type MerchantStoreGuestEmailChallenge struct {
	ChallengeID string `json:"challenge_id"`
	ExpiresAt   int64  `json:"expires_at"`
	Email       string `json:"-"`
	Code        string `json:"-"`
}

func storeLockGuestEmailSubject(tx *gorm.DB, token string) (*MerchantStoreGuest, error) {
	guest, err := ResolveMerchantStoreGuest(tx, token)
	if err != nil {
		return nil, err
	}
	if err = lockForUpdate(tx).Where("id = ? AND token_hash = ? AND expires_at > ?", guest.ID, storeHash(token), common.GetTimestamp()).First(guest).Error; err != nil {
		return nil, ErrMerchantStoreDenied
	}
	return guest, nil
}

// Read every address under the guest row lock. Both the failure budget and
// rolling send budget survive resends and switches to another address.
func storeGuestEmailWindow(tx *gorm.DB, guestID string, now int64) ([]MerchantStoreGuestEmailVerification, int, int, int64, int64, error) {
	var rows []MerchantStoreGuestEmailVerification
	if err := tx.Where("guest_id = ?", guestID).Find(&rows).Error; err != nil {
		return nil, 0, 0, 0, 0, err
	}
	attempts, sends := 0, 0
	deadline, lastSend := now+600, int64(0)
	for _, row := range rows {
		if row.ExpiresAt > now {
			attempts += row.Attempts
			if row.ExpiresAt < deadline {
				deadline = row.ExpiresAt
			}
		}
		if row.SentAt > lastSend {
			lastSend = row.SentAt
		}
		for _, sent := range row.SentTimes {
			if sent > now-3600 {
				sends++
			}
		}
	}
	return rows, attempts, sends, deadline, lastSend, nil
}

func BeginMerchantStoreGuestEmailVerification(token, expectedEmail string) (*MerchantStoreGuestEmailChallenge, error) {
	email, err := NormalizeMerchantStorePickupEmail(expectedEmail)
	if err != nil || email == "" {
		return nil, ErrMerchantStoreInput
	}
	result := &MerchantStoreGuestEmailChallenge{Email: email}
	err = marketTransaction(DB, func(tx *gorm.DB) error {
		guest, err := storeLockGuestEmailSubject(tx, token)
		if err != nil {
			return err
		}
		if err = MerchantStoreAccessRequiresWriter(tx); err != nil {
			return err
		}
		now := common.GetTimestamp()
		rows, attempts, sends, deadline, lastSend, err := storeGuestEmailWindow(tx, guest.ID, now)
		if err != nil {
			return err
		}
		if lastSend+60 > now || attempts >= 5 || sends >= 10 {
			return ErrMerchantStoreEmailVerificationCooldown
		}
		hash := storeHash(email)
		row := MerchantStoreGuestEmailVerification{GuestID: guest.ID, EmailHash: hash}
		for _, old := range rows {
			if old.EmailHash == hash {
				row = old
			}
		}
		if row.ExpiresAt <= now {
			row.Attempts = 0
		}
		row.ExpiresAt = deadline
		row.ChallengeID = uuid.NewString()
		n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1000000))
		if err != nil {
			return err
		}
		result.Code = strconv.FormatInt(n.Int64(), 10)
		result.Code = strings.Repeat("0", 6-len(result.Code)) + result.Code
		row.CodeCiphertext, err = storeEncrypt("guest-email-verification", guest.ID+":"+hash+":"+row.ChallengeID, result.Code)
		if err != nil {
			return err
		}
		recent := make([]int64, 0, 10)
		for _, sent := range row.SentTimes {
			if sent > now-3600 {
				recent = append(recent, sent)
			}
		}
		row.SentTimes, row.SentAt = append(recent, now), now
		// A changed address or resend consumes the previous challenge. Verified
		// facts and failed-attempt windows are deliberately retained.
		if err = tx.Model(&MerchantStoreGuestEmailVerification{}).Where("guest_id = ?", guest.ID).Updates(map[string]any{"challenge_id": "", "code_ciphertext": ""}).Error; err != nil {
			return err
		}
		if err = tx.Save(&row).Error; err != nil {
			return err
		}
		result.ChallengeID, result.ExpiresAt = row.ChallengeID, row.ExpiresAt
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func ConfirmMerchantStoreGuestEmailVerification(token, expectedEmail, challengeID, code string) error {
	email, err := NormalizeMerchantStorePickupEmail(expectedEmail)
	if err != nil || email == "" {
		return ErrMerchantStoreInput
	}
	validShape := len(code) == 6 && strings.Trim(code, "0123456789") == ""
	var resultError error
	err = marketTransaction(DB, func(tx *gorm.DB) error {
		guest, err := storeLockGuestEmailSubject(tx, token)
		if err != nil {
			return err
		}
		if err = MerchantStoreAccessRequiresWriter(tx); err != nil {
			return err
		}
		_, attempts, _, _, _, err := storeGuestEmailWindow(tx, guest.ID, common.GetTimestamp())
		if err != nil {
			return err
		}
		var row MerchantStoreGuestEmailVerification
		err = tx.First(&row, "guest_id = ? AND email_hash = ?", guest.ID, storeHash(email)).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMerchantStoreEmailVerificationInvalid
		}
		if err != nil {
			return err
		}
		if row.ExpiresAt <= common.GetTimestamp() || attempts >= 5 || row.ChallengeID == "" || row.CodeCiphertext == "" {
			return ErrMerchantStoreEmailVerificationInvalid
		}
		expected, err := storeDecrypt("guest-email-verification", guest.ID+":"+row.EmailHash+":"+row.ChallengeID, row.CodeCiphertext)
		if err != nil {
			return err
		}
		if !validShape || row.ChallengeID != challengeID || subtle.ConstantTimeCompare([]byte(expected), []byte(code)) != 1 {
			resultError = ErrMerchantStoreEmailVerificationInvalid
			return tx.Model(&row).Update("attempts", row.Attempts+1).Error
		}
		return tx.Model(&row).Updates(map[string]any{"verified_at": common.GetTimestamp(), "challenge_id": "", "code_ciphertext": ""}).Error
	})
	if err != nil {
		return err
	}
	return resultError
}

func GetMerchantStoreGuestEmailStatus(token, expectedEmail string) (bool, error) {
	email, err := NormalizeMerchantStorePickupEmail(expectedEmail)
	if err != nil {
		return false, err
	}
	guest, err := ResolveMerchantStoreGuest(DB, token)
	if err != nil {
		return false, err
	}
	if email == "" {
		return false, nil
	}
	var row MerchantStoreGuestEmailVerification
	err = DB.First(&row, "guest_id = ? AND email_hash = ?", guest.ID, storeHash(email)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return row.VerifiedAt > 0, err
}

// This sender-only check cannot establish a verified fact or expose a pickup
// link. It detects a superseded challenge before sending its ownership code.
func ValidateMerchantStoreGuestEmailChallenge(token, expectedEmail, challengeID, code string) error {
	email, err := NormalizeMerchantStorePickupEmail(expectedEmail)
	if err != nil || email == "" {
		return ErrMerchantStoreInput
	}
	guest, err := ResolveMerchantStoreGuest(DB, token)
	if err != nil {
		return err
	}
	var row MerchantStoreGuestEmailVerification
	if err = DB.First(&row, "guest_id = ? AND email_hash = ? AND challenge_id = ? AND expires_at > ?", guest.ID, storeHash(email), challengeID, common.GetTimestamp()).Error; err != nil {
		return ErrMerchantStoreEmailVerificationInvalid
	}
	expected, err := storeDecrypt("guest-email-verification", guest.ID+":"+row.EmailHash+":"+row.ChallengeID, row.CodeCiphertext)
	if err != nil || subtle.ConstantTimeCompare([]byte(expected), []byte(code)) != 1 {
		return ErrMerchantStoreEmailVerificationInvalid
	}
	return nil
}

// Checkout already holds the original guest row lock. Never authenticate a
// guest through buyer_id=0; a nonempty optional address needs the same proof as
// a merchant-required address. Existing-order replay runs before this policy.
func storeRequireGuestCheckoutEmail(tx *gorm.DB, guestID string, required bool, email string) error {
	if email == "" && !required {
		return nil
	}
	if email == "" || len(guestID) != 36 {
		return ErrMerchantStoreEmailUnverified
	}
	normalized, err := NormalizeMerchantStorePickupEmail(email)
	if err != nil || normalized != email {
		return ErrMerchantStoreInput
	}
	var guest MerchantStoreGuest
	if err = tx.First(&guest, "id = ? AND expires_at > ?", guestID, common.GetTimestamp()).Error; err != nil {
		return ErrMerchantStoreDenied
	}
	var row MerchantStoreGuestEmailVerification
	if err = tx.First(&row, "guest_id = ? AND email_hash = ? AND verified_at > 0", guestID, storeHash(email)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrMerchantStoreEmailUnverified
		}
		return err
	}
	return nil
}

func storeGuestOrderDeliveryEmail(tx *gorm.DB, o *MerchantStoreOrder) (string, error) {
	if o.BuyerID != 0 || len(o.GuestID) != 36 || o.PickupLoginRequired || o.PickupEmailHash == "" || o.PickupEmailCiphertext == "" {
		return "", ErrMerchantStoreDenied
	}
	// A paid obligation outlives the raw guest session. Only its immutable
	// identity and original verified address are used; no bearer is recovered.
	var guest MerchantStoreGuest
	if err := tx.First(&guest, "id = ?", o.GuestID).Error; err != nil {
		return "", ErrMerchantStoreDenied
	}
	email, err := storeDecrypt("order-pickup-email", o.ID, o.PickupEmailCiphertext)
	if err != nil || !storeEmailValid(email) || storeHash(email) != o.PickupEmailHash {
		return "", ErrMerchantStoreDenied
	}
	var fact MerchantStoreGuestEmailVerification
	if err = tx.First(&fact, "guest_id = ? AND email_hash = ? AND verified_at > 0", o.GuestID, o.PickupEmailHash).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", ErrMerchantStoreEmailUnverified
		}
		return "", err
	}
	return email, nil
}
