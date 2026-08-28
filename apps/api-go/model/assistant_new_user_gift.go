package model

import (
	cryptorand "crypto/rand"
	"encoding/hex"
	"errors"
	"math"
	"net"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AssistantGiftOffered  = "offered"
	AssistantGiftClaimed  = "claimed"
	AssistantGiftDeclined = "declined"
	assistantGiftMaxCents = 1000
	assistantGiftRiskAge  = 30 * 24 * time.Hour
	assistantGiftIPLimit  = 3
)

var (
	ErrAssistantGiftIneligible  = errors.New("new-user assistant gift is not available")
	ErrAssistantGiftInvalid     = errors.New("new-user assistant gift decision is invalid")
	ErrAssistantGiftUnavailable = errors.New("new-user assistant gift cannot be claimed")
	ErrAssistantGiftAbuse       = errors.New("new-user assistant gift risk limit reached")
)

// AssistantGiftError preserves the coarse sentinel used by callers while
// attaching a safe, non-identifying reason for UI/support. The code never
// includes an email, IP, account age, or another user's state.
type AssistantGiftError struct {
	Code  string
	Cause error
}

func (err *AssistantGiftError) Error() string {
	if err == nil || err.Cause == nil {
		return "new-user assistant gift decision failed"
	}
	return err.Cause.Error()
}

