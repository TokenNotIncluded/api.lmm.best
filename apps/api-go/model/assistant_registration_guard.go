package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/registrationguard"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AssistantRegistrationAutoSuspendOption = "AssistantRegistrationAutoSuspendEnabled"
	AssistantRegistrationDailyCapOption    = "AssistantRegistrationDailySuspendCap"
	registrationWindow                     = int64(7 * 24 * 60 * 60)
	AssistantRegistrationTerminationNotice = "这段对话已暂停，注册核验需要进一步确认。已向管理员的站内风险收件箱提交记录；这不是仅凭说话风格作出的判断。若账号仍可登录，可以转人工说明情况。\n\nThis conversation is paused for registration verification. A record was saved to the administrator risk inbox. This is not a judgment based only on writing style; use human support if your account is still accessible."
)

var ErrAssistantRegistrationCheck = errors.New("registration verification needs more evidence or support")

// The correlation index never stores conversation text, raw email or IP. It is
// separate from user-deletable chat history so clearing chat is not a risk reset.
type AssistantRegistrationProfile struct {
	UserID       int    `gorm:"primaryKey" json:"-"`
	IdentityHash string `gorm:"type:char(64);index" json:"-"`
	NetworkHash  string `gorm:"type:char(64);index" json:"-"`
	ObservedAt   int64  `gorm:"index" json:"-"`
}

type AssistantRegistrationFingerprint struct {
	ID            int64  `gorm:"primaryKey" json:"-"`
	UserID        int    `gorm:"not null;uniqueIndex:idx_registration_fingerprint,priority:1;index" json:"-"`
	MessageDigest string `gorm:"type:char(64);not null;uniqueIndex:idx_registration_fingerprint,priority:2" json:"-"`
	Token         string `gorm:"type:char(64);not null;uniqueIndex:idx_registration_fingerprint,priority:3;index" json:"-"`
	CreatedAt     int64  `gorm:"not null;index" json:"-"`
}

type AssistantRegistrationCase struct {
	UserID        int    `gorm:"primaryKey" json:"user_id"`
	State         string `gorm:"type:varchar(24);not null" json:"state"`
	ReleasedUntil int64  `json:"-"`
	UpdatedAt     int64  `json:"updated_at"`
}

// Events are also the durable in-site administrator inbox. A returned receipt
// means this row committed, not that an email or push notification was delivered.
type AssistantRegistrationEvent struct {
	ID             int64  `gorm:"primaryKey" json:"id"`
	DedupeKey      string `gorm:"type:varchar(120);not null;uniqueIndex" json:"-"`
	UserID         int    `gorm:"not null;index" json:"user_id"`
	ConversationID int64  `json:"conversation_id"`
	Action         string `gorm:"type:varchar(24);not null;index" json:"action"`
	PolicyVersion  string `gorm:"type:varchar(32);not null" json:"policy_version"`
	Evidence       string `gorm:"type:text;not null" json:"evidence"`
	AdminID        int    `json:"admin_id,omitempty"`
	CreatedAt      int64  `gorm:"not null;index" json:"created_at"`
}

type AssistantRegistrationSummary struct {
	Evidence      registrationguard.Evidence `json:"signals"`
	Decision      registrationguard.Decision `json:"decision"`
	PolicyVersion string                     `json:"policy_version"`
	ObservedAt    int64                      `json:"observed_at"`
}

func RegistrationGuardMigrationModels() []any {
	return []any{&AssistantRegistrationProfile{}, &AssistantRegistrationFingerprint{}, &AssistantRegistrationCase{}, &AssistantRegistrationEvent{}}
}

func ValidateRegistrationGuardOption(key, value string) error {
	switch key {
	case AssistantRegistrationAutoSuspendOption:
		if value != "true" && value != "false" {
			return errors.New("automatic suspension must be true or false")
		}
	case AssistantRegistrationDailyCapOption:
		cap, err := strconv.Atoi(value)
		if err != nil || cap < 0 || cap > 5 {
			return errors.New("automatic suspension daily cap must be between 0 and 5")
		}
	}
	return nil
}

