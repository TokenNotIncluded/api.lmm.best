package model

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const (
	AssistantKeyManagementDraftVersion = 1
	AssistantKeyOperationDisable       = "disable"
	AssistantKeyOperationDelete        = "delete"
	AssistantKeyMetadataMaxLimit       = 50
)

var ErrAssistantKeyTargetChanged = errors.New("assistant API key target is unavailable or changed")

// AssistantKeyMetadata is the complete allowlist available to the assistant.
// It deliberately has no credential, partial credential, quota, or IP fields.
type AssistantKeyMetadata struct {
	ID           int    `json:"id" gorm:"column:id"`
	Name         string `json:"name"`
	Status       int    `json:"status"`
	Group        string `json:"group"`
	CreatedTime  int64  `json:"created_time"`
	AccessedTime int64  `json:"accessed_time"`
	ExpiredTime  int64  `json:"expired_time"`
}

// AssistantKeyManagementDraft binds the exact target shown for confirmation.
// Access times may advance while the user reviews it; identity, name, group,
// and status must still match at the final locked mutation.
type AssistantKeyManagementDraft struct {
	Version   int                  `json:"version"`
	Operation string               `json:"operation"`
	Key       AssistantKeyMetadata `json:"key"`
}

func assistantOwnManageableKeyQuery(db *gorm.DB, userID int) *gorm.DB {
	return db.Model(&Token{}).
		Where("user_id = ? AND oauth_managed = ?", userID, false).
		Where("creation_source IS NULL OR creation_source <> ?", TokenCreationSourceAssistantRuntime)
}

var assistantKeyMetadataColumns = []string{"id", "name", "status", "group", "created_time", "accessed_time", "expired_time"}

// ListAssistantKeyMetadata only reads allowlisted columns from own manageable
// keys. An optional name is an exact match, including literal SQL wildcards.
func ListAssistantKeyMetadata(userID, startIdx, limit int, exactName string) ([]AssistantKeyMetadata, int64, error) {
	if userID <= 0 || startIdx < 0 || limit <= 0 || limit > AssistantKeyMetadataMaxLimit {
		return nil, 0, gorm.ErrInvalidData
	}
	query := assistantOwnManageableKeyQuery(DB, userID)
	if exactName != "" {
		query = query.Where("name = ?", exactName)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return nil, 0, err
	}
	items := make([]AssistantKeyMetadata, 0, limit)
	if err := query.Select(assistantKeyMetadataColumns).Order("id DESC").Offset(startIdx).Limit(limit).Find(&items).Error; err != nil {
		return nil, 0, err
	}
	return items, count, nil
}

func GetAssistantKeyMetadataByID(userID, keyID int) (*AssistantKeyMetadata, error) {
	if userID <= 0 || keyID <= 0 {
		return nil, ErrAssistantKeyTargetChanged
	}
	var metadata AssistantKeyMetadata
	if err := assistantOwnManageableKeyQuery(DB, userID).
		Select(assistantKeyMetadataColumns).Where("id = ?", keyID).Take(&metadata).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAssistantKeyTargetChanged
		}
		return nil, err
	}
	return &metadata, nil
}

func assistantKeyMetadata(token *Token) *AssistantKeyMetadata {
	return &AssistantKeyMetadata{
		ID: token.Id, Name: token.Name, Status: token.Status, Group: token.Group,
		CreatedTime: token.CreatedTime, AccessedTime: token.AccessedTime, ExpiredTime: token.ExpiredTime,
	}
}

// authorizeAssistantKeyManagementTx shares creation's server-captured identity
// fence, but revocation is available to L0 owners as well. The lock order is
// flow -> user -> session -> factor/backup rows -> exact token.
func authorizeAssistantKeyManagementTx(tx *gorm.DB, fence AssistantKeyAuthorizationFence, code string) (bool, error) {
	if fence.userID <= 0 || fence.sessionID == "" || fence.expectedSessionVersion <= 0 || fence.expectedUserAuthVersion <= 0 {
		return false, ErrAssistantKeyAuthorizationChanged
	}
	var user User
	if err := lockForUpdate(tx.Unscoped()).Where("id = ? AND deleted_at IS NULL", fence.userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrAssistantKeyAuthorizationChanged
		}
		return false, err
	}
	if user.Status != common.UserStatusEnabled || user.AuthVersion != fence.expectedUserAuthVersion {
		return false, ErrAssistantKeyAuthorizationChanged
	}
	var session UserSession
	if err := lockForUpdate(tx).Where("sid = ? AND user_id = ?", fence.sessionID, fence.userID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, ErrAssistantKeyAuthorizationChanged
		}
		return false, err
	}
	now := time.Now().Unix()
	if session.Status != UserSessionStatusActive || session.RevokedAt != 0 || session.ExpiresAt <= now ||
		session.Version != fence.expectedSessionVersion || session.UserAuthVersion != fence.expectedUserAuthVersion {
		return false, ErrAssistantKeyAuthorizationChanged
	}
	if user.GetSetting().IsSessionAutoLogoutEnabled() && session.CreatedAt < now-int64(UserSessionAutoLogoutAge/time.Second) {
		return false, ErrAssistantKeyAuthorizationChanged
	}
	return verifyAssistantKeyTwoFactorTx(tx, fence.userID, code)
}

