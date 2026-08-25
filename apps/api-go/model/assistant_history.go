package model

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	AssistantHistoryPrivacyNotice = "你与助手的对话不是私密通信，请勿发送个人信息、密码、API Key、Cookie 或其他凭证。敏感内容会被自动脱敏。"

	AssistantHistoryRoleUser      = "user"
	AssistantHistoryRoleAssistant = "assistant"
	AssistantHistoryRoleCard      = "secure_card"

	AssistantSecureCardTypeAPIKey = "api_key"

	assistantConversationTitleMaxRunes = 120
	assistantHistoryMessageMaxRunes    = 4000
	assistantHistoryPageMax            = 100
	assistantHistorySecureCardMax      = 100
	// A conversation is intentionally bounded independently of the retention
	// task.  Retention handles old conversations; these limits protect an
	// active conversation from becoming an unbounded in-process/database
	// payload when a client keeps chatting for months.
	assistantHistoryConversationMaxMessages = 128
	assistantHistoryConversationMaxBytes    = 256 * 1024
	assistantHistoryTrimBatch               = 256
	assistantSecureCardPayloadMaxBytes      = 16 * 1024
	assistantSecureCardDefaultLifetime      = 10 * time.Minute
)

var (
	ErrAssistantConversationNotFound        = errors.New("assistant conversation not found")
	ErrAssistantHistoryForbidden            = errors.New("assistant conversation is not visible to this account")
	ErrAssistantConversationAlreadyArchived = errors.New("assistant conversation is already archived")
	ErrAssistantConversationNotArchived     = errors.New("assistant conversation is not archived")
	ErrAssistantConversationRestricted      = errors.New("assistant conversation is restricted")
	ErrAssistantHistoryInvalidCursor        = errors.New("assistant history cursor is invalid")
	ErrAssistantSecureCardNotFound          = errors.New("assistant secure card not found")
	ErrAssistantSecureCardConsumed          = errors.New("assistant secure card has already been revealed")
	ErrAssistantSecureCardExpired           = errors.New("assistant secure card has expired")
	ErrAssistantTokenLimit                  = errors.New("assistant API key limit reached")

	assistantHistoryAPIKeyPattern = regexp.MustCompile(`(?i)\b(?:sk|rk|pk|ak|tok|token|key|secret)[_-][a-z0-9._~+/-]{8,}\b`)
	assistantHistoryJWTPattern    = regexp.MustCompile(`\beyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\b`)
	assistantHistoryEmailPattern  = regexp.MustCompile(`(?i)\b[a-z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+\b`)
	assistantHistoryCookiePattern = regexp.MustCompile(`(?i)\b(cookie|set-cookie|session(?:[_ -]?id)?|csrf(?:[_ -]?token)?)\s*[:=：]\s*[^\s;,]+`)
	assistantHistoryBearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[a-z0-9._~+/-]{6,}=*`)
	assistantHistorySecretPattern = regexp.MustCompile(`(?i)\b(password|passwd|pwd|api[ _-]?key|access[ _-]?token|refresh[ _-]?token|bearer|authorization|密碼|密码|密钥|令牌)\s*[:=：]\s*[^\s,;]+`)
	assistantHistoryURLSecret     = regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access[_-]?token|token|password|passwd|secret)=)[^&#\s]+`)
	assistantHistoryPEMPrivateKey = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	assistantHistoryIPv4Pattern   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	assistantHistoryIPv6Pattern   = regexp.MustCompile(`[0-9A-Fa-f:]{2,39}`)
	assistantHistoryPhonePattern  = regexp.MustCompile(`(^|[^\w])((?:\+?86[\s-]?)?1[3-9]\d{9}|\+\d{1,3}(?:[\s.-]?\d{2,4}){2,4})([^\w]|$)`)
	assistantHistoryCardPattern   = regexp.MustCompile(`(^|[^0-9])([0-9][0-9 -]{11,22}[0-9])([^0-9]|$)`)
)

// AssistantConversation holds only redacted, support-oriented text.  Its
// owner is authoritative; clients never choose it when reading or continuing
// a conversation.
type AssistantConversation struct {
	Id                 int64  `json:"id" gorm:"primaryKey"`
	UserId             int    `json:"-" gorm:"not null;index:idx_assistant_conversation_user_updated,priority:1"`
	Title              string `json:"title" gorm:"type:varchar(255);not null"`
	LastMessagePreview string `json:"last_message_preview" gorm:"type:varchar(512);not null"`
	CreatedAt          int64  `json:"created_at" gorm:"not null;index"`
	UpdatedAt          int64  `json:"updated_at" gorm:"not null;index:idx_assistant_conversation_user_updated,priority:2;index:idx_assistant_conversation_updated"`
	ArchivedAt         int64  `json:"archived_at" gorm:"not null;default:0;index"`
	RestrictedAt       int64  `json:"restricted_at" gorm:"not null;default:0;index"`
	RestrictionReason  string `json:"-" gorm:"type:varchar(64);not null;default:''"`
}

func (AssistantConversation) TableName() string { return "assistant_conversations" }

const (
	AssistantSecurityIncidentStatusOpen = "open"
	AssistantSecurityIncidentCategory   = "high_confidence_abuse"
)

// AssistantSecurityIncident is the administrator-facing report for a
// deterministically terminated assistant conversation. It deliberately keeps
// only a digest; administrators inspect the already-redacted transcript under
// the normal assistant-history role lattice.
type AssistantSecurityIncident struct {
	Id             int    `json:"id" gorm:"primaryKey"`
	UserId         int    `json:"-" gorm:"not null;index"`
	ConversationId int64  `json:"conversation_id" gorm:"not null;uniqueIndex"`
	Category       string `json:"category" gorm:"type:varchar(64);not null;index"`
	Status         string `json:"status" gorm:"type:varchar(24);not null;index"`
	InputDigest    string `json:"-" gorm:"type:char(64);not null"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;index"`
	UpdatedAt      int64  `json:"updated_at" gorm:"not null;index"`
}

func (AssistantSecurityIncident) TableName() string { return "assistant_security_incidents" }

// AssistantHistoryMessage is deliberately limited to text that has passed
// redactAssistantHistoryContent.  Secure values are represented by a card row
// rather than this content field.
type AssistantHistoryMessage struct {
	Id             int64  `json:"id" gorm:"primaryKey"`
	ConversationId int64  `json:"conversation_id" gorm:"not null;index:idx_assistant_history_conversation_id,priority:1"`
	Sequence       int    `json:"sequence" gorm:"not null;index:idx_assistant_history_conversation_id,priority:2"`
	Role           string `json:"role" gorm:"type:varchar(20);not null"`
	Content        string `json:"content" gorm:"type:text;not null"`
	CreatedAt      int64  `json:"created_at" gorm:"not null;index"`
}

