package model

import (
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"gorm.io/gorm"
)

var (
	ErrRedPacketTokenAccessDenied     = errors.New("developer access is required for red packet cover generation")
	ErrRedPacketTokenGroupUnavailable = errors.New("the selected group is not available to this account")
	ErrRedPacketTokenLimit            = errors.New("the maximum API key count has been reached")
	ErrRedPacketTokenWarningRequired  = errors.New("acknowledge the group warning in key management before creating a red packet cover key")
)

// ResolveRedPacketCoverToken selects or creates a key for red packet cover generation.
// Similar to ResolveDrawingToken but uses a configurable group instead of hardcoded image-2.
func ResolveRedPacketCoverToken(userID int, group string, groupAllowed func(string, string) bool) (*Token, bool, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		group = "image-2" // default group
	}
	if userID <= 0 || groupAllowed == nil {
		return nil, false, ErrRedPacketTokenAccessDenied
	}
	var token Token
	created := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Status != common.UserStatusEnabled {
			return ErrRedPacketTokenAccessDenied
		}
		access, err := GetDeveloperAccessStateForUserBaseWithTx(tx, user.ToBaseUser(), CurrentDeveloperAccessPolicy())
		if err != nil {
			return err
		}
		if !access.Granted {
			return ErrRedPacketTokenAccessDenied
		}
		if !groupAllowed(user.Group, group) {
			return ErrRedPacketTokenGroupUnavailable
		}
		// Look for existing red_packet_cover token in the specified group
		err = tx.Where("user_id = ? AND "+commonGroupCol+" = ? AND oauth_managed = ? AND creation_source = ?",
			userID, group, false, TokenCreationSourceRedPacketCover).Order("id ASC").First(&token).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// Check group warning requirement
		if _, required := ratio_setting.GetGroupWarning(group); required {
			return ErrRedPacketTokenWarningRequired
		}
		// Check token count limit
		var count int64
		if err := tx.Model(&Token{}).Where("user_id = ? AND oauth_managed = ? AND (creation_source IS NULL OR creation_source <> ?)", userID, false, TokenCreationSourceAssistantRuntime).Count(&count).Error; err != nil {
			return err
		}
		if count >= int64(operation_setting.GetMaxUserTokens()) {
			return ErrRedPacketTokenLimit
		}
		// Create new token
		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		now := common.GetTimestamp()
		token = Token{
			UserId: userID, Key: key, Name: "red-packet-cover-" + group, Group: group,
			Status: common.TokenStatusEnabled, CreatedTime: now, AccessedTime: now,
			ExpiredTime: -1, UnlimitedQuota: true, CreationSource: TokenCreationSourceRedPacketCover,
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
			common.SysLog("failed to invalidate user cache after red packet cover token creation")
		}
	}
	return &token, created, nil
}
