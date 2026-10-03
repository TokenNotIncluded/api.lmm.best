package model

import (
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const AuthFlowPurposeAssistantDisplayName = "assistant_display_name"

var (
	ErrAssistantDisplayNameInvalid    = errors.New("display name must contain 1 to 20 characters without control characters")
	ErrAssistantProfileSessionInvalid = errors.New("a current browser login session is required")
)

// NormalizeAssistantDisplayName shares the profile's 20-character limit.
// A nickname is one line of visible text, never a credential or a login name.
func NormalizeAssistantDisplayName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrAssistantDisplayNameInvalid
	}
	value = strings.TrimSpace(value)
	if err := common.Validate.Var(value, "required,max=20"); err != nil {
		return "", ErrAssistantDisplayNameInvalid
	}
	for _, character := range value {
		if unicode.IsControl(character) || unicode.Is(unicode.Zl, character) || unicode.Is(unicode.Zp, character) {
			return "", ErrAssistantDisplayNameInvalid
		}
	}
	return value, nil
}

// ConfirmAssistantDisplayName updates only the current user's nickname and
// consumes the browser-bound draft in the same transaction. The editable
// confirmation form supplies the final nickname; the token authorizes only
// this self-profile operation, never another account or another field.
func ConfirmAssistantDisplayName(token string, match AuthFlowMatch, sessionVersion, authVersion int64, displayName string) (*User, error) {
	name, err := NormalizeAssistantDisplayName(displayName)
	if err != nil {
		return nil, err
	}
	if match.Purpose != AuthFlowPurposeAssistantDisplayName || match.UserId <= 0 || match.SessionId == "" || sessionVersion <= 0 || authVersion <= 0 {
		return nil, ErrAssistantProfileSessionInvalid
	}
	var user User
	_, err = ConsumeAuthFlowWithAction(token, match, func(tx *gorm.DB, _ *AuthFlow) error {
		var session UserSession
		if err := lockForUpdate(tx).Where("sid = ?", match.SessionId).First(&session).Error; err != nil {
			return ErrAssistantProfileSessionInvalid
		}
		now := time.Now().Unix()
		if session.UserID != match.UserId || session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now || session.Version != sessionVersion || session.UserAuthVersion != authVersion {
			return ErrAssistantProfileSessionInvalid
		}
		if err := lockForUpdate(tx).First(&user, match.UserId).Error; err != nil {
			return ErrAssistantProfileSessionInvalid
		}
		if user.Status != common.UserStatusEnabled || user.AuthVersion != authVersion || (user.GetSetting().IsSessionAutoLogoutEnabled() && session.CreatedAt < now-int64(UserSessionAutoLogoutAge/time.Second)) {
			return ErrAssistantProfileSessionInvalid
		}
		if err := tx.Model(&user).Update("display_name", name).Error; err != nil {
			return err
		}
		user.DisplayName = name
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A committed nickname change remains successful even if Redis is down.
	// UserBase currently omits display_name; evict the cache rather than writing
	// a potentially stale snapshot of authorization or quota fields.
	if err := InvalidateUserCache(user.Id); err != nil {
		common.SysError("assistant display-name cache invalidation failed: " + err.Error())
	}
	return &user, nil
}
