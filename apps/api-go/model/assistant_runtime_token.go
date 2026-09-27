package model

import (
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

var ErrAssistantRuntimeTokenUnavailable = errors.New("enabled super administrator and assistant group are required")
var ErrAssistantRuntimeTokenManaged = errors.New("the assistant runtime key is managed by the system")

// EnsureAssistantRuntimeToken binds assistant model calls to one durable,
// root-owned key. The key is internal-only: normal API key authentication and
// reveal endpoints must never accept or disclose it.
func EnsureAssistantRuntimeToken(userID int, group string) (*Token, bool, error) {
	group = strings.TrimSpace(group)
	if DB == nil || userID <= 0 || group == "" {
		return nil, false, ErrAssistantRuntimeTokenUnavailable
	}

	var token Token
	created := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).
			Where("id = ? AND role = ? AND status = ?", userID, common.RoleRootUser, common.UserStatusEnabled).
			First(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssistantRuntimeTokenUnavailable
			}
			return err
		}

		now := common.GetTimestamp()
		err := tx.Where("user_id = ? AND oauth_managed = ? AND creation_source = ?", userID, false, TokenCreationSourceAssistantRuntime).
			Order("id ASC").First(&token).Error
		if err == nil {
			updates := map[string]any{
				"name":            "AI assistant runtime",
				"group":           group,
				"status":          common.TokenStatusEnabled,
				"expired_time":    -1,
				"unlimited_quota": true,
				"one_time_reveal": true,
			}
			if err := tx.Model(&Token{}).Where("id = ?", token.Id).Updates(updates).Error; err != nil {
				return err
			}
			token.Name = "AI assistant runtime"
			token.Group = group
			token.Status = common.TokenStatusEnabled
			token.ExpiredTime = -1
			token.UnlimitedQuota = true
			token.OneTimeReveal = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		key, err := common.GenerateKey()
		if err != nil {
			return err
		}
		token = Token{
			UserId: userID, Name: "AI assistant runtime", Key: key,
			Group: group, Status: common.TokenStatusEnabled,
			CreatedTime: now, AccessedTime: 0, ExpiredTime: -1,
			UnlimitedQuota: true, OneTimeReveal: true,
			CreationSource: TokenCreationSourceAssistantRuntime,
		}
		if err := tx.Create(&token).Error; err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &token, created, nil
}

// TouchAssistantRuntimeToken records a real assistant request, not a key-list
// page view, as the key's last use.
func TouchAssistantRuntimeToken(userID, tokenID int) error {
	if DB == nil || userID <= 0 || tokenID <= 0 {
		return ErrAssistantRuntimeTokenUnavailable
	}
	result := DB.Model(&Token{}).
		Where("id = ? AND user_id = ? AND creation_source = ?", tokenID, userID, TokenCreationSourceAssistantRuntime).
		Update("accessed_time", common.GetTimestamp())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrAssistantRuntimeTokenUnavailable
	}
	return nil
}