func (AssistantHistoryMessage) TableName() string { return "assistant_history_messages" }

// AssistantSecureCard stores a short-lived encrypted value that must never be
// serialized through the conversation API.  The card can be revealed once by
// its owner through an authenticated browser action.
type AssistantSecureCard struct {
	Id             string `json:"-" gorm:"type:varchar(64);primaryKey"`
	OwnerUserId    int    `json:"-" gorm:"not null;index"`
	ConversationId int64  `json:"-" gorm:"not null;default:0;index"`
	MessageId      int64  `json:"-" gorm:"not null;default:0;index"`
	Type           string `json:"-" gorm:"type:varchar(32);not null"`
	Summary        string `json:"-" gorm:"type:varchar(255);not null"`
	Ciphertext     string `json:"-" gorm:"type:text;not null"`
	CreatedAt      int64  `json:"-" gorm:"not null;index"`
	ExpiresAt      int64  `json:"-" gorm:"not null;index"`
	RevealedAt     int64  `json:"-" gorm:"not null;default:0;index"`
}

func (AssistantSecureCard) TableName() string { return "assistant_secure_cards" }

type AssistantSecureCardView struct {
	// ID is intentionally empty for viewers other than the card owner.  An
	// opaque card identifier is still a bearer-like capability because it is
	// accepted by the reveal endpoint.
	ID     string `json:"id,omitempty"`
	Type   string `json:"type,omitempty"`
	Label  string `json:"label,omitempty"`
	Owner  string `json:"owner"`
	Shield bool   `json:"shield"`
}

type AssistantConversationView struct {
	Id                 int64  `json:"id"`
	Title              string `json:"title"`
	LastMessagePreview string `json:"last_message_preview"`
	CreatedAt          int64  `json:"created_at"`
	UpdatedAt          int64  `json:"updated_at"`
	ArchivedAt         int64  `json:"archived_at"`
	RestrictedAt       int64  `json:"restricted_at"`
	Owner              string `json:"owner"`
	PrivacyNotice      string `json:"privacy_notice"`
}

// AssistantConversationHistoryPage is deliberately cursor based. Offset
// pagination becomes increasingly expensive as a user's transcript list grows
// and can repeat/skip rows while new conversations arrive.
type AssistantConversationHistoryPage struct {
	Conversations []AssistantConversationView `json:"conversations"`
	NextCursor    string                      `json:"next_cursor,omitempty"`
}

type assistantConversationCursor struct {
	Version   int   `json:"v"`
	OwnerID   int   `json:"owner_id"`
	Archived  bool  `json:"archived"`
	UpdatedAt int64 `json:"updated_at"`
	ID        int64 `json:"id"`
}

const assistantConversationCursorVersion = 1

func assistantConversationCursorKey() []byte {
	return []byte("assistant-history-cursor-v1:" + common.SessionSecret)
}

func encodeAssistantConversationCursor(ownerID int, archived bool, conversation AssistantConversation) (string, error) {
	payload, err := json.Marshal(assistantConversationCursor{
		Version:   assistantConversationCursorVersion,
		OwnerID:   ownerID,
		Archived:  archived,
		UpdatedAt: conversation.UpdatedAt,
		ID:        conversation.Id,
	})
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, assistantConversationCursorKey())
	_, _ = mac.Write([]byte(encodedPayload))
	encodedSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + encodedSignature, nil
}

func decodeAssistantConversationCursor(value string, ownerID int, archived bool) (assistantConversationCursor, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return assistantConversationCursor{}, ErrAssistantHistoryInvalidCursor
	}
	mac := hmac.New(sha256.New, assistantConversationCursorKey())
	_, _ = mac.Write([]byte(parts[0]))
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return assistantConversationCursor{}, ErrAssistantHistoryInvalidCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return assistantConversationCursor{}, ErrAssistantHistoryInvalidCursor
	}
	var cursor assistantConversationCursor
	if err := json.Unmarshal(payload, &cursor); err != nil ||
		cursor.Version != assistantConversationCursorVersion ||
		cursor.OwnerID != ownerID || cursor.Archived != archived ||
		cursor.UpdatedAt <= 0 || cursor.ID <= 0 {
		return assistantConversationCursor{}, ErrAssistantHistoryInvalidCursor
	}
	return cursor, nil
}

type AssistantHistoryMessageView struct {
	Id            int64                     `json:"id"`
	Role          string                    `json:"role"`
	Content       string                    `json:"content,omitempty"`
	Cards         []AssistantSecureCardView `json:"cards,omitempty"`
	CreatedAt     int64                     `json:"created_at"`
	PrivacyNotice string                    `json:"privacy_notice"`
}

// RedactAssistantHistoryContent is intentionally applied before a message is
// sent to the model and before it reaches persistent storage.  It favours
// false positives over retaining credentials in a support transcript.
func RedactAssistantHistoryContent(value string) string {
	value = strings.TrimSpace(value)
	value = assistantHistoryPEMPrivateKey.ReplaceAllString(value, "[REDACTED_PRIVATE_KEY]")
	value = assistantHistoryURLSecret.ReplaceAllString(value, "$1[REDACTED]")
	value = assistantHistoryCookiePattern.ReplaceAllString(value, "$1: [REDACTED]")
	value = assistantHistoryBearerPattern.ReplaceAllString(value, "Bearer [REDACTED_TOKEN]")
	value = assistantHistorySecretPattern.ReplaceAllString(value, "$1: [REDACTED]")
	value = assistantHistoryAPIKeyPattern.ReplaceAllString(value, "[REDACTED_API_KEY]")
	value = assistantHistoryJWTPattern.ReplaceAllString(value, "[REDACTED_TOKEN]")
	value = assistantHistoryEmailPattern.ReplaceAllString(value, "[REDACTED_EMAIL]")
	value = redactAssistantPhoneNumbers(value)
	value = redactAssistantIPAddresses(value)
	value = redactAssistantCardNumbers(value)
	return value
}

func redactAssistantPhoneNumbers(value string) string {
	return assistantHistoryPhonePattern.ReplaceAllString(value, "$1[REDACTED_PHONE]$3")
}

func redactAssistantIPAddresses(value string) string {
	value = assistantHistoryIPv4Pattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if net.ParseIP(candidate) != nil {
			return "[REDACTED_IP]"
		}
		return candidate
	})
	return assistantHistoryIPv6Pattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if !strings.Contains(candidate, ":") || net.ParseIP(candidate) == nil {
			return candidate
		}
		return "[REDACTED_IP]"
	})
}