// Email uses the existing reward ledger canonicalization. A server-bound OAuth
// subject can support admission without an email, but is never described as proof
// that the person has no other account. Unverified client claims are not accepted.
func registrationIdentity(secret string, user *User) string {
	if email := canonicalAssistantGiftEmail(user.Email); email != "" {
		return common.GenerateHMACWithKey([]byte(secret), "assistant-gift-identity-v1:"+email)
	}
	for _, subject := range []struct{ provider, id string }{{"github", user.GitHubId}, {"oidc", user.OidcId}, {"linuxdo", user.LinuxDOId}, {"discord", user.DiscordId}, {"telegram", user.TelegramId}, {"wechat", user.WeChatId}} {
		if subject.id != "" {
			return common.GenerateHMACWithKey([]byte(secret), "assistant-registration-oauth-v1:"+subject.provider+":"+subject.id)
		}
	}
	return ""
}

// ObserveAssistantRegistration is called only with the authenticated actor,
// server-resolved ClientIP and current user message, before switching to relay
// billing credentials. Similarity is bounded and never treated as proof of AI use.
func ObserveAssistantRegistration(userID int, clientIP, message string) error {
	if userID <= 0 {
		return gorm.ErrInvalidData
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		if user.Role != common.RoleCommonUser || user.Status != common.UserStatusEnabled {
			return nil
		}
		secret, err := getAssistantGiftRiskSecret(tx)
		if err != nil {
			return err
		}
		identity := registrationIdentity(secret, &user)
		ip := net.ParseIP(strings.TrimSpace(clientIP))
		if identity == "" || ip == nil {
			return ErrAssistantRegistrationCheck
		}
		network := common.GenerateHMACWithKey([]byte(secret), "assistant-gift-network-v1:"+ip.String())
		now := common.GetTimestamp()
		profile := AssistantRegistrationProfile{UserID: userID, IdentityHash: identity, NetworkHash: network, ObservedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.AssignmentColumns([]string{"identity_hash", "network_hash", "observed_at"})}).Create(&profile).Error; err != nil {
			return err
		}
		// Redact BEFORE hashing: a pasted key or contact address must not become a
		// cross-account join key, even when the stored value would be an HMAC.
		digest, tokens := registrationguard.Fingerprints(secret, RedactAssistantHistoryContent(message))
		for _, token := range tokens {
			row := AssistantRegistrationFingerprint{UserID: userID, MessageDigest: digest, Token: token, CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		// Keep at most 256 tokens/account, irrespective of chat length or repeats.
		var oldIDs []int64
		if err := tx.Model(&AssistantRegistrationFingerprint{}).Where("user_id = ?", userID).Order("id DESC").Offset(256).Pluck("id", &oldIDs).Error; err != nil {
			return err
		}
		if len(oldIDs) > 0 {
			if err := tx.Where("id IN ?", oldIDs).Delete(&AssistantRegistrationFingerprint{}).Error; err != nil {
				return err
			}
		}
		return tx.Where("user_id = ? AND created_at < ?", userID, now-registrationWindow).Delete(&AssistantRegistrationFingerprint{}).Error
	})
}

