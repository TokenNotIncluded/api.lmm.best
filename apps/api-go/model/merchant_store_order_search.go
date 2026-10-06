package model

import (
	cryptorand "crypto/rand"
	"crypto/subtle"
	"errors"
	"math/big"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A public order search proves ownership of the address entered at checkout.
// It intentionally never marks an account address as verified or resolves an
// address through an account's mutable email field.
type MerchantStoreOrderSearchChallenge struct {
	EmailHash          string  `json:"-" gorm:"primaryKey;size:64"`
	ChallengeTokenHash string  `json:"-" gorm:"size:64;index"`
	EmailCiphertext    string  `json:"-" gorm:"type:text"`
	CodeCiphertext     string  `json:"-" gorm:"type:text"`
	ExpiresAt          int64   `json:"-"`
	Attempts           int     `json:"-"`
	SentAt             int64   `json:"-" gorm:"index"`
	SentTimes          []int64 `json:"-" gorm:"serializer:json;type:text"`
	ConsumedAt         int64   `json:"-"`
}

type MerchantStoreOrderSearchAuthorization struct {
	TokenHash string `json:"-" gorm:"primaryKey;size:64"`
	EmailHash string `json:"-" gorm:"size:64;not null;index"`
	CreatedAt int64  `json:"-"`
	ExpiresAt int64  `json:"-" gorm:"index"`
}

// Only these fields may be sent to an unauthenticated search caller. Number
// search never includes PickupToken. A verified-address search may include it
// internally for the controller to produce a pickup URL; claim authorization
// still independently enforces the order's code and login requirements.
type MerchantStoreOrderSearchSummary struct {
	ID                  string `json:"id"`
	TradeNo             string `json:"trade_no"`
	ProductTitle        string `json:"product_title"`
	Quantity            int    `json:"quantity"`
	Status              string `json:"status"`
	CreatedAt           int64  `json:"created_at"`
	PaidAt              int64  `json:"paid_at"`
	ExpiresAt           int64  `json:"expires_at"`
	ClaimedAt           int64  `json:"claimed_at"`
	PickupLoginRequired bool   `json:"pickup_login_required"`
	PickupCodeRequired  bool   `json:"pickup_code_required"`
	PickupURL           string `json:"pickup_url,omitempty"`
	RawOrderID          string `json:"-"`
	PickupToken         string `json:"-"`
}

func NormalizeMerchantStorePickupEmail(email string) (string, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return "", nil
	}
	if !storeEmailValid(email) {
		return "", ErrMerchantStoreInput
	}
	// Local parts can be case-sensitive even though common providers ignore
	// case. Never merge distinct mailbox owners; normalize only the domain.
	at := strings.LastIndexByte(email, '@')
	email = email[:at+1] + strings.ToLower(email[at+1:])
	return email, nil
}

// The sender reads this before delivery and subtracts the current timestamp
// afterward. Resending preserves the original deadline, so its remaining
// lifetime must not be reported as a fresh ten-minute window.
func GetMerchantStoreOrderSearchChallengeExpires(challengeID string) (int64, error) {
	if !storeTokenValid(challengeID) {
		return 0, ErrMerchantStoreEmailVerificationInvalid
	}
	var challenge MerchantStoreOrderSearchChallenge
	err := DB.Select("expires_at").First(&challenge, "challenge_token_hash = ?", storeHash(challengeID)).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, ErrMerchantStoreEmailVerificationInvalid
	}
	return challenge.ExpiresAt, err
}