func redactAssistantCardNumbers(value string) string {
	return assistantHistoryCardPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		matches := assistantHistoryCardPattern.FindStringSubmatch(candidate)
		if len(matches) != 4 {
			return candidate
		}
		digits := strings.Map(func(r rune) rune {
			if r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, matches[2])
		if len(digits) < 13 || len(digits) > 19 || !assistantLuhnValid(digits) {
			return candidate
		}
		return matches[1] + "[REDACTED_CARD]" + matches[3]
	})
}

func assistantLuhnValid(digits string) bool {
	sum := 0
	double := false
	for index := len(digits) - 1; index >= 0; index-- {
		digit := int(digits[index] - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}

func redactAssistantHistoryBounded(value string) string {
	value = RedactAssistantHistoryContent(value)
	runes := []rune(value)
	if len(runes) > assistantHistoryMessageMaxRunes {
		return string(runes[:assistantHistoryMessageMaxRunes])
	}
	return value
}

func assistantConversationTitle(value string) string {
	value = redactAssistantHistoryBounded(value)
	runes := []rune(value)
	if len(runes) > assistantConversationTitleMaxRunes {
		return string(runes[:assistantConversationTitleMaxRunes])
	}
	return value
}

// UpdateAssistantConversationTitle stores an automatically summarized title
// only on a conversation owned by the requesting user. The same redaction and
// length boundary as fallback titles applies before persistence.
func UpdateAssistantConversationTitle(userID int, conversationID int64, title string) error {
	if userID <= 0 || conversationID <= 0 || strings.TrimSpace(title) == "" {
		return gorm.ErrInvalidData
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		result := tx.Model(&AssistantConversation{}).
			Where("id = ? AND user_id = ?", conversationID, userID).
			Update("title", assistantConversationTitle(title))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrAssistantConversationNotFound
		}
		return nil
	})
}

func assistantConversationRank(user *User) (int, error) {
	if user == nil || user.Id <= 0 {
		return 0, gorm.ErrInvalidData
	}
	if user.Role >= common.RoleRootUser {
		return 10_000, nil
	}
	if user.Role >= common.RoleAdminUser {
		return 1_000 + user.Role, nil
	}
	return 0, nil
}

// AuthorizeAssistantHistoryViewer implements the strict visibility lattice:
// a user always sees their own conversation; other conversations are visible
// only to an administrator with a strictly higher role. Ordinary account trust
// levels never grant access to another user's transcripts.
func AuthorizeAssistantHistoryViewer(viewerUserID, ownerUserID int) error {
	if viewerUserID <= 0 || ownerUserID <= 0 {
		return ErrAssistantHistoryForbidden
	}
	if viewerUserID == ownerUserID {
		return nil
	}
	var users []User
	if err := DB.Where("id IN ?", []int{viewerUserID, ownerUserID}).Find(&users).Error; err != nil {
		return err
	}
	var viewer, owner *User
	for index := range users {
		if users[index].Id == viewerUserID {
			viewer = &users[index]
		}
		if users[index].Id == ownerUserID {
			owner = &users[index]
		}
	}
	if viewer == nil || owner == nil {
		return ErrAssistantConversationNotFound
	}
	viewerRank, err := assistantConversationRank(viewer)
	if err != nil {
		return err
	}
	ownerRank, err := assistantConversationRank(owner)
	if err != nil {
		return err
	}
	if viewerRank <= ownerRank {
		return ErrAssistantHistoryForbidden
	}
	return nil
}

// PrepareAssistantConversation resolves a conversation for a new chat turn.
// Unlike reads, a user may continue only their own conversation.
func PrepareAssistantConversation(userID int, conversationID int64, firstMessage string) (*AssistantConversation, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	if conversationID > 0 {
		var conversation AssistantConversation
		err := DB.Where("id = ? AND user_id = ?", conversationID, userID).First(&conversation).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAssistantConversationNotFound
		}
		if err != nil {
			return nil, err
		}
		if conversation.RestrictedAt > 0 {
			return nil, ErrAssistantConversationRestricted
		}
		return &conversation, nil
	}
	now := common.GetTimestamp()
	conversation := AssistantConversation{
		UserId:             userID,
		Title:              assistantConversationTitle(firstMessage),
		LastMessagePreview: assistantConversationTitle(firstMessage),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		return tx.Create(&conversation).Error
	}); err != nil {
		return nil, err
	}
	return &conversation, nil
}

// RecordAssistantSecurityRefusal persists one redacted refusal turn and
// atomically restricts the conversation. Repeated reports for the same
// conversation are idempotent and never duplicate the security incident.
func RecordAssistantSecurityRefusal(userID int, conversationID int64, userContent, assistantContent, reason string) (int64, bool, error) {
	if userID <= 0 || conversationID < 0 || strings.TrimSpace(userContent) == "" || strings.TrimSpace(assistantContent) == "" {
		return 0, false, gorm.ErrInvalidData
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "security_policy"
	}
	if len(reason) > 64 {
		reason = reason[:64]
	}

	var recordedConversationID int64
	created := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		var conversation AssistantConversation
		if conversationID > 0 {
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", conversationID, userID).First(&conversation).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAssistantConversationNotFound
				}
				return err
			}
			if conversation.RestrictedAt > 0 {
				recordedConversationID = conversation.Id
				return nil
			}
		} else {
			now := common.GetTimestamp()
			conversation = AssistantConversation{
				UserId:             userID,
				Title:              assistantConversationTitle(userContent),
				LastMessagePreview: assistantConversationTitle(assistantContent),
				CreatedAt:          now,
				UpdatedAt:          now,
			}
			if err := tx.Create(&conversation).Error; err != nil {
				return err
			}
		}

		recordedConversationID = conversation.Id
		if _, err := appendAssistantHistoryMessageTx(tx, conversation.Id, AssistantHistoryRoleUser, userContent); err != nil {
			return err
		}
		if _, err := appendAssistantHistoryMessageTx(tx, conversation.Id, AssistantHistoryRoleAssistant, assistantContent); err != nil {
			return err
		}
		now := common.GetTimestamp()
		if err := tx.Model(&conversation).Updates(map[string]any{
			"last_message_preview": assistantConversationTitle(assistantContent),
			"updated_at":           now,
			"restricted_at":        now,
			"restriction_reason":   reason,
		}).Error; err != nil {
			return err
		}
		redactedInput := redactAssistantHistoryBounded(userContent)
		inputDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(redactedInput)))
		incident := AssistantSecurityIncident{
			UserId:         userID,
			ConversationId: conversation.Id,
			Category:       AssistantSecurityIncidentCategory,
			Status:         AssistantSecurityIncidentStatusOpen,
			InputDigest:    inputDigest,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := tx.Create(&incident).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	return recordedConversationID, created, err
}