func registrationSummaryTx(tx *gorm.DB, userID int) (*AssistantRegistrationSummary, error) {
	var profile AssistantRegistrationProfile
	if err := tx.First(&profile, "user_id = ?", userID).Error; err != nil {
		return nil, ErrAssistantRegistrationCheck
	}
	now := common.GetTimestamp()
	if profile.IdentityHash == "" || profile.NetworkHash == "" || profile.ObservedAt > now+60 || now-profile.ObservedAt > 900 {
		return nil, ErrAssistantRegistrationCheck
	}
	var user User
	if err := tx.First(&user, userID).Error; err != nil {
		return nil, err
	}
	secret, err := getAssistantGiftRiskSecret(tx)
	if err != nil {
		return nil, err
	}
	if registrationIdentity(secret, &user) != profile.IdentityHash {
		return nil, ErrAssistantRegistrationCheck
	}
	summary := &AssistantRegistrationSummary{PolicyVersion: registrationguard.Version, ObservedAt: profile.ObservedAt}
	var ownGift int64
	if err := tx.Model(&AssistantNewUserGift{}).Where("user_id = ?", userID).Count(&ownGift).Error; err != nil {
		return nil, err
	}
	if ownGift == 0 {
		var used int64
		if err := tx.Model(&AssistantGiftRiskMemory{}).Where("key_hash = ? AND kind = ? AND decision_count > 0", profile.IdentityHash, assistantGiftRiskIdentity).Count(&used).Error; err != nil {
			return nil, err
		}
		summary.Evidence.IdentityRewardUsed = used > 0
	}
	var ownMessages int64
	if err := tx.Model(&AssistantRegistrationFingerprint{}).Where("user_id = ? AND created_at >= ?", userID, now-registrationWindow).Distinct("message_digest").Count(&ownMessages).Error; err != nil {
		return nil, err
	}
	if ownMessages >= 2 {
		ownTokens := tx.Model(&AssistantRegistrationFingerprint{}).Select("token").Where("user_id = ? AND created_at >= ?", userID, now-registrationWindow)
		peers := tx.Model(&AssistantRegistrationFingerprint{}).Select("user_id").Where("user_id <> ? AND created_at >= ? AND token IN (?)", userID, now-registrationWindow, ownTokens).Group("user_id").Having("COUNT(DISTINCT message_digest) >= 2 AND COUNT(DISTINCT token) >= 4")
		// Count only peers in the matching campaign; unrelated users on a campus
		// network cannot increase the independent network evidence.
		var templates, networks int64
		if err := tx.Table("(?) AS matching_peers", peers).Count(&templates).Error; err != nil {
			return nil, err
		}
		if err := tx.Model(&AssistantRegistrationProfile{}).Where("user_id IN (?) AND network_hash = ? AND observed_at >= ?", peers, profile.NetworkHash, now-registrationWindow).Count(&networks).Error; err != nil {
			return nil, err
		}
		summary.Evidence.TemplatePeers = int(templates)
		summary.Evidence.NetworkPeers = int(networks)
	}
	summary.Decision = registrationguard.Evaluate(summary.Evidence)
	return summary, nil
}

func GetAssistantRegistrationSummary(userID int) (*AssistantRegistrationSummary, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	return registrationSummaryTx(DB, userID)
}

func checkAssistantRegistrationTx(tx *gorm.DB, userID int) error {
	summary, err := registrationSummaryTx(tx, userID)
	if err != nil {
		return err
	}
	if summary.Decision.Hold {
		return ErrAssistantRegistrationCheck
	}
	return nil
}

// Shared by direct L1 grants and welcome-gift decision/claim transactions.
// There is no "LLM says safe" parameter and no client-created evidence ID.
func CheckAssistantRegistration(userID int) error { return checkAssistantRegistrationTx(DB, userID) }

func registrationEventTx(tx *gorm.DB, userID int, conversationID int64, action string, summary *AssistantRegistrationSummary, adminID int) (*AssistantRegistrationEvent, error) {
	now := common.GetTimestamp()
	evidence, err := json.Marshal(summary)
	if err != nil {
		return nil, err
	}
	event := AssistantRegistrationEvent{DedupeKey: fmt.Sprintf("%d:%s:%d", userID, action, now/86400), UserID: userID, ConversationID: conversationID, Action: action, PolicyVersion: registrationguard.Version, Evidence: string(evidence), AdminID: adminID, CreatedAt: now}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&event).Error; err != nil {
		return nil, err
	}
	if event.ID == 0 {
		if err := tx.Where("dedupe_key = ?", event.DedupeKey).First(&event).Error; err != nil {
			return nil, err
		}
	}
	return &event, nil
}

