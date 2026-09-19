package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"gorm.io/gorm"
)

// Receipts survive transcript trimming. They contain no additional message
// content and prevent a delayed retry from re-creating a trimmed turn.
type AssistantTurnReceipt struct {
	TurnKey          string `gorm:"primaryKey;type:varchar(128)"`
	ConversationID   int64  `gorm:"not null;index"`
	SupportRequestID int    `gorm:"not null;default:0"`
	MessageID        int64  `gorm:"not null"`
	InputDigest      string `gorm:"type:char(64);not null"`
}

var ErrAssistantTurnConflict = errors.New("assistant turn identifier was already used for another request")
var ErrAssistantTurnExpired = errors.New("assistant turn is no longer available; start a new message")

func assistantTurnKey(userID int, turnID string) string { return fmt.Sprintf("%d:%s", userID, turnID) }
func assistantTurnDigest(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(redactAssistantHistoryBounded(content))))
}

func lookupAssistantTurnTx(tx *gorm.DB, userID int, turnID string, conversationID int64, content string) (*AssistantHistoryMessage, error) {
	var receipt AssistantTurnReceipt
	err := tx.Where("turn_key = ?", assistantTurnKey(userID, turnID)).First(&receipt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if receipt.InputDigest != assistantTurnDigest(content) || (conversationID > 0 && receipt.ConversationID != conversationID) {
		return nil, ErrAssistantTurnConflict
	}
	var answer AssistantHistoryMessage
	err = tx.Where("id = ? AND conversation_id = ?", receipt.MessageID, receipt.ConversationID).First(&answer).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAssistantTurnExpired
	}
	return &answer, err
}

func LookupAssistantTurn(userID int, turnID string, conversationID int64, content string) (*AssistantHistoryMessage, error) {
	if userID <= 0 || turnID == "" {
		return nil, gorm.ErrInvalidData
	}
	return lookupAssistantTurnTx(DB, userID, turnID, conversationID, content)
}

// LookupAssistantSupportTurn preserves a handoff receipt even if staff close
// the request before a browser retries its lost response.
func LookupAssistantSupportTurn(actorID int, turnID string, conversationID int64, content string) (*AssistantSupportRequest, error) {
	var receipt AssistantTurnReceipt
	err := DB.Where("turn_key = ?", assistantTurnKey(actorID, turnID)).First(&receipt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if receipt.SupportRequestID == 0 {
		return nil, nil
	}
	if _, err := lookupAssistantTurnTx(DB, actorID, turnID, conversationID, content); err != nil {
		return nil, err
	}
	return GetAssistantSupportRequest(actorID, receipt.SupportRequestID)
}