// FindRecentAssistantConversationForRetry finds a completed first-turn
// conversation that a browser retry can safely resume.  The caller must only
// use this for an explicit retry attempt; ordinary identical questions remain
// independent conversations.  Matching is scoped to the owner, a short time
// window, the redacted user message, and an existing assistant message so a
// half-created conversation is never reused.
func FindRecentAssistantConversationForRetry(userID int, firstMessage string, since time.Time) (*AssistantConversation, error) {
	if userID <= 0 {
		return nil, gorm.ErrInvalidData
	}
	firstMessage = redactAssistantHistoryBounded(firstMessage)
	if strings.TrimSpace(firstMessage) == "" {
		return nil, gorm.ErrInvalidData
	}
	if since.IsZero() {
		since = time.Now().Add(-5 * time.Minute)
	}

	var conversation AssistantConversation
	err := DB.Model(&AssistantConversation{}).
		Joins("JOIN assistant_history_messages AS history ON history.conversation_id = assistant_conversations.id").
		Where("assistant_conversations.user_id = ?", userID).
		Where("assistant_conversations.archived_at = 0").
		Where("assistant_conversations.updated_at >= ?", since.Unix()).
		Where("history.role = ? AND history.content = ?", AssistantHistoryRoleUser, firstMessage).
		Joins("JOIN assistant_history_messages AS assistant_history ON assistant_history.conversation_id = assistant_conversations.id").
		Where("assistant_history.role = ?", AssistantHistoryRoleAssistant).
		Order("assistant_conversations.updated_at DESC, assistant_conversations.id DESC").
		First(&conversation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &conversation, nil
}

func LoadAssistantConversationMessages(userID int, conversationID int64, limit int) ([]AssistantHistoryMessage, error) {
	conversation, err := PrepareAssistantConversation(userID, conversationID, "")
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > assistantHistoryPageMax {
		limit = assistantHistoryPageMax
	}
	pairLimit := limit / 2
	if pairLimit == 0 {
		return []AssistantHistoryMessage{}, nil
	}
	// Fetch newest-first so a bounded query keeps the latest context.  Read a
	// wider window to tolerate legacy/incomplete rows without ever returning a
	// split user/assistant turn to the model.
	scanLimit := limit * 4
	if scanLimit < 20 {
		scanLimit = 20
	}
	if scanLimit > assistantHistoryPageMax {
		scanLimit = assistantHistoryPageMax
	}
	var candidates []AssistantHistoryMessage
	if err := DB.Where("conversation_id = ?", conversation.Id).
		Where("role IN ?", []string{AssistantHistoryRoleUser, AssistantHistoryRoleAssistant}).
		Order("sequence DESC").Limit(scanLimit).Find(&candidates).Error; err != nil {
		return nil, err
	}
	for left, right := 0, len(candidates)-1; left < right; left, right = left+1, right-1 {
		candidates[left], candidates[right] = candidates[right], candidates[left]
	}
	pairStarts := make([]int, 0, pairLimit)
	for index := len(candidates) - 1; index > 0 && len(pairStarts) < pairLimit; {
		userMessage := candidates[index-1]
		assistantMessage := candidates[index]
		if userMessage.Role == AssistantHistoryRoleUser &&
			assistantMessage.Role == AssistantHistoryRoleAssistant &&
			assistantMessage.Sequence == userMessage.Sequence+1 {
			pairStarts = append(pairStarts, index-1)
			index -= 2
			continue
		}
		index--
	}
	messages := make([]AssistantHistoryMessage, 0, len(pairStarts)*2)
	for index := len(pairStarts) - 1; index >= 0; index-- {
		start := pairStarts[index]
		messages = append(messages, candidates[start], candidates[start+1])
	}
	return messages, nil
}

func appendAssistantHistoryMessageTx(tx *gorm.DB, conversationID int64, role, content string) (*AssistantHistoryMessage, error) {
	if role != AssistantHistoryRoleUser && role != AssistantHistoryRoleAssistant && role != AssistantHistoryRoleCard {
		return nil, gorm.ErrInvalidData
	}
	content = redactAssistantHistoryBounded(content)
	if role != AssistantHistoryRoleCard && strings.TrimSpace(content) == "" {
		return nil, gorm.ErrInvalidData
	}
	var last AssistantHistoryMessage
	if err := lockForUpdate(tx).Where("conversation_id = ?", conversationID).Order("sequence DESC").First(&last).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	message := &AssistantHistoryMessage{
		ConversationId: conversationID,
		Sequence:       last.Sequence + 1,
		Role:           role,
		Content:        content,
		CreatedAt:      common.GetTimestamp(),
	}
	if err := tx.Create(message).Error; err != nil {
		return nil, err
	}
	if err := trimAssistantHistoryTx(tx, conversationID); err != nil {
		return nil, err
	}
	return message, nil
}

// assistantHistoryContentBytesExpression keeps the SQL-side storage budget in
// the same units as Go's len(string): UTF-8 bytes, not Unicode code points.
// SQLite's LENGTH(text) counts characters, while PostgreSQL/MySQL expose an
// explicit byte-length function.  The expression is static and selected from
// the configured dialect; it is never built from user input.
func assistantHistoryContentBytesExpression() string {
	switch common.MainDatabaseType() {
	case common.DatabaseTypePostgreSQL, common.DatabaseTypeMySQL:
		return "OCTET_LENGTH(content)"
	case common.DatabaseTypeSQLite:
		return "LENGTH(CAST(content AS BLOB))"
	case common.DatabaseTypeClickHouse:
		return "length(content)"
	default:
		return "LENGTH(content)"
	}
}

// trimAssistantHistoryTx keeps active conversations within a small, explicit
// storage budget.  It runs inside the owner's transaction, so a concurrent
// turn cannot observe or create an inconsistent sequence.  Only the oldest
// rows are removed; the newest turn remains available for the model and UI.
// Secure cards linked to removed card rows are deleted first so encrypted
// payloads cannot outlive the transcript row that referenced them.
func trimAssistantHistoryTx(tx *gorm.DB, conversationID int64) error {
	if conversationID <= 0 {
		return gorm.ErrInvalidData
	}
	var count int64
	if err := tx.Model(&AssistantHistoryMessage{}).
		Where("conversation_id = ?", conversationID).
		Count(&count).Error; err != nil {
		return err
	}
	var totals struct {
		Bytes int64 `gorm:"column:bytes"`
	}
	if err := tx.Model(&AssistantHistoryMessage{}).
		Select("COALESCE(SUM("+assistantHistoryContentBytesExpression()+"), 0) AS bytes").
		Where("conversation_id = ?", conversationID).
		Scan(&totals).Error; err != nil {
		return err
	}
	if count <= assistantHistoryConversationMaxMessages && totals.Bytes <= assistantHistoryConversationMaxBytes {
		return nil
	}

	var candidates []AssistantHistoryMessage
	if err := tx.Select("id", "content").
		Where("conversation_id = ?", conversationID).
		Order("sequence ASC, id ASC").
		Limit(assistantHistoryTrimBatch).
		Find(&candidates).Error; err != nil {
		return err
	}
	if len(candidates) == 0 {
		return nil
	}
	deleteIDs := make([]int64, 0, len(candidates))
	remainingCount := count
	remainingBytes := totals.Bytes
	for _, candidate := range candidates {
		if remainingCount <= assistantHistoryConversationMaxMessages && remainingBytes <= assistantHistoryConversationMaxBytes {
			break
		}
		deleteIDs = append(deleteIDs, candidate.Id)
		remainingCount--
		remainingBytes -= int64(len(candidate.Content))
	}
	if len(deleteIDs) == 0 {
		return nil
	}
	if err := tx.Where("message_id IN ?", deleteIDs).Delete(&AssistantSecureCard{}).Error; err != nil {
		return err
	}
	return tx.Where("id IN ?", deleteIDs).Delete(&AssistantHistoryMessage{}).Error
}

// RecordAssistantConversationTurnForRequest records a complete successful turn.
// A zero conversation ID creates the conversation in the same transaction, so
// failed or empty upstream responses cannot leave empty conversation shells.
func RecordAssistantConversationTurnForRequest(userID int, conversationID int64, userContent, assistantContent string) (int64, error) {
	if userID <= 0 || conversationID < 0 {
		return 0, gorm.ErrInvalidData
	}
	var recordedConversationID int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		var conversation AssistantConversation
		if conversationID > 0 {
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", conversationID, userID).First(&conversation).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAssistantConversationNotFound
				}
				return err
			}
			if conversation.RestrictedAt > 0 {
				return ErrAssistantConversationRestricted
			}
		} else {
			now := common.GetTimestamp()
			conversation = AssistantConversation{
				UserId:             userID,
				Title:              assistantConversationTitle(userContent),
				LastMessagePreview: assistantConversationTitle(userContent),
				CreatedAt:          now,
				UpdatedAt:          now,
			}
			if err := tx.Create(&conversation).Error; err != nil {
				return err
			}
		}
		recordedConversationID = conversation.Id
		if _, err := appendAssistantHistoryMessageTx(tx, conversation.Id, AssistantHistoryRoleUser, userContent); err != nil {
			return err
		}
		if _, err := appendAssistantHistoryMessageTx(tx, conversation.Id, AssistantHistoryRoleAssistant, assistantContent); err != nil {
			return err
		}
		now := common.GetTimestamp()
		return tx.Model(&conversation).Updates(map[string]any{
			"last_message_preview": assistantConversationTitle(assistantContent),
			"updated_at":           now,
		}).Error
	})
	if err != nil {
		return 0, err
	}
	return recordedConversationID, nil
}

