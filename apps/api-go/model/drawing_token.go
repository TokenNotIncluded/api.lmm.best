package model

import (
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"gorm.io/gorm"
)

const DrawingTokenGroup = "image-2"

var (
	ErrDrawingTokenAccessDenied     = errors.New("developer access is required for drawing keys")
	ErrDrawingTokenGroupUnavailable = errors.New("the selected drawing group is not available to this account")
	ErrDrawingTokenRequired         = errors.New("create an API key for the selected group in key management first")
	ErrDrawingTokenLimit            = errors.New("the maximum API key count has been reached")
	ErrDrawingTokenWarningRequired  = errors.New("acknowledge the group warning in key management before creating a drawing key")
)

// ResolveDrawingToken selects the oldest non-deleted key in the exact group.
// Disabled, expired, exhausted or restricted keys are deliberately NOT filtered
// out: the normal relay authentication/distribution policy must reject them,
// rather than silently provisioning a replacement or trying a less restricted
// key. Deleting that key in key management is an explicit user choice.
//
// groupAllowed runs against the locked account's current group, avoiding a
// model -> service dependency while retaining the normal group policy. The user
// row serializes concurrent first-use creation; all reads are repeated inside
// that transaction. No token secret should be serialized by callers.
func ResolveDrawingToken(userID int, group string, groupAllowed func(string, string) bool) (*Token, bool, error) {
	group = strings.TrimSpace(group)
	if userID <= 0 || group == "" || groupAllowed == nil {
		return nil, false, ErrDrawingTokenAccessDenied
	}
	var token Token
	created := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return ErrDrawingTokenAccessDenied
		}
		access, err := GetDeveloperAccessStateForUserBaseWithTx(tx, user.ToBaseUser(), CurrentDeveloperAccessPolicy())
		if err != nil {
			return err
		}
		if !access.Granted {
			return ErrDrawingTokenAccessDenied
		}
		if !groupAllowed(user.Group, group) {
			return ErrDrawingTokenGroupUnavailable
		}
		err = tx.Where("user_id = ? AND "+commonGroupCol+" = ?", userID, group).Order("id ASC").First(&token).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if group != DrawingTokenGroup {
			return ErrDrawingTokenRequired
		}
		// The body{} convenience API has no acknowledgement flow. Never infer
		// consent, including the default zero-ratio group's multi-step warning.
		if _, required := ratio_setting.GetGroupWarning(group); required {
			return ErrDrawingTokenWarningRequired
		}
		var count int64
		if err := tx.Model(&Token{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(operation_setting.GetMaxUserTokens()) {
			return ErrDrawingTokenLimit
		}
		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		token = Token{
			UserId: userID, Key: key, Name: "drawing-image-2", Group: DrawingTokenGroup,
			Status: common.TokenStatusEnabled, CreatedTime: now, AccessedTime: now,
			ExpiredTime: -1, UnlimitedQuota: true,
		}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ? AND console_activated_at = ?", userID, 0).
			Update("console_activated_at", now).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if created {
		if err := invalidateUserCache(userID); err != nil {
			common.SysLog("failed to invalidate user cache after drawing console activation")
		}
	}
	return &token, created, nil
}