func (err *AssistantGiftError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

func assistantGiftError(code string, cause error) error {
	return &AssistantGiftError{Code: code, Cause: cause}
}

func AssistantGiftErrorCode(err error) string {
	var giftErr *AssistantGiftError
	if errors.As(err, &giftErr) && giftErr != nil {
		return giftErr.Code
	}
	return ""
}

const (
	assistantGiftRiskIdentity = "identity"
	assistantGiftRiskNetwork  = "network"
	assistantGiftRiskKeyID    = "assistant-gift-risk-v1"
)

// AssistantGiftRiskKey keeps the HMAC key used by the global gift-risk
// ledger in the database. CryptoSecret is normally shared by all nodes, but
// its development fallback is process-local and changes after a restart. A
// persisted key makes the identity/network dedupe boundary independent of
// process lifetime while keeping raw identifiers out of the database.
type AssistantGiftRiskKey struct {
	Id        string `json:"-" gorm:"primaryKey;type:varchar(64)"`
	Secret    string `json:"-" gorm:"type:varchar(255);not null"`
	CreatedAt int64  `json:"-" gorm:"not null"`
}

func (AssistantGiftRiskKey) TableName() string { return "assistant_gift_risk_keys" }

// AssistantGiftRiskMemory is the privacy-minimized global fraud ledger for
// welcome gifts. KeyHash is an HMAC of either a provider-aware email identity
// or a normalized IP address; raw identifiers and transcripts are never
// stored or exposed to the assistant. Identity rows never reset. Network rows
// reuse one bounded counter per address and roll forward every 30 days.
type AssistantGiftRiskMemory struct {
	KeyHash         string `json:"-" gorm:"type:char(64);primaryKey"`
	Kind            string `json:"-" gorm:"type:varchar(16);not null;index"`
	DecisionCount   int    `json:"-" gorm:"not null;default:0"`
	WindowStartedAt int64  `json:"-" gorm:"not null"`
	UpdatedAt       int64  `json:"-" gorm:"not null;index"`
}

func (AssistantGiftRiskMemory) TableName() string { return "assistant_gift_risk_memories" }

// AssistantNewUserGift is a one-time, user-scoped decision. AmountCents and
// Quota are both persisted so a later exchange-rate change cannot alter an
// already presented gift. A zero-dollar decision is retained as declined and
// consumes the same single opportunity.
type AssistantNewUserGift struct {
	Id             int64  `json:"id" gorm:"primaryKey"`
	UserId         int    `json:"-" gorm:"not null;uniqueIndex"`
	ConversationId int64  `json:"conversation_id,omitempty" gorm:"not null;default:0;index"`
	AmountCents    int    `json:"amount_cents" gorm:"not null"`
	Quota          int    `json:"quota" gorm:"not null"`
	Status         string `json:"status" gorm:"type:varchar(20);not null;index"`
	Reason         string `json:"reason" gorm:"type:varchar(240);not null"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;index"`
	ClaimedAt      int64  `json:"claimed_at" gorm:"not null;default:0"`
}

func (AssistantNewUserGift) TableName() string { return "assistant_new_user_gifts" }

func GetAssistantNewUserGift(userID int) (*AssistantNewUserGift, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	var gift AssistantNewUserGift
	err := DB.Where("user_id = ?", userID).First(&gift).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &gift, err
}

// DecideAssistantNewUserGift persists the model's bounded decision exactly
// once. Conversation evidence is calculated by trusted controller code; it is
// not accepted from a browser. The database uniqueness constraint is the
// final race boundary across retries and instances.
func DecideAssistantNewUserGift(userID int, conversationID int64, amountCents int, reason string, substantiveTurns int, substantiveRunes int, clientIP string) (*AssistantNewUserGift, bool, error) {
	if userID <= 0 || conversationID < 0 || amountCents < 0 || amountCents > assistantGiftMaxCents {
		return nil, false, assistantGiftError("invalid_decision", ErrAssistantGiftInvalid)
	}
	// The controller counts each user turn only when it has at least four
	// runes. Requiring two such turns is the actual product rule; a second
	// language-dependent total-rune threshold would reject concise but valid
	// conversations (for example, short Chinese project descriptions).
	if substantiveTurns < 2 || substantiveRunes < 8 {
		return nil, false, assistantGiftError("insufficient_conversation", ErrAssistantGiftInvalid)
	}
	reason = strings.TrimSpace(redactAssistantHandoffMessage(reason))
	if len([]rune(reason)) < 2 || len([]rune(reason)) > 240 {
		return nil, false, assistantGiftError("invalid_decision", ErrAssistantGiftInvalid)
	}

	if existing, err := GetAssistantNewUserGift(userID); err != nil || existing != nil {
		return existing, false, err
	}

	var user User
	if err := DB.Select("id", "email", "role", "status", "created_at", "last_api_activity_at", "trust_level_override", "console_activated_at").First(&user, "id = ?", userID).Error; err != nil {
		return nil, false, err
	}
	if user.Role != common.RoleCommonUser || user.Status != common.UserStatusEnabled || strings.TrimSpace(user.Email) == "" || IsDisposableEmail(user.Email) {
		return nil, false, assistantGiftError("account_not_eligible", ErrAssistantGiftIneligible)
	}
	quota := int(math.Round(float64(amountCents) * common.QuotaPerUnit / 100))
	if quota < 0 || (amountCents > 0 && quota <= 0) {
		return nil, false, assistantGiftError("invalid_decision", ErrAssistantGiftInvalid)
	}
	status := AssistantGiftOffered
	if amountCents == 0 {
		status = AssistantGiftDeclined
	}
	gift := AssistantNewUserGift{
		UserId:         userID,
		ConversationId: conversationID,
		AmountCents:    amountCents,
		Quota:          quota,
		Status:         status,
		Reason:         reason,
		CreatedAt:      common.GetTimestamp(),
	}
	createdDecision := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		var existing AssistantNewUserGift
		existingErr := lockForUpdate(tx).Where("user_id = ?", userID).First(&existing).Error
		if existingErr == nil {
			gift = existing
			return nil
		}
		if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}
		riskSecret, err := getAssistantGiftRiskSecret(tx)
		if err != nil {
			return assistantGiftError("risk_check_unavailable", ErrAssistantGiftIneligible)
		}
		identityHash, networkHash, err := assistantGiftRiskKeysWithSecret(riskSecret, user.Email, clientIP)
		if err != nil {
			return assistantGiftError("risk_check_unavailable", ErrAssistantGiftIneligible)
		}
		nowTimestamp := common.GetTimestamp()
		if err := reserveAssistantGiftRisk(tx, identityHash, assistantGiftRiskIdentity, nowTimestamp, 1, 0); err != nil {
			if errors.Is(err, ErrAssistantGiftAbuse) {
				return assistantGiftError("identity_already_used", err)
			}
			return err
		}
		if err := reserveAssistantGiftRisk(tx, networkHash, assistantGiftRiskNetwork, nowTimestamp, assistantGiftIPLimit, int64(assistantGiftRiskAge/time.Second)); err != nil {
			if errors.Is(err, ErrAssistantGiftAbuse) {
				return assistantGiftError("network_limit_reached", err)
			}
			return err
		}
		if err := tx.Create(&gift).Error; err != nil {
			return err
		}
		createdDecision = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &gift, createdDecision, nil
}

// getAssistantGiftRiskSecret returns the installation-wide HMAC key. The
// insert is conflict-safe so two instances bootstrapping the first gift at
// the same time converge on one durable key. Existing deployments that have
// configured CRYPTO_SECRET keep using that value when the row is first
// created, preserving existing risk hashes during this migration.
func getAssistantGiftRiskSecret(tx *gorm.DB) (string, error) {
	if tx == nil {
		return "", gorm.ErrInvalidData
	}
	var stored AssistantGiftRiskKey
	if err := tx.Where("id = ?", assistantGiftRiskKeyID).First(&stored).Error; err == nil {
		stored.Secret = strings.TrimSpace(stored.Secret)
		if stored.Secret == "" {
			return "", ErrAssistantGiftInvalid
		}
		return stored.Secret, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}

	seed := strings.TrimSpace(common.CryptoSecret)
	if seed == "" {
		raw := make([]byte, 32)
		if _, err := cryptorand.Read(raw); err != nil {
			return "", err
		}
		seed = hex.EncodeToString(raw)
	}
	candidate := AssistantGiftRiskKey{
		Id:        assistantGiftRiskKeyID,
		Secret:    seed,
		CreatedAt: common.GetTimestamp(),
	}
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate)
	if result.Error != nil {
		return "", result.Error
	}
	if err := tx.Where("id = ?", assistantGiftRiskKeyID).First(&stored).Error; err != nil {
		return "", err
	}
	stored.Secret = strings.TrimSpace(stored.Secret)
	if stored.Secret == "" {
		return "", ErrAssistantGiftInvalid
	}
	return stored.Secret, nil
}