// RecordAssistantConversationTurn preserves the existing explicit-conversation
// API for callers that already created a conversation.
func RecordAssistantConversationTurn(userID int, conversationID int64, userContent, assistantContent string) error {
	if conversationID <= 0 {
		return gorm.ErrInvalidData
	}
	_, err := RecordAssistantConversationTurnForRequest(userID, conversationID, userContent, assistantContent)
	return err
}

// RecordAssistantConversationTurnForRetry keeps a browser replay idempotent.
// Ordinary repeated questions still use RecordAssistantConversationTurn and
// remain visible as distinct turns.
func RecordAssistantConversationTurnForRetry(userID int, conversationID int64, userContent, assistantContent string) error {
	if userID <= 0 || conversationID <= 0 {
		return gorm.ErrInvalidData
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		var conversation AssistantConversation
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", conversationID, userID).First(&conversation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssistantConversationNotFound
			}
			return err
		}
		if conversation.RestrictedAt > 0 {
			return ErrAssistantConversationRestricted
		}
		var recent []AssistantHistoryMessage
		if err := tx.Where("conversation_id = ?", conversation.Id).
			Where("role IN ?", []string{AssistantHistoryRoleUser, AssistantHistoryRoleAssistant}).
			Order("sequence DESC").Limit(2).Find(&recent).Error; err != nil {
			return err
		}
		if len(recent) == 2 &&
			recent[0].Role == AssistantHistoryRoleAssistant &&
			recent[1].Role == AssistantHistoryRoleUser &&
			recent[1].Content == redactAssistantHistoryBounded(userContent) {
			return nil
		}
		if _, err := appendAssistantHistoryMessageTx(tx, conversation.Id, AssistantHistoryRoleUser, userContent); err != nil {
			return err
		}
		if _, err := appendAssistantHistoryMessageTx(tx, conversation.Id, AssistantHistoryRoleAssistant, assistantContent); err != nil {
			return err
		}
		now := common.GetTimestamp()
		return tx.Model(&conversation).Updates(map[string]any{
			"last_message_preview": assistantConversationTitle(assistantContent),
			"updated_at":           now,
		}).Error
	})
}

func setAssistantConversationArchived(userID int, conversationID int64, archived bool) (*AssistantConversation, error) {
	if userID <= 0 || conversationID <= 0 {
		return nil, ErrAssistantConversationNotFound
	}

	var updated AssistantConversation
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, userID); err != nil {
			return err
		}
		var conversation AssistantConversation
		if err := lockForUpdate(tx).
			Where("id = ? AND user_id = ?", conversationID, userID).
			First(&conversation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssistantConversationNotFound
			}
			return err
		}
		if archived {
			if conversation.ArchivedAt != 0 {
				return ErrAssistantConversationAlreadyArchived
			}
			conversation.ArchivedAt = common.GetTimestamp()
		} else {
			if conversation.ArchivedAt == 0 {
				return ErrAssistantConversationNotArchived
			}
			conversation.ArchivedAt = 0
		}
		if err := tx.Model(&conversation).Update("archived_at", conversation.ArchivedAt).Error; err != nil {
			return err
		}
		updated = conversation
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// ArchiveAssistantConversation changes only the owner's soft archive state.
// Visibility grants never confer mutation rights.
func ArchiveAssistantConversation(userID int, conversationID int64) (*AssistantConversation, error) {
	return setAssistantConversationArchived(userID, conversationID, true)
}

