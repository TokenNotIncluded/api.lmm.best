package model

import (
	"errors"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var (
	ErrAssistantKeyAuthorizationChanged = errors.New("assistant key authorization changed")
	ErrAssistantKeyTwoFactorInvalid     = errors.New("assistant key two-factor verification failed")
)

// AssistantKeyAuthorizationFence is a server-captured snapshot of the browser
// identity that prepared the final mutation. Its fields are private so request
// JSON cannot supply or override an authorization fact.
type AssistantKeyAuthorizationFence struct {
	userID                  int
	sessionID               string
	expectedSessionVersion  int64
	expectedUserAuthVersion int64
	developerAccessPolicy   DeveloperAccessPolicy
}

func NewAssistantKeyAuthorizationFence(
	userID int,
	sessionID string,
	expectedSessionVersion int64,
	expectedUserAuthVersion int64,
	policy DeveloperAccessPolicy,
) (AssistantKeyAuthorizationFence, error) {
	sessionID = strings.TrimSpace(sessionID)
	if userID <= 0 || sessionID == "" || expectedSessionVersion <= 0 || expectedUserAuthVersion <= 0 {
		return AssistantKeyAuthorizationFence{}, ErrAssistantKeyAuthorizationChanged
	}
	return AssistantKeyAuthorizationFence{
		userID:                  userID,
		sessionID:               sessionID,
		expectedSessionVersion:  expectedSessionVersion,
		expectedUserAuthVersion: expectedUserAuthVersion,
		developerAccessPolicy:   policy,
	}, nil
}

func (fence AssistantKeyAuthorizationFence) authFlowMatch() AuthFlowMatch {
	return AuthFlowMatch{
		Purpose:   AuthFlowPurposeAssistantKey,
		UserId:    fence.userID,
		SessionId: fence.sessionID,
	}
}

// authorizeAssistantKeyCreationTx is the commit-time authority. The caller
// locks the confirmation flow first; this function then locks user -> session
// -> factor/backup rows -> developer-access facts. Group/options and credential
// rows are intentionally handled afterward by the sole mutation entry point.
func authorizeAssistantKeyCreationTx(tx *gorm.DB, fence AssistantKeyAuthorizationFence, twoFactorCode string) (bool, error) {
	if tx == nil {
		return false, gorm.ErrInvalidDB
	}
	var user User
	if err := lockForUpdate(tx.Unscoped()).
		Where("id = ? AND deleted_at IS NULL", fence.userID).
		First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrAssistantKeyAuthorizationChanged
		}
		return false, err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != fence.expectedUserAuthVersion {
		return false, ErrAssistantKeyAuthorizationChanged
	}

	var session UserSession
	if err := lockForUpdate(tx).
		Where("sid = ? AND user_id = ?", fence.sessionID, fence.userID).
		First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrAssistantKeyAuthorizationChanged
		}
		return false, err
	}
	now := time.Now().Unix()
	if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now ||
		session.Version != fence.expectedSessionVersion ||
		session.UserAuthVersion != fence.expectedUserAuthVersion {
		return false, ErrAssistantKeyAuthorizationChanged
	}

	twoFactorAccepted, err := verifyAssistantKeyTwoFactorTx(tx, fence.userID, twoFactorCode)
	if err != nil {
		return false, err
	}
	if !twoFactorAccepted {
		return false, nil
	}

	access, err := GetDeveloperAccessStateForUserBaseWithTx(
		tx,
		user.ToBaseUser(),
		fence.developerAccessPolicy,
	)
	if err != nil {
		return false, err
	}
	if !access.Granted {
		return false, ErrAssistantKeyAuthorizationChanged
	}
	return true, nil
}