func registrationSuspendBudgetTx(tx *gorm.DB) (bool, error) {
	// This row serializes the cap across ALL nodes and target accounts.
	seed := Option{Key: "AssistantRegistrationSuspendBudget", Value: "0:0"}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
		return false, err
	}
	var budget Option
	if err := lockForUpdate(tx).Where("key = ?", seed.Key).First(&budget).Error; err != nil {
		return false, err
	}
	enabled, cap := true, 5
	var options []Option
	if err := tx.Where("key IN ?", []string{AssistantRegistrationAutoSuspendOption, AssistantRegistrationDailyCapOption}).Find(&options).Error; err != nil {
		return false, err
	}
	for _, option := range options {
		if err := ValidateRegistrationGuardOption(option.Key, option.Value); err != nil {
			return false, err
		}
		if option.Key == AssistantRegistrationAutoSuspendOption {
			enabled = option.Value == "true"
		} else {
			cap, _ = strconv.Atoi(option.Value)
		}
	}
	if !enabled || cap == 0 {
		return false, nil
	}
	var day int64
	var count int
	if _, err := fmt.Sscanf(budget.Value, "%d:%d", &day, &count); err != nil {
		return false, err
	}
	today := common.GetTimestamp() / 86400
	if day != today {
		count = 0
	}
	if count >= cap {
		return false, nil
	}
	return true, tx.Model(&budget).Update("value", fmt.Sprintf("%d:%d", today, count+1)).Error
}

// ApplyAssistantRegistrationAction never accepts a target selected by the model.
// Controller binds userID/conversationID to the signed-in conversation. Every
// mutation re-evaluates persisted evidence under the subject user row lock.
func ApplyAssistantRegistrationAction(userID int, conversationID int64, action string) (*AssistantRegistrationEvent, error) {
	if userID <= 0 || conversationID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	if action != "notify" && action != "end_conversation" && action != "suspend" {
		return nil, gorm.ErrInvalidData
	}
	var receipt *AssistantRegistrationEvent
	suspended := false
	var nextAuthVersion int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Role != common.RoleCommonUser || user.TrustLevelOverride != nil {
			return ErrAssistantRegistrationCheck
		}
		var conversation AssistantConversation
		if err := tx.First(&conversation, "id = ? AND user_id = ?", conversationID, userID).Error; err != nil {
			return err
		}
		var current AssistantRegistrationCase
		found := tx.Where("user_id = ?", userID).First(&current).Error
		if found != nil && !errors.Is(found, gorm.ErrRecordNotFound) {
			return found
		}
		if current.State == "suspended" && action == "suspend" && user.Status == common.UserStatusDisabled {
			var prior AssistantRegistrationEvent
			if err := tx.Where("user_id = ? AND action = ?", userID, "suspend").Order("id DESC").First(&prior).Error; err != nil {
				return err
			}
			receipt = &prior
			return nil
		}
		if user.Status != common.UserStatusEnabled {
			return ErrAssistantRegistrationCheck
		}
		access, err := GetDeveloperAccessStateForUserBaseWithTx(tx, user.ToBaseUser(), CurrentDeveloperAccessPolicy())
		if err != nil {
			return err
		}
		if access.Granted {
			return ErrAssistantRegistrationCheck
		}
		summary, err := registrationSummaryTx(tx, userID)
		if err != nil {
			return err
		}
		if !summary.Decision.Alert {
			return ErrAssistantRegistrationCheck
		}
		now := common.GetTimestamp()
		if action == "end_conversation" && !summary.Decision.Hold {
			return ErrAssistantRegistrationCheck
		}
		if action == "suspend" {
			if !summary.Decision.CanSuspend || current.ReleasedUntil > now {
				return ErrAssistantRegistrationCheck
			}
			allowed, err := registrationSuspendBudgetTx(tx)
			if err != nil {
				return err
			}
			if !allowed {
				action = "notify"
			} else {
				if err := tx.Model(&user).Update("status", common.UserStatusDisabled).Error; err != nil {
					return err
				}
				nextAuthVersion, err = IncrementUserAuthVersionWithTx(tx, userID)
				if err != nil {
					return err
				}
				suspended = true
			}
		}
		state := "needs_context"
		if summary.Decision.Hold {
			state = "held"
		}
		if action == "suspend" {
			state = "suspended"
		}
		if action == "end_conversation" || action == "suspend" {
			// Persist the terminal notice atomically with the restriction. Retry does
			// not append it twice and normal response persistence must not replay it.
			if conversation.RestrictedAt == 0 {
				if _, err := appendAssistantHistoryMessageTx(tx, conversationID, AssistantHistoryRoleAssistant, AssistantRegistrationTerminationNotice); err != nil {
					return err
				}
			}
			// Only this conversation ends; no arbitrary conversation ID or raw text.
			if err := tx.Model(&conversation).Updates(map[string]any{"restricted_at": now, "restriction_reason": "registration_guard", "updated_at": now}).Error; err != nil {
				return err
			}
		}
		row := AssistantRegistrationCase{UserID: userID, State: state, ReleasedUntil: current.ReleasedUntil, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.AssignmentColumns([]string{"state", "updated_at"})}).Create(&row).Error; err != nil {
			return err
		}
		receipt, err = registrationEventTx(tx, userID, conversationID, action, summary, 0)
		return err
	})
	if err != nil {
		return nil, err
	}
	if suspended {
		if err := publishCommittedUserAuthVersion(userID, nextAuthVersion); err != nil {
			common.SysError("registration suspension auth publication: " + err.Error())
		}
		if err := InvalidateUserCache(userID); err != nil {
			common.SysError("registration suspension cache invalidation: " + err.Error())
		}
	}
	return receipt, nil
}