// BeginMerchantStoreOrderSearch returns a code only to the trusted email
// sender. The public response must expose only challengeID, never code or
// normalizedEmail. A challenge is created even when the address has no orders.
func BeginMerchantStoreOrderSearch(email string) (challengeID, code, normalizedEmail string, err error) {
	normalizedEmail, err = NormalizeMerchantStorePickupEmail(email)
	if err != nil || normalizedEmail == "" {
		return "", "", "", ErrMerchantStoreInput
	}
	emailHash := storeHash(normalizedEmail)
	err = marketTransaction(DB, func(tx *gorm.DB) error {
		// Inserting the address row first gives concurrent first sends the same
		// durable lock, including when no challenge previously existed.
		seed := MerchantStoreOrderSearchChallenge{EmailHash: emailHash}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; e != nil {
			return e
		}
		var challenge MerchantStoreOrderSearchChallenge
		if e := lockForUpdate(tx).First(&challenge, "email_hash = ?", emailHash).Error; e != nil {
			return e
		}
		now := common.GetTimestamp()
		if challenge.SentAt+60 > now || (challenge.ExpiresAt > now && challenge.Attempts >= 5) {
			return ErrMerchantStoreEmailVerificationCooldown
		}
		// A rolling hour, rather than a resettable fixed-hour counter, avoids
		// bursts at window boundaries. Retain at most ten timestamps.
		recent := make([]int64, 0, 10)
		for _, sent := range challenge.SentTimes {
			if sent > now-3600 {
				recent = append(recent, sent)
			}
		}
		if len(recent) >= 10 {
			return ErrMerchantStoreEmailVerificationCooldown
		}
		var e error
		challengeID, e = storeToken()
		if e != nil {
			return e
		}
		n, e := cryptorand.Int(cryptorand.Reader, big.NewInt(1000000))
		if e != nil {
			return e
		}
		digits := strconv.FormatInt(n.Int64(), 10)
		code = strings.Repeat("0", 6-len(digits)) + digits
		tokenHash := storeHash(challengeID)
		emailCipher, e := storeEncrypt("order-search-email", emailHash, normalizedEmail)
		if e != nil {
			return e
		}
		codeCipher, e := storeEncrypt("order-search-code", tokenHash+":"+emailHash, code)
		if e != nil {
			return e
		}
		if challenge.ExpiresAt <= now {
			challenge.Attempts = 0
			challenge.ExpiresAt = now + 600
		}
		challenge.ChallengeTokenHash = tokenHash
		challenge.EmailCiphertext = emailCipher
		challenge.CodeCiphertext = codeCipher
		challenge.SentAt = now
		challenge.SentTimes = append(recent, now)
		challenge.ConsumedAt = 0
		return tx.Save(&challenge).Error
	})
	if err != nil {
		return "", "", "", err
	}
	return challengeID, code, normalizedEmail, nil
}

func ConfirmMerchantStoreOrderSearch(challengeID, code string) (string, error) {
	if !storeTokenValid(challengeID) {
		return "", ErrMerchantStoreEmailVerificationInvalid
	}
	validShape := len(code) == 6
	for _, r := range code {
		if r < '0' || r > '9' {
			validShape = false
		}
	}
	tokenHash := storeHash(challengeID)
	// This unlocked lookup is only a locator. Authorization below locks the
	// same email primary-key row as Begin and rechecks the current token.
	var lookup MerchantStoreOrderSearchChallenge
	err := DB.Select("email_hash").First(&lookup, "challenge_token_hash = ?", tokenHash).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrMerchantStoreEmailVerificationInvalid
	} else if err != nil {
		return "", err
	}
	var resultError error
	var searchToken string
	err = marketTransaction(DB, func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "sqlite" {
			// SQLite has no row locks. Acquire its writer lock before reading
			// or comparing the code; a busy writer never gets a free guess.
			if err := tx.Model(&MerchantStoreOrderSearchChallenge{}).Where("email_hash = ?", lookup.EmailHash).UpdateColumn("attempts", gorm.Expr("attempts")).Error; err != nil {
				return err
			}
		}
		var challenge MerchantStoreOrderSearchChallenge
		err := lockForUpdate(tx).First(&challenge, "email_hash = ?", lookup.EmailHash).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			resultError = ErrMerchantStoreEmailVerificationInvalid
			return nil
		} else if err != nil {
			return err
		}
		now := common.GetTimestamp()
		if challenge.ChallengeTokenHash != tokenHash || challenge.ExpiresAt <= now || challenge.Attempts >= 5 || challenge.ConsumedAt != 0 || challenge.CodeCiphertext == "" {
			resultError = ErrMerchantStoreEmailVerificationInvalid
			return nil
		}
		expected, err := storeDecrypt("order-search-code", challenge.ChallengeTokenHash+":"+challenge.EmailHash, challenge.CodeCiphertext)
		if err != nil {
			return err
		}
		if !validShape || subtle.ConstantTimeCompare([]byte(expected), []byte(code)) != 1 {
			// Return the validation error after commit so incorrect guesses
			// cannot roll their own attempt counter back.
			resultError = ErrMerchantStoreEmailVerificationInvalid
			return tx.Model(&challenge).Update("attempts", challenge.Attempts+1).Error
		}
		searchToken, err = storeToken()
		if err != nil {
			return err
		}
		authorization := MerchantStoreOrderSearchAuthorization{TokenHash: storeHash(searchToken), EmailHash: challenge.EmailHash, CreatedAt: now, ExpiresAt: now + 900}
		if err := tx.Create(&authorization).Error; err != nil {
			return err
		}
		return tx.Model(&challenge).Updates(map[string]any{"consumed_at": now, "challenge_token_hash": "", "code_ciphertext": "", "email_ciphertext": ""}).Error
	})
	if err != nil {
		return "", err
	}
	if resultError != nil {
		return "", resultError
	}
	return searchToken, nil
}

