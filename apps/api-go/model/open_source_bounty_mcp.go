package model

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	openSourceBountyMCPTokenPrefix     = "lmm_mcp_"
	openSourceBountyMCPConfirmationTTL = int64(10 * 60)
)

type OpenSourceBountyMCPToken struct {
	Id              int    `json:"id"`
	UserId          int    `json:"user_id" gorm:"not null;uniqueIndex"`
	UserAuthVersion int64  `json:"-" gorm:"bigint;not null;default:0"`
	TokenHash       string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	TokenHint       string `json:"token_hint" gorm:"type:varchar(24);not null"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint;not null"`
	UpdatedAt       int64  `json:"updated_at" gorm:"bigint;not null"`
	LastUsedAt      int64  `json:"last_used_at" gorm:"bigint;not null;default:0"`
}

func (OpenSourceBountyMCPToken) TableName() string { return "open_source_bounty_mcp_tokens" }

type OpenSourceBountyMCPConfirmation struct {
	Id          string `json:"id" gorm:"type:varchar(80);primaryKey"`
	UserId      int    `json:"user_id" gorm:"not null;index"`
	ToolName    string `json:"tool_name" gorm:"type:varchar(128);not null;index"`
	PayloadHash string `json:"payload_hash" gorm:"type:char(64);not null"`
	ExpiresAt   int64  `json:"expires_at" gorm:"bigint;not null;index"`
	ConsumedAt  int64  `json:"consumed_at" gorm:"bigint;not null;default:0;index"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint;not null"`
}

func (OpenSourceBountyMCPConfirmation) TableName() string {
	return "open_source_bounty_mcp_confirmations"
}

type OpenSourceBountyMCPOperation struct {
	Id          string `json:"id" gorm:"type:varchar(80);primaryKey"`
	UserId      int    `json:"user_id" gorm:"not null;index"`
	ToolName    string `json:"tool_name" gorm:"type:varchar(128);not null;index"`
	PayloadHash string `json:"payload_hash" gorm:"type:char(64);not null"`
	ResultJson  string `json:"result_json" gorm:"type:text;not null"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint;not null;index"`
}

type OpenSourceBountyMCPConfirmedOperation struct {
	State                      string
	ToolName                   string
	PayloadHash                string
	PlatformFeeRecipientUserId int
}

func (OpenSourceBountyMCPOperation) TableName() string {
	return "open_source_bounty_mcp_operations"
}

type OpenSourceBountyMCPTokenStatus struct {
	Configured bool   `json:"configured"`
	TokenHint  string `json:"token_hint,omitempty"`
	CreatedAt  int64  `json:"created_at,omitempty"`
	UpdatedAt  int64  `json:"updated_at,omitempty"`
	LastUsedAt int64  `json:"last_used_at,omitempty"`
}

func newOpenSourceBountyMCPSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := cryptorand.Read(raw); err != nil {
		return "", err
	}
	return openSourceBountyMCPTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func openSourceBountyMCPTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func OpenSourceBountyMCPPayloadHash(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func RotateOpenSourceBountyMCPToken(userId int) (string, *OpenSourceBountyMCPToken, error) {
	authVersion, err := openSourceBountyDeveloperAuthVersion(userId)
	if err != nil {
		return "", nil, bountyError("OPEN_SOURCE_BOUNTY_MCP_FORBIDDEN", "developer access is required to create an MCP token")
	}

	for attempt := 0; attempt < 3; attempt++ {
		token, err := newOpenSourceBountyMCPSecret()
		if err != nil {
			return "", nil, err
		}
		now := common.GetTimestamp()
		hint := token[:len(openSourceBountyMCPTokenPrefix)] + "••••" + token[len(token)-8:]
		record := OpenSourceBountyMCPToken{
			UserId: userId, UserAuthVersion: authVersion, TokenHash: openSourceBountyMCPTokenHash(token), TokenHint: hint,
			CreatedAt: now, UpdatedAt: now,
		}
		err = DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"token_hash": record.TokenHash, "token_hint": record.TokenHint,
				"user_auth_version": authVersion, "updated_at": now, "last_used_at": 0,
			}),
		}).Create(&record).Error
		if err == nil {
			if err := DB.Where("user_id = ?", userId).First(&record).Error; err != nil {
				return "", nil, err
			}
			return token, &record, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", nil, err
		}
	}
	return "", nil, errors.New("failed to generate a unique MCP token")
}