func ReleaseAssistantRegistrationSuspension(adminID, userID int) (*AssistantRegistrationEvent, error) {
	var receipt *AssistantRegistrationEvent
	var nextAuthVersion int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var admin User
		if err := tx.First(&admin, adminID).Error; err != nil {
			return err
		}
		if admin.Status != common.UserStatusEnabled || admin.Role < common.RoleAdminUser || adminID == userID {
			return gorm.ErrInvalidData
		}
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Role != common.RoleCommonUser {
			return gorm.ErrInvalidData
		}
		var current AssistantRegistrationCase
		if err := tx.First(&current, "user_id = ?", userID).Error; err != nil {
			return err
		}
		if current.State != "suspended" {
			return ErrAssistantRegistrationCheck
		}
		if user.Status != common.UserStatusDisabled {
			return ErrAssistantRegistrationCheck
		}
		if err := tx.Model(&user).Update("status", common.UserStatusEnabled).Error; err != nil {
			return err
		}
		var versionErr error
		nextAuthVersion, versionErr = IncrementUserAuthVersionWithTx(tx, userID)
		if versionErr != nil {
			return versionErr
		}
		now := common.GetTimestamp()
		if err := tx.Model(&current).Updates(map[string]any{"state": "released", "released_until": now + registrationWindow, "updated_at": now}).Error; err != nil {
			return err
		}
		var err error
		receipt, err = registrationEventTx(tx, userID, 0, "released", nil, adminID)
		return err
	})
	if err == nil {
		if publishErr := publishCommittedUserAuthVersion(userID, nextAuthVersion); publishErr != nil {
			common.SysError("registration release auth publication: " + publishErr.Error())
		}
		_ = InvalidateUserCache(userID)
	}
	return receipt, err
}

// PruneAssistantRegistrationFingerprints is suitable for the existing retention
// job; neither welcome-gift identity decisions nor sanction audit records are
// discarded together with ordinary chat history.
func PruneAssistantRegistrationFingerprints(now time.Time) error {
	return DB.Where("created_at < ?", now.Unix()-registrationWindow).Delete(&AssistantRegistrationFingerprint{}).Error
}

// RegistrationPublicState deliberately omits evidence, peer IDs, hashes and
// thresholds. "ready" is eligibility, not a claim to have proved personhood.
func RegistrationPublicState(userID int) string {
	var current AssistantRegistrationCase
	if DB.First(&current, "user_id = ?", userID).Error == nil && current.State == "suspended" {
		return "suspended"
	}
	summary, err := GetAssistantRegistrationSummary(userID)
	if err != nil {
		return "context_needed"
	}
	if summary.Decision.Hold {
		return "held"
	}
	return "ready"
}

func isRegistrationGuardOption(key string) bool {
	return strings.HasPrefix(key, "AssistantRegistration")
}