// UnarchiveAssistantConversation restores only the owner's soft archive state.
func UnarchiveAssistantConversation(userID int, conversationID int64) (*AssistantConversation, error) {
	return setAssistantConversationArchived(userID, conversationID, false)
}

func ListAssistantConversationsPage(viewerUserID, ownerUserID int, limit int, archived bool, rawCursor string) (AssistantConversationHistoryPage, error) {
	if err := AuthorizeAssistantHistoryViewer(viewerUserID, ownerUserID); err != nil {
		return AssistantConversationHistoryPage{}, err
	}
	if limit <= 0 || limit > assistantHistoryPageMax {
		limit = 30
	}
	var cursor assistantConversationCursor
	if strings.TrimSpace(rawCursor) != "" {
		var err error
		cursor, err = decodeAssistantConversationCursor(strings.TrimSpace(rawCursor), ownerUserID, archived)
		if err != nil {
			return AssistantConversationHistoryPage{}, err
		}
	}
	var conversations []AssistantConversation
	archiveFilter := "archived_at = 0"
	if archived {
		archiveFilter = "archived_at <> 0"
	}
	query := DB.Where("user_id = ?", ownerUserID).
		Where(archiveFilter).
		Where("EXISTS (SELECT 1 FROM assistant_history_messages WHERE assistant_history_messages.conversation_id = assistant_conversations.id)")
	if cursor.ID > 0 {
		query = query.Where("(updated_at < ?) OR (updated_at = ? AND id < ?)", cursor.UpdatedAt, cursor.UpdatedAt, cursor.ID)
	}
	if err := query.Order("updated_at DESC, id DESC").Limit(limit + 1).Find(&conversations).Error; err != nil {
		return AssistantConversationHistoryPage{}, err
	}
	hasMore := len(conversations) > limit
	if hasMore {
		conversations = conversations[:limit]
	}
	owner := "lower_level_user"
	if viewerUserID == ownerUserID {
		owner = "self"
	}
	views := make([]AssistantConversationView, 0, len(conversations))
	for _, conversation := range conversations {
		views = append(views, AssistantConversationView{
			Id:                 conversation.Id,
			Title:              conversation.Title,
			LastMessagePreview: conversation.LastMessagePreview,
			CreatedAt:          conversation.CreatedAt,
			UpdatedAt:          conversation.UpdatedAt,
			ArchivedAt:         conversation.ArchivedAt,
			RestrictedAt:       conversation.RestrictedAt,
			Owner:              owner,
			PrivacyNotice:      AssistantHistoryPrivacyNotice,
		})
	}
	page := AssistantConversationHistoryPage{Conversations: views}
	if hasMore && len(conversations) > 0 {
		nextCursor, err := encodeAssistantConversationCursor(ownerUserID, archived, conversations[len(conversations)-1])
		if err != nil {
			return AssistantConversationHistoryPage{}, err
		}
		page.NextCursor = nextCursor
	}
	return page, nil
}

// ListAssistantConversations keeps the original model API for internal
// callers while the HTTP endpoint uses the cursor-aware page variant.
func ListAssistantConversations(viewerUserID, ownerUserID int, limit int, archived bool) ([]AssistantConversationView, error) {
	page, err := ListAssistantConversationsPage(viewerUserID, ownerUserID, limit, archived, "")
	if err != nil {
		return nil, err
	}
	return page.Conversations, nil
}

// PopulateAssistantConversationCounts adds visible transcript counts to user
// management rows. Ordinary users may only receive their own count; an
// administrator receives counts only for accounts with a strictly lower role.
// Empty conversation shells are excluded to match the history list.
func PopulateAssistantConversationCounts(users []*User, viewerUserID, viewerRole int) error {
	authorizedUserIDs := make([]int, 0, len(users))
	usersByID := make(map[int]*User, len(users))
	for _, user := range users {
		if user == nil {
			continue
		}
		canView := user.Id == viewerUserID ||
			(viewerRole >= common.RoleAdminUser && viewerRole > user.Role)
		if !canView {
			user.AssistantConversationCount = nil
			continue
		}
		count := int64(0)
		user.AssistantConversationCount = &count
		authorizedUserIDs = append(authorizedUserIDs, user.Id)
		usersByID[user.Id] = user
	}
	if len(authorizedUserIDs) == 0 {
		return nil
	}

	type conversationCount struct {
		UserID int   `gorm:"column:user_id"`
		Count  int64 `gorm:"column:count"`
	}
	var counts []conversationCount
	if err := DB.Table("assistant_conversations").
		Select("assistant_conversations.user_id, COUNT(DISTINCT assistant_conversations.id) AS count").
		Joins("JOIN assistant_history_messages ON assistant_history_messages.conversation_id = assistant_conversations.id").
		Where("assistant_conversations.user_id IN ?", authorizedUserIDs).
		Group("assistant_conversations.user_id").
		Scan(&counts).Error; err != nil {
		return err
	}
	for _, row := range counts {
		if user := usersByID[row.UserID]; user != nil {
			count := row.Count
			user.AssistantConversationCount = &count
		}
	}
	return nil
}