func decodeAssistantKeyManagementDraft(payload string) (*AssistantKeyManagementDraft, error) {
	var draft AssistantKeyManagementDraft
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&draft); err != nil {
		return nil, ErrAuthFlowInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF || draft.Version != AssistantKeyManagementDraftVersion || draft.Key.ID <= 0 ||
		(draft.Operation != AssistantKeyOperationDisable && draft.Operation != AssistantKeyOperationDelete) {
		return nil, ErrAuthFlowInvalid
	}
	return &draft, nil
}

// ConsumeAssistantKeyManagementFlow consumes the owner/session-bound draft
// atomically with a single exact-key revocation. It never accepts a request ID
// or writable token fields, and never rewrites concurrent quota usage.
func ConsumeAssistantKeyManagementFlow(confirmationToken string, fence AssistantKeyAuthorizationFence, twoFactorCode string) (*AssistantKeyMetadata, string, error) {
	if confirmationToken == "" || fence.userID <= 0 || fence.sessionID == "" {
		return nil, "", ErrAuthFlowInvalid
	}
	match := AuthFlowMatch{Purpose: AuthFlowPurposeAssistantKeyManagement, UserId: fence.userID, SessionId: fence.sessionID}
	var metadata *AssistantKeyMetadata
	var operation, cacheKey string
	var rejection error
	err := DB.Transaction(func(tx *gorm.DB) error {
		var flow AuthFlow
		if err := applyAuthFlowMatch(lockForUpdate(tx), confirmationToken, match).First(&flow).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAuthFlowInvalid
			}
			return err
		}
		if flow.ConsumedAt != nil {
			return ErrAuthFlowConsumed
		}
		if !flow.ExpiresAt.After(time.Now()) {
			return ErrAuthFlowExpired
		}
		draft, err := decodeAssistantKeyManagementDraft(flow.Payload)
		if err != nil {
			return err
		}
		authorized, err := authorizeAssistantKeyManagementTx(tx, fence, twoFactorCode)
		if err != nil {
			return err
		}
		if !authorized {
			// Persist failed-attempt protection without consuming the draft.
			rejection = ErrAssistantKeyTwoFactorInvalid
			return nil
		}
		var token Token
		if err := assistantOwnManageableKeyQuery(lockForUpdate(tx), fence.userID).Where("id = ?", draft.Key.ID).Take(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAssistantKeyTargetChanged
			}
			return err
		}
		if token.Name != draft.Key.Name || token.Group != draft.Key.Group || token.Status != draft.Key.Status {
			return ErrAssistantKeyTargetChanged
		}
		now := time.Now()
		result := tx.Model(&AuthFlow{}).Where("id = ? AND consumed_at IS NULL AND expires_at > ?", flow.Id, now).Update("consumed_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAuthFlowExpired
		}
		cacheKey = token.Key
		if err := invalidateTokenCacheForMutation(cacheKey); err != nil {
			return err
		}
		query := assistantOwnManageableKeyQuery(tx, fence.userID).Where("id = ?", token.Id)
		if draft.Operation == AssistantKeyOperationDelete {
			result = query.Delete(&Token{})
		} else if token.Status == common.TokenStatusDisabled {
			// MySQL reports zero changed rows for a no-op update. The locked
			// matching target is already revoked, so consume this confirmation
			// as an idempotent success without depending on affected-row mode.
			metadata, operation = assistantKeyMetadata(&token), draft.Operation
			return nil
		} else {
			result = query.Update("status", common.TokenStatusDisabled)
			token.Status = common.TokenStatusDisabled
		}
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAssistantKeyTargetChanged
		}
		metadata, operation = assistantKeyMetadata(&token), draft.Operation
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	if rejection != nil {
		return nil, "", rejection
	}
	// Refresh the fence after commit, covering a reader that crossed the
	// transaction boundary while holding an older database snapshot.
	if err := invalidateTokenCacheForMutation(cacheKey); err != nil {
		common.SysLog("failed to invalidate token cache after assistant revocation: " + err.Error())
	}
	return metadata, operation, nil
}