func assistantGiftRiskKeys(email, clientIP string) (string, string, error) {
	return assistantGiftRiskKeysWithSecret(common.CryptoSecret, email, clientIP)
}

func assistantGiftRiskKeysWithSecret(secret, email, clientIP string) (string, string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", "", ErrAssistantGiftInvalid
	}
	identity := canonicalAssistantGiftEmail(email)
	ip := net.ParseIP(strings.TrimSpace(clientIP))
	if identity == "" || ip == nil {
		return "", "", ErrAssistantGiftInvalid
	}
	return common.GenerateHMACWithKey([]byte(secret), "assistant-gift-identity-v1:"+identity),
		common.GenerateHMACWithKey([]byte(secret), "assistant-gift-network-v1:"+ip.String()), nil
}

func canonicalAssistantGiftEmail(email string) string {
	email = NormalizeEmail(email)
	local, domain, found := strings.Cut(email, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") {
		return ""
	}
	switch domain {
	case "googlemail.com":
		domain = "gmail.com"
		fallthrough
	case "gmail.com":
		local = strings.ReplaceAll(local, ".", "")
		if plus := strings.IndexByte(local, '+'); plus >= 0 {
			local = local[:plus]
		}
	case "hotmail.com", "live.com", "outlook.com":
		if plus := strings.IndexByte(local, '+'); plus >= 0 {
			local = local[:plus]
		}
	}
	if local == "" {
		return ""
	}
	return local + "@" + domain
}

func reserveAssistantGiftRisk(tx *gorm.DB, keyHash, kind string, now int64, limit int, windowSeconds int64) error {
	if tx == nil || keyHash == "" || limit <= 0 {
		return ErrAssistantGiftInvalid
	}
	seed := AssistantGiftRiskMemory{
		KeyHash: keyHash, Kind: kind, WindowStartedAt: now, UpdatedAt: now,
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return err
	}
	var memory AssistantGiftRiskMemory
	if err := lockForUpdate(tx).Where("key_hash = ?", keyHash).First(&memory).Error; err != nil {
		return err
	}
	if memory.Kind != kind {
		return ErrAssistantGiftInvalid
	}
	count := memory.DecisionCount
	windowStart := memory.WindowStartedAt
	if windowSeconds > 0 && (windowStart <= 0 || now-windowStart >= windowSeconds) {
		count = 0
		windowStart = now
	}
	if count >= limit {
		return ErrAssistantGiftAbuse
	}
	return tx.Model(&AssistantGiftRiskMemory{}).Where("key_hash = ?", keyHash).Updates(map[string]any{
		"decision_count":    count + 1,
		"window_started_at": windowStart,
		"updated_at":        now,
	}).Error
}

// ClaimAssistantNewUserGift credits the stored quota and marks the gift in
// one transaction. Repeated claims return the same row without another credit.
func ClaimAssistantNewUserGift(userID int) (*AssistantNewUserGift, bool, error) {
	if userID <= 0 {
		return nil, false, gorm.ErrInvalidData
	}
	var gift AssistantNewUserGift
	alreadyClaimed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		if err := lockForUpdate(tx).Where("user_id = ?", userID).First(&gift).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssistantGiftUnavailable
			}
			return err
		}
		if gift.Status == AssistantGiftClaimed {
			alreadyClaimed = true
			return nil
		}
		if gift.Status != AssistantGiftOffered || gift.AmountCents <= 0 || gift.Quota <= 0 {
			return ErrAssistantGiftUnavailable
		}
		result := tx.Model(&User{}).
			Where("id = ? AND status = ?", userID, common.UserStatusEnabled).
			Update("quota", gorm.Expr("quota + ?", gift.Quota))
		if result.Error != nil {
			return result.Error
		}
		// Do not consume the durable gift row when the authenticated account was
		// disabled or removed between issuing and claiming the gift. Without the
		// affected-row check the transaction could mark the gift claimed while
		// no quota was actually credited, making a legitimate retry impossible.
		if result.RowsAffected != 1 {
			return ErrAssistantGiftUnavailable
		}
		gift.Status = AssistantGiftClaimed
		gift.ClaimedAt = common.GetTimestamp()
		return tx.Model(&gift).Updates(map[string]any{
			"status":     gift.Status,
			"claimed_at": gift.ClaimedAt,
		}).Error
	})
	if err != nil {
		return nil, false, err
	}
	if !alreadyClaimed {
		if err := cacheIncrUserQuota(userID, int64(gift.Quota)); err != nil {
			common.SysLog("failed to update new-user assistant gift quota cache: " + err.Error())
		}
	}
	return &gift, alreadyClaimed, nil
}