func GetAssistantConversationHistory(viewerUserID int, conversationID int64, limit int) (*AssistantConversationView, []AssistantHistoryMessageView, error) {
	if conversationID <= 0 {
		return nil, nil, ErrAssistantConversationNotFound
	}
	var conversation AssistantConversation
	if err := DB.First(&conversation, conversationID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrAssistantConversationNotFound
		}
		return nil, nil, err
	}
	if err := AuthorizeAssistantHistoryViewer(viewerUserID, conversation.UserId); err != nil {
		return nil, nil, err
	}
	if limit <= 0 || limit > assistantHistoryPageMax {
		limit = assistantHistoryPageMax
	}
	var messages []AssistantHistoryMessage
	if err := DB.Where("conversation_id = ?", conversation.Id).Order("sequence DESC").Limit(limit).Find(&messages).Error; err != nil {
		return nil, nil, err
	}
	for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
		messages[left], messages[right] = messages[right], messages[left]
	}
	var cards []AssistantSecureCard
	// Cards are metadata-only in this response, so keep their query bounded by
	// a fixed page size and never hydrate ciphertext that this endpoint cannot
	// use. The newest cards win when a legacy or future conversation contains
	// more cards than the page can represent; the reveal endpoint remains the
	// only path that reads the encrypted payload.
	if err := DB.Select("id", "owner_user_id", "conversation_id", "message_id", "type", "summary", "created_at", "expires_at", "revealed_at").
		Where("conversation_id = ?", conversation.Id).
		Order("created_at DESC, id DESC").
		Limit(assistantHistorySecureCardMax).
		Find(&cards).Error; err != nil {
		return nil, nil, err
	}
	cardsByMessageID := make(map[int64][]AssistantSecureCardView)
	standaloneCards := make([]AssistantSecureCardView, 0)
	for _, card := range cards {
		// Cards are intentionally shown as metadata to every authorized history
		// viewer.  Only the owner can call RevealAssistantSecureCard.
		cardView := assistantSecureCardView(card, viewerUserID == card.OwnerUserId)
		if card.MessageId > 0 {
			cardsByMessageID[card.MessageId] = append(cardsByMessageID[card.MessageId], cardView)
		} else {
			standaloneCards = append(standaloneCards, cardView)
		}
	}
	owner := "lower_level_user"
	if viewerUserID == conversation.UserId {
		owner = "self"
	}
	view := &AssistantConversationView{
		Id:                 conversation.Id,
		Title:              conversation.Title,
		LastMessagePreview: conversation.LastMessagePreview,
		CreatedAt:          conversation.CreatedAt,
		UpdatedAt:          conversation.UpdatedAt,
		ArchivedAt:         conversation.ArchivedAt,
		RestrictedAt:       conversation.RestrictedAt,
		Owner:              owner,
		PrivacyNotice:      AssistantHistoryPrivacyNotice,
	}
	messageViews := make([]AssistantHistoryMessageView, 0, len(messages)+len(standaloneCards))
	for _, message := range messages {
		messageViews = append(messageViews, AssistantHistoryMessageView{
			Id:            message.Id,
			Role:          message.Role,
			Content:       message.Content,
			Cards:         cardsByMessageID[message.Id],
			CreatedAt:     message.CreatedAt,
			PrivacyNotice: AssistantHistoryPrivacyNotice,
		})
	}
	for _, card := range standaloneCards {
		messageViews = append(messageViews, AssistantHistoryMessageView{
			Role:          AssistantHistoryRoleCard,
			Cards:         []AssistantSecureCardView{card},
			PrivacyNotice: AssistantHistoryPrivacyNotice,
		})
	}
	return view, messageViews, nil
}

func secureCardEncryptionKey() [32]byte {
	return sha256.Sum256([]byte("assistant-secure-card-v1:" + common.SessionSecret))
}

func encryptAssistantSecureCardPayload(payload string) (string, error) {
	if len(payload) == 0 || len(payload) > assistantSecureCardPayloadMaxBytes {
		return "", gorm.ErrInvalidData
	}
	key := secureCardEncryptionKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(payload), nil)
	return base64.RawURLEncoding.EncodeToString(append(nonce, ciphertext...)), nil
}

func decryptAssistantSecureCardPayload(ciphertext string) (string, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	key := secureCardEncryptionKey()
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(encoded) < gcm.NonceSize() {
		return "", errors.New("secure card ciphertext is invalid")
	}
	plaintext, err := gcm.Open(nil, encoded[:gcm.NonceSize()], encoded[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func newAssistantSecureCardID() (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func CreateAssistantSecureCard(ownerUserID int, conversationID int64, cardType, summary, payload string) (*AssistantSecureCard, error) {
	if ownerUserID <= 0 || strings.TrimSpace(cardType) == "" || strings.TrimSpace(summary) == "" {
		return nil, gorm.ErrInvalidData
	}
	ciphertext, err := encryptAssistantSecureCardPayload(payload)
	if err != nil {
		return nil, err
	}
	id, err := newAssistantSecureCardID()
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	card := &AssistantSecureCard{
		Id:             id,
		OwnerUserId:    ownerUserID,
		ConversationId: conversationID,
		Type:           cardType,
		Summary:        assistantConversationTitle(summary),
		Ciphertext:     ciphertext,
		CreatedAt:      now,
		ExpiresAt:      time.Now().Add(assistantSecureCardDefaultLifetime).Unix(),
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, ownerUserID); err != nil {
			return err
		}
		if conversationID > 0 {
			var conversation AssistantConversation
			if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", conversationID, ownerUserID).First(&conversation).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return ErrAssistantConversationNotFound
				}
				return err
			}
			message, err := appendAssistantHistoryMessageTx(tx, conversationID, AssistantHistoryRoleCard, "")
			if err != nil {
				return err
			}
			card.MessageId = message.Id
		}
		return tx.Create(card).Error
	}); err != nil {
		return nil, err
	}
	return card, nil
}

// insertAssistantTokenAndCreateSecureCardTx writes a key and its opaque card
// under the caller's transaction and owner lock.
func insertAssistantTokenAndCreateSecureCardTx(tx *gorm.DB, token *Token, ownerUserID int, conversationID int64, summary, payload string, maxUserTokens int) (*AssistantSecureCard, error) {
	if token == nil || token.UserId <= 0 || token.UserId != ownerUserID {
		return nil, gorm.ErrInvalidData
	}
	ciphertext, err := encryptAssistantSecureCardPayload(payload)
	if err != nil {
		return nil, err
	}
	id, err := newAssistantSecureCardID()
	if err != nil {
		return nil, err
	}
	now := common.GetTimestamp()
	card := &AssistantSecureCard{
		Id:             id,
		OwnerUserId:    ownerUserID,
		ConversationId: conversationID,
		Type:           AssistantSecureCardTypeAPIKey,
		Summary:        assistantConversationTitle(summary),
		Ciphertext:     ciphertext,
		CreatedAt:      now,
		ExpiresAt:      time.Now().Add(assistantSecureCardDefaultLifetime).Unix(),
	}
	if err := lockAssistantOwner(tx, ownerUserID); err != nil {
		return nil, err
	}
	if maxUserTokens > 0 {
		var count int64
		if err := tx.Model(&Token{}).Where("user_id = ?", ownerUserID).Count(&count).Error; err != nil {
			return nil, err
		}
		if count >= int64(maxUserTokens) {
			return nil, ErrAssistantTokenLimit
		}
	}
	if conversationID > 0 {
		var conversation AssistantConversation
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ?", conversationID, ownerUserID).First(&conversation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrAssistantConversationNotFound
			}
			return nil, err
		}
		message, err := appendAssistantHistoryMessageTx(tx, conversationID, AssistantHistoryRoleCard, "")
		if err != nil {
			return nil, err
		}
		card.MessageId = message.Id
	}
	if err := tx.Create(token).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&User{}).
		Where("id = ? AND console_activated_at = ?", token.UserId, 0).
		Update("console_activated_at", time.Now().Unix()).Error; err != nil {
		return nil, err
	}
	if err := tx.Create(card).Error; err != nil {
		return nil, err
	}
	return card, nil
}

