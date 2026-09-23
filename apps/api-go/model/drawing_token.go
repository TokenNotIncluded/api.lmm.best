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
		err = tx.Where("user_id = ? AND "+commonGroupCol+" = ? AND oauth_managed = ? AND (creation_source IS NULL OR creation_source <> ?)", userID, group, false, TokenCreationSourceAssistantRuntime).Order("id ASC").First(&token).Error
		if err == nil {
			if token.CreationSource != TokenCreationSourceDrawingMCP {
				if err := tx.Model(&Token{}).Where("id = ?", token.Id).Update("creation_source", TokenCreationSourceDrawingMCP).Error; err != nil {
					return err
				}
				token.CreationSource = TokenCreationSourceDrawingMCP
			}
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
		if err := tx.Model(&Token{}).Where("user_id = ? AND oauth_managed = ? AND (creation_source IS NULL OR creation_source <> ?)", userID, false, TokenCreationSourceAssistantRuntime).Count(&count).Error; err != nil {
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
			ExpiredTime: -1, UnlimitedQuota: true, CreationSource: TokenCreationSourceDrawingMCP,
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

// ResolveDrawingTokenByID enforces an explicit user-selected API key. Unlike
// ResolveDrawingToken it never creates or falls back to another key.
func ResolveDrawingTokenByID(userID, tokenID int, group string) (*Token, error) {
	if userID <= 0 || tokenID <= 0 {
		return nil, ErrDrawingTokenRequired
	}
	var token Token
	if err := DB.Where("id = ? AND user_id = ? AND oauth_managed = ?", tokenID, userID, false).First(&token).Error; err != nil {
		return nil, ErrDrawingTokenRequired
	}
	if token.CreationSource == TokenCreationSourceAssistantRuntime {
		return nil, ErrDrawingTokenRequired
	}
	if token.Status != common.TokenStatusEnabled || (token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp()) || (!token.UnlimitedQuota && token.RemainQuota <= 0) {
		return nil, ErrDrawingTokenRequired
	}
	if strings.TrimSpace(group) == "" {
		return &token, nil
	}
	if strings.TrimSpace(token.Group) == "" {
		var user User
		if err := DB.Select("id", "group").First(&user, userID).Error; err != nil || strings.TrimSpace(user.Group) != strings.TrimSpace(group) {
			return nil, ErrDrawingTokenGroupUnavailable
		}
		return &token, nil
	}
	if token.Group != "auto" && strings.TrimSpace(token.Group) != "" && strings.TrimSpace(token.Group) != strings.TrimSpace(group) {
		return nil, ErrDrawingTokenGroupUnavailable
	}
	if token.Group == "auto" {
		groups, err := token.GetAutoGroups()
		if err != nil {
			return nil, ErrDrawingTokenGroupUnavailable
		}
		allowed := false
		for _, candidate := range groups {
			if strings.TrimSpace(candidate) == strings.TrimSpace(group) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, ErrDrawingTokenGroupUnavailable
		}
	}
	return &token, nil
}