func GetOpenSourceBountyMCPTokenStatus(userId int) (*OpenSourceBountyMCPTokenStatus, error) {
	if err := RequireOpenSourceBountyDeveloperAccess(userId); err != nil {
		return nil, bountyError("OPEN_SOURCE_BOUNTY_MCP_FORBIDDEN", "developer access is required to inspect an MCP token")
	}
	var token OpenSourceBountyMCPToken
	err := DB.Where("user_id = ?", userId).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &OpenSourceBountyMCPTokenStatus{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &OpenSourceBountyMCPTokenStatus{
		Configured: true, TokenHint: token.TokenHint, CreatedAt: token.CreatedAt,
		UpdatedAt: token.UpdatedAt, LastUsedAt: token.LastUsedAt,
	}, nil
}

func RevokeOpenSourceBountyMCPToken(userId int) error {
	if err := RequireOpenSourceBountyDeveloperAccess(userId); err != nil {
		return bountyError("OPEN_SOURCE_BOUNTY_MCP_FORBIDDEN", "developer access is required to revoke an MCP token")
	}
	return DB.Where("user_id = ?", userId).Delete(&OpenSourceBountyMCPToken{}).Error
}

func VerifyOpenSourceBountyMCPToken(rawToken string) (int, error) {
	if !strings.HasPrefix(rawToken, openSourceBountyMCPTokenPrefix) || len(rawToken) < len(openSourceBountyMCPTokenPrefix)+32 {
		return 0, bountyError("OPEN_SOURCE_BOUNTY_MCP_INVALID_TOKEN", "invalid MCP token")
	}
	var token OpenSourceBountyMCPToken
	err := DB.Table("open_source_bounty_mcp_tokens AS token").
		Select("token.*").
		Joins("JOIN users AS token_user ON token_user.id = token.user_id AND token_user.deleted_at IS NULL AND token_user.status = ? AND token_user.auth_version = token.user_auth_version", common.UserStatusEnabled).
		Where("token.token_hash = ?", openSourceBountyMCPTokenHash(rawToken)).
		First(&token).Error
	if err != nil {
		return 0, bountyError("OPEN_SOURCE_BOUNTY_MCP_INVALID_TOKEN", "invalid MCP token")
	}
	if err := RequireOpenSourceBountyDeveloperAccess(token.UserId); err != nil {
		return 0, bountyError("OPEN_SOURCE_BOUNTY_MCP_INVALID_TOKEN", "invalid MCP token")
	}
	now := common.GetTimestamp()
	if token.LastUsedAt < now-60 {
		_ = DB.Model(&OpenSourceBountyMCPToken{}).
			Where("id = ? AND last_used_at < ?", token.Id, now-60).
			UpdateColumn("last_used_at", now).Error
	}
	return token.UserId, nil
}

func CreateOpenSourceBountyMCPConfirmation(userId int, toolName string, payloadHash string) (string, error) {
	raw := make([]byte, 24)
	if _, err := cryptorand.Read(raw); err != nil {
		return "", err
	}
	now := common.GetTimestamp()
	state := "mcp_confirm_" + base64.RawURLEncoding.EncodeToString(raw)
	confirmation := OpenSourceBountyMCPConfirmation{
		Id: state, UserId: userId, ToolName: toolName, PayloadHash: payloadHash,
		ExpiresAt: now + openSourceBountyMCPConfirmationTTL, CreatedAt: now,
	}
	_ = DB.Where("expires_at < ? OR (consumed_at > 0 AND consumed_at < ?)", now, now-86400).
		Delete(&OpenSourceBountyMCPConfirmation{}).Error
	return state, DB.Create(&confirmation).Error
}

func ConsumeOpenSourceBountyMCPConfirmation(userId int, toolName string, payloadHash string, state string) error {
	now := common.GetTimestamp()
	valid := false
	found := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var confirmation OpenSourceBountyMCPConfirmation
		if err := lockForUpdate(tx).Where("id = ? AND user_id = ? AND tool_name = ?", state, userId, toolName).First(&confirmation).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		found = true
		if confirmation.ConsumedAt != 0 || confirmation.ExpiresAt < now {
			return nil
		}
		if err := tx.Model(&confirmation).Update("consumed_at", now).Error; err != nil {
			return err
		}
		valid = confirmation.PayloadHash == payloadHash
		return nil
	})
	if err != nil {
		return err
	}
	if !found || !valid {
		return bountyError("OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID", "confirmation is missing, expired, already used, or does not match this action")
	}
	return nil
}

func GetOpenSourceBountyMCPOperationResult(userId int, toolName string, state string) (map[string]any, bool, error) {
	if state == "" {
		return nil, false, nil
	}
	var operation OpenSourceBountyMCPOperation
	err := DB.Where("id = ? AND user_id = ? AND tool_name = ?", state, userId, toolName).First(&operation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(operation.ResultJson), &result); err != nil {
		return nil, false, err
	}
	return result, true, nil
}

func validateOpenSourceBountyMCPConfirmationTx(tx *gorm.DB, userId int, toolName string, payloadHash string, state string) error {
	now := common.GetTimestamp()
	var confirmation OpenSourceBountyMCPConfirmation
	if err := lockForUpdate(tx).Where("id = ? AND user_id = ? AND tool_name = ?", state, userId, toolName).First(&confirmation).Error; err != nil {
		return bountyError("OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID", "confirmation is missing, expired, already used, or does not match this action")
	}
	if confirmation.ConsumedAt != 0 || confirmation.ExpiresAt < now || confirmation.PayloadHash != payloadHash {
		return bountyError("OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID", "confirmation is missing, expired, already used, or does not match this action")
	}
	return nil
}

func completeOpenSourceBountyMCPOperationTx(tx *gorm.DB, userId int, toolName string, payloadHash string, state string, result any) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	if err := tx.Create(&OpenSourceBountyMCPOperation{
		Id: state, UserId: userId, ToolName: toolName, PayloadHash: payloadHash,
		ResultJson: string(encoded), CreatedAt: now,
	}).Error; err != nil {
		return err
	}
	updated := tx.Model(&OpenSourceBountyMCPConfirmation{}).
		Where("id = ? AND user_id = ? AND tool_name = ? AND payload_hash = ? AND consumed_at = 0", state, userId, toolName, payloadHash).
		Update("consumed_at", now)
	if updated.Error != nil {
		return updated.Error
	}
	if updated.RowsAffected != 1 {
		return bountyError("OPEN_SOURCE_BOUNTY_MCP_CONFIRMATION_INVALID", "confirmation could not be committed with this operation")
	}
	return nil
}