func finalizeAssistantTokenCreation(token *Token) {
	if token == nil {
		return
	}
	if err := invalidateUserCache(token.UserId); err != nil {
		common.SysLog("failed to invalidate user cache after assistant key creation: " + err.Error())
	}
}

// InsertAssistantTokenAndCreateSecureCard keeps credential creation atomic:
// either the user receives an opaque, owner-bound card or the token itself is
// not created. This is also used by the direct key form, whose confirmation is
// owned by that form rather than an assistant auth flow.
func InsertAssistantTokenAndCreateSecureCard(token *Token, ownerUserID int, conversationID int64, summary, payload string) (*AssistantSecureCard, error) {
	var card *AssistantSecureCard
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		card, err = insertAssistantTokenAndCreateSecureCardTx(tx, token, ownerUserID, conversationID, summary, payload, 0)
		return err
	})
	if err != nil {
		return nil, err
	}
	finalizeAssistantTokenCreation(token)
	return card, nil
}

// AssistantKeyMaterial is produced only after the locked auth-flow payload and
// current account configuration have been validated inside the transaction.
type AssistantKeyMaterial struct {
	Token          *Token
	ConversationID int64
	Summary        string
	SecurePayload  string
}

// AssistantKeyMaterialFactory validates the locked draft and creates the
// credential material at the final mutation boundary. Returning an error rolls
// back both flow consumption and every write performed by the factory.
type AssistantKeyMaterialFactory func(tx *gorm.DB, flow *AuthFlow) (*AssistantKeyMaterial, error)

// ConsumeAssistantKeyFlowAndCreateSecureCard is the sole assistant key
// mutation entry point. Its PostgreSQL linearization point is the ordered
// flow -> user -> session -> factor -> paid facts lock sequence. A revocation
// that acquires the authority rows first is observed and rejected; a key
// transaction that already owns them may legally commit before the revocation.
func ConsumeAssistantKeyFlowAndCreateSecureCard(
	flowToken string,
	fence AssistantKeyAuthorizationFence,
	twoFactorCode string,
	maxUserTokens int,
	factory AssistantKeyMaterialFactory,
) (*Token, *AssistantSecureCard, error) {
	match := fence.authFlowMatch()
	if flowToken == "" || match.Purpose != AuthFlowPurposeAssistantKey || factory == nil {
		return nil, nil, ErrAuthFlowInvalid
	}
	var (
		material  *AssistantKeyMaterial
		card      *AssistantSecureCard
		rejection error
	)
	err := DB.Transaction(func(tx *gorm.DB) error {
		var flow AuthFlow
		if err := applyAuthFlowMatch(lockForUpdate(tx), flowToken, match).First(&flow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAuthFlowInvalid
			}
			return err
		}
		if flow.ConsumedAt != nil {
			return ErrAuthFlowConsumed
		}
		now := time.Now()
		if !flow.ExpiresAt.After(now) {
			return ErrAuthFlowExpired
		}

		authorized, err := authorizeAssistantKeyCreationTx(tx, fence, twoFactorCode)
		if err != nil {
			return err
		}
		if !authorized {
			// Failed-attempt state is authoritative security data. Commit that
			// update without consuming the confirmation flow or creating secrets.
			rejection = ErrAssistantKeyTwoFactorInvalid
			return nil
		}

		result := tx.Model(&AuthFlow{}).
			Where("id = ? AND consumed_at IS NULL AND expires_at > ?", flow.Id, now).
			Update("consumed_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAuthFlowConsumed
		}
		flow.ConsumedAt = &now

		material, err = factory(tx, &flow)
		if err != nil {
			return err
		}
		if material == nil || material.Token == nil || material.Token.UserId != fence.userID {
			return ErrAuthFlowInvalid
		}
		card, err = insertAssistantTokenAndCreateSecureCardTx(
			tx,
			material.Token,
			fence.userID,
			material.ConversationID,
			material.Summary,
			material.SecurePayload,
			maxUserTokens,
		)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	if rejection != nil {
		return nil, nil, rejection
	}
	finalizeAssistantTokenCreation(material.Token)
	return material.Token, card, nil
}

func assistantSecureCardView(card AssistantSecureCard, isOwner bool) AssistantSecureCardView {
	if !isOwner {
		return AssistantSecureCardView{
			Type:   "protected",
			Label:  "个人凭证已保护；仅所有者可查看",
			Owner:  "protected",
			Shield: true,
		}
	}
	return AssistantSecureCardView{
		ID:     card.Id,
		Type:   card.Type,
		Label:  card.Summary,
		Owner:  "self",
		Shield: true,
	}
}

// AssistantSecureCardViewForOwner returns only the opaque card metadata.  It
// deliberately has no path to serialize Ciphertext or the protected value.
func AssistantSecureCardViewForOwner(card *AssistantSecureCard) AssistantSecureCardView {
	if card == nil {
		return AssistantSecureCardView{}
	}
	return assistantSecureCardView(*card, true)
}

func RevealAssistantSecureCard(ownerUserID int, cardID string) (string, AssistantSecureCardView, error) {
	if ownerUserID <= 0 || strings.TrimSpace(cardID) == "" {
		return "", AssistantSecureCardView{}, gorm.ErrInvalidData
	}
	var payload string
	var view AssistantSecureCardView
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := lockAssistantOwner(tx, ownerUserID); err != nil {
			return err
		}
		var card AssistantSecureCard
		if err := lockForUpdate(tx).Where("id = ? AND owner_user_id = ?", cardID, ownerUserID).First(&card).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssistantSecureCardNotFound
			}
			return err
		}
		if card.RevealedAt > 0 {
			return ErrAssistantSecureCardConsumed
		}
		if card.ExpiresAt <= time.Now().Unix() {
			return ErrAssistantSecureCardExpired
		}
		plaintext, err := decryptAssistantSecureCardPayload(card.Ciphertext)
		if err != nil {
			return fmt.Errorf("decrypt assistant secure card: %w", err)
		}
		now := common.GetTimestamp()
		if result := tx.Model(&card).Where("revealed_at = ?", 0).Updates(map[string]any{
			"revealed_at": now,
			"ciphertext":  "",
		}); result.Error != nil {
			return result.Error
		} else if result.RowsAffected != 1 {
			return ErrAssistantSecureCardConsumed
		}
		payload = plaintext
		view = assistantSecureCardView(card, true)
		return nil
	})
	if err != nil {
		return "", AssistantSecureCardView{}, err
	}
	return payload, view, nil
}

func AssistantSecureCardPayload(value string) (map[string]string, error) {
	var payload map[string]string
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}