func storeOrderSearchSummary(o MerchantStoreOrder) MerchantStoreOrderSearchSummary {
	return MerchantStoreOrderSearchSummary{ID: o.TradeNo, TradeNo: o.TradeNo, ProductTitle: o.ProductTitle, Quantity: o.Quantity, Status: o.Status, CreatedAt: o.CreatedAt, PaidAt: o.PaidAt, ExpiresAt: o.ExpiresAt, ClaimedAt: o.ClaimedAt, PickupLoginRequired: o.PickupLoginRequired, PickupCodeRequired: o.PickupCodeRequired || o.PickupCodeHash != "", RawOrderID: o.ID}
}

func ListMerchantStoreOrdersByVerifiedEmail(searchToken string, offset, limit int) ([]MerchantStoreOrderSearchSummary, error) {
	if !storeTokenValid(searchToken) {
		return nil, ErrMerchantStoreDenied
	}
	var authorization MerchantStoreOrderSearchAuthorization
	err := DB.First(&authorization, "token_hash = ? AND expires_at > ?", storeHash(searchToken), common.GetTimestamp()).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrMerchantStoreDenied
	} else if err != nil {
		return nil, err
	}
	if authorization.EmailHash == "" {
		return nil, ErrMerchantStoreDenied
	}
	if offset < 0 || offset > 100000 || limit < 1 || limit > 100 {
		return nil, ErrMerchantStoreInput
	}
	var rows []MerchantStoreOrder
	// Legacy rows without an address snapshot are deliberately excluded:
	// resolving their buyer's current account email could expose old orders
	// to a different mailbox owner after an account address change.
	err = DB.Where("pickup_email_hash = ?", authorization.EmailHash).Order("created_at DESC,id ASC").Offset(offset).Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	summaries := make([]MerchantStoreOrderSearchSummary, 0, len(rows))
	for _, row := range rows {
		summary := storeOrderSearchSummary(row)
		if row.Status == "paid" || row.Status == "refund_pending" {
			summary.PickupToken, err = storeDecrypt("pickup", row.ID, row.PickupTokenCiphertext)
			if err != nil {
				return nil, err
			}
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func GetMerchantStoreOrderSearchSummary(tradeNo string) (*MerchantStoreOrderSearchSummary, error) {
	return GetMerchantStoreOrderSearchSummaryForActor(tradeNo, 0)
}

func GetMerchantStoreOrderSearchSummaryForActor(tradeNo string, actorID int) (*MerchantStoreOrderSearchSummary, error) {
	return GetMerchantStoreOrderSearchSummaryWithGuest(tradeNo, actorID, "")
}

func GetMerchantStoreOrderSearchSummaryWithGuest(tradeNo string, actorID int, guestToken string) (*MerchantStoreOrderSearchSummary, error) {
	if !storeValidTradeNo(tradeNo) {
		return nil, ErrMerchantStoreInput
	}
	var row MerchantStoreOrder
	if err := DB.Scopes(marketExactTextScope("trade_no", tradeNo)).First(&row).Error; err != nil {
		return nil, err
	}
	if row.GuestID != "" {
		allowed := false
		if actorID > 0 {
			actor, e := storeUser(DB, actorID, common.RoleCommonUser)
			allowed = e == nil && (actorID == row.SellerID || actor.Role >= common.RoleRootUser)
		}
		if !allowed && guestToken != "" {
			guest, e := ResolveMerchantStoreGuest(DB, guestToken)
			allowed = e == nil && row.BuyerID == 0 && guest.ID == row.GuestID
		}
		if !allowed {
			return nil, gorm.ErrRecordNotFound
		}
	}
	// Legacy numbers were derived from a buyer's idempotency key. They can
	// be predictable and must retain the old authenticated access boundary.
	if len(row.ID) >= 30 && row.TradeNo == "MS"+row.ID[:30] {
		if actorID <= 0 || (actorID != row.BuyerID && actorID != row.SellerID) {
			return nil, gorm.ErrRecordNotFound
		}
		if _, err := storeUser(DB, actorID, common.RoleCommonUser); err != nil {
			return nil, gorm.ErrRecordNotFound
		}
	}
	summary := storeOrderSearchSummary(row)
	return &summary, nil
}

func CleanupMerchantStoreOrderSearch(now int64) error {
	if now <= 0 {
		return ErrMerchantStoreInput
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(&MerchantStoreOrderSearchAuthorization{}).Error; err != nil {
			return err
		}
		// Keep address history well beyond the rolling hour and live OTP
		// window; cleanup must not restore a blocked sender's budget.
		return tx.Where("sent_at <= ? AND expires_at <= ?", now-86400, now).Delete(&MerchantStoreOrderSearchChallenge{}).Error
	})
}
