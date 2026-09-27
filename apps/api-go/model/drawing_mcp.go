package model

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const drawingMCPTokenPrefix = "lmm_drawing_mcp_"

var (
	ErrDrawingMCPForbidden    = errors.New("developer access is required for a drawing MCP token")
	ErrDrawingMCPKeyInvalid   = errors.New("the selected API key is unavailable for drawing MCP")
	ErrDrawingMCPTokenInvalid = errors.New("invalid drawing MCP token")
)

type DrawingMCPToken struct {
	Id              int    `json:"id"`
	UserId          int    `json:"user_id" gorm:"not null;uniqueIndex"`
	ApiKeyId        int    `json:"api_key_id" gorm:"not null;index"`
	UserAuthVersion int64  `json:"-" gorm:"bigint;not null;default:0"`
	TokenHash       string `json:"-" gorm:"type:char(64);not null;uniqueIndex"`
	TokenHint       string `json:"token_hint" gorm:"type:varchar(40);not null"`
	CreatedAt       int64  `json:"created_at" gorm:"bigint;not null"`
	RotatedAt       int64  `json:"rotated_at" gorm:"bigint;not null"`
	LastUsedAt      int64  `json:"last_used_at" gorm:"bigint;not null;default:0"`
	DefaultModel    string `json:"default_model" gorm:"type:varchar(64);not null;default:''"`
}

func (DrawingMCPToken) TableName() string { return "drawing_mcp_tokens" }

type DrawingMCPTokenStatus struct {
	Configured   bool   `json:"configured"`
	ApiKeyId     int    `json:"api_key_id,omitempty"`
	TokenHint    string `json:"token_hint,omitempty"`
	CreatedAt    int64  `json:"created_at,omitempty"`
	RotatedAt    int64  `json:"rotated_at,omitempty"`
	LastUsedAt   int64  `json:"last_used_at,omitempty"`
	DefaultModel string `json:"default_model,omitempty"`
}

type DrawingMCPTokenIdentity struct {
	UserId       int
	ApiKeyId     int
	DefaultModel string
}

type DrawingMCPAPIKey struct {
	Id                 int    `json:"id"`
	Name               string `json:"name"`
	Group              string `json:"group"`
	Status             int    `json:"status"`
	ExpiredTime        int64  `json:"expired_time"`
	RemainQuota        int    `json:"remain_quota"`
	UsedQuota          int    `json:"used_quota"`
	UnlimitedQuota     bool   `json:"unlimited_quota"`
	ModelLimitsEnabled bool   `json:"model_limits_enabled"`
}

func ListDrawingMCPAPIKeys(userId int) ([]DrawingMCPAPIKey, error) {
	if userId <= 0 {
		return nil, ErrDrawingMCPForbidden
	}
	var user User
	if err := DB.First(&user, userId).Error; err != nil {
		return nil, ErrDrawingMCPForbidden
	}
	access, err := GetDeveloperAccessStateForUserBase(user.ToBaseUser())
	if err != nil || !access.Granted {
		return nil, ErrDrawingMCPForbidden
	}
	var tokens []Token
	if err := DB.Where("user_id = ? AND oauth_managed = ? AND (creation_source IS NULL OR creation_source <> ?)", userId, false, TokenCreationSourceAssistantRuntime).Order("id ASC").Find(&tokens).Error; err != nil {
		return nil, err
	}
	keys := make([]DrawingMCPAPIKey, 0, len(tokens))
	for _, token := range tokens {
		keys = append(keys, DrawingMCPAPIKey{Id: token.Id, Name: token.Name, Group: token.Group, Status: token.Status, ExpiredTime: token.ExpiredTime, RemainQuota: token.RemainQuota, UsedQuota: token.UsedQuota, UnlimitedQuota: token.UnlimitedQuota, ModelLimitsEnabled: token.ModelLimitsEnabled})
	}
	return keys, nil
}

func newDrawingMCPSecret() (string, error) {
	raw := make([]byte, 32)
	if _, err := cryptorand.Read(raw); err != nil {
		return "", err
	}
	return drawingMCPTokenPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func drawingMCPTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func validateDrawingMCPKey(tx *gorm.DB, userId, apiKeyId int) (*Token, error) {
	if userId <= 0 || apiKeyId <= 0 {
		return nil, ErrDrawingMCPKeyInvalid
	}
	var token Token
	err := tx.Where("id = ? AND user_id = ? AND oauth_managed = ?", apiKeyId, userId, false).First(&token).Error
	if err != nil || token.Status != common.TokenStatusEnabled || token.CreationSource == TokenCreationSourceAssistantRuntime {
		return nil, ErrDrawingMCPKeyInvalid
	}
	if token.ExpiredTime != -1 && token.ExpiredTime < common.GetTimestamp() {
		return nil, ErrDrawingMCPKeyInvalid
	}
	if !token.UnlimitedQuota && token.RemainQuota <= 0 {
		return nil, ErrDrawingMCPKeyInvalid
	}
	return &token, nil
}

func RotateDrawingMCPToken(userId, apiKeyId int, defaultModels ...string) (string, *DrawingMCPTokenStatus, error) {
	if userId <= 0 || apiKeyId <= 0 {
		return "", nil, ErrDrawingMCPForbidden
	}
	token, err := newDrawingMCPSecret()
	if err != nil {
		return "", nil, err
	}
	now := common.GetTimestamp()
	defaultModel := ""
	if len(defaultModels) > 0 {
		defaultModel = strings.TrimSpace(defaultModels[0])
	}
	var status DrawingMCPTokenStatus
	err = DB.Transaction(func(tx *gorm.DB) error {
		var user User
		if err := lockForUpdate(tx).First(&user, userId).Error; err != nil || user.Status != common.UserStatusEnabled {
			return ErrDrawingMCPForbidden
		}
		access, err := GetDeveloperAccessStateForUserBaseWithTx(tx, user.ToBaseUser(), CurrentDeveloperAccessPolicy())
		if err != nil || !access.Granted {
			return ErrDrawingMCPForbidden
		}
		if _, err := validateDrawingMCPKey(tx, userId, apiKeyId); err != nil {
			return err
		}
		record := DrawingMCPToken{
			UserId: userId, ApiKeyId: apiKeyId, UserAuthVersion: user.AuthVersion,
			TokenHash: drawingMCPTokenHash(token), TokenHint: drawingMCPTokenHint(token),
			CreatedAt: now, RotatedAt: now, DefaultModel: defaultModel,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"api_key_id": apiKeyId, "user_auth_version": user.AuthVersion,
				"token_hash": record.TokenHash, "token_hint": record.TokenHint,
				"rotated_at": now, "last_used_at": 0, "default_model": defaultModel,
			}),
		}).Create(&record).Error; err != nil {
			return err
		}
		status = DrawingMCPTokenStatus{Configured: true, ApiKeyId: apiKeyId, TokenHint: record.TokenHint, CreatedAt: record.CreatedAt, RotatedAt: record.RotatedAt, DefaultModel: defaultModel}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	return token, &status, nil
}

func drawingMCPTokenHint(token string) string {
	return drawingMCPTokenPrefix + "••••" + token[len(token)-8:]
}

func GetDrawingMCPTokenStatus(userId int) (*DrawingMCPTokenStatus, error) {
	if userId <= 0 {
		return nil, ErrDrawingMCPForbidden
	}
	var user User
	if err := DB.First(&user, userId).Error; err != nil {
		return nil, ErrDrawingMCPForbidden
	}
	access, err := GetDeveloperAccessStateForUserBase(user.ToBaseUser())
	if err != nil || !access.Granted {
		return nil, ErrDrawingMCPForbidden
	}
	var token DrawingMCPToken
	err = DB.Where("user_id = ?", userId).First(&token).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &DrawingMCPTokenStatus{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &DrawingMCPTokenStatus{Configured: true, ApiKeyId: token.ApiKeyId, TokenHint: token.TokenHint, CreatedAt: token.CreatedAt, RotatedAt: token.RotatedAt, LastUsedAt: token.LastUsedAt, DefaultModel: token.DefaultModel}, nil
}

func RevokeDrawingMCPToken(userId int) error {
	if userId <= 0 {
		return ErrDrawingMCPForbidden
	}
	return DB.Where("user_id = ?", userId).Delete(&DrawingMCPToken{}).Error
}

func VerifyDrawingMCPToken(rawToken string) (DrawingMCPTokenIdentity, error) {
	if !strings.HasPrefix(rawToken, drawingMCPTokenPrefix) || len(rawToken) < len(drawingMCPTokenPrefix)+32 {
		return DrawingMCPTokenIdentity{}, ErrDrawingMCPTokenInvalid
	}
	var token DrawingMCPToken
	err := DB.Table("drawing_mcp_tokens AS token").Select("token.*").
		Joins("JOIN users AS token_user ON token_user.id = token.user_id AND token_user.deleted_at IS NULL AND token_user.status = ? AND token_user.auth_version = token.user_auth_version", common.UserStatusEnabled).
		Where("token.token_hash = ?", drawingMCPTokenHash(rawToken)).First(&token).Error
	if err != nil {
		return DrawingMCPTokenIdentity{}, ErrDrawingMCPTokenInvalid
	}
	if _, err := validateDrawingMCPKey(DB, token.UserId, token.ApiKeyId); err != nil {
		return DrawingMCPTokenIdentity{}, ErrDrawingMCPTokenInvalid
	}
	now := common.GetTimestamp()
	if token.LastUsedAt < now-60 {
		_ = DB.Model(&DrawingMCPToken{}).Where("id = ? AND last_used_at < ?", token.Id, now-60).UpdateColumn("last_used_at", now).Error
	}
	return DrawingMCPTokenIdentity{UserId: token.UserId, ApiKeyId: token.ApiKeyId, DefaultModel: token.DefaultModel}, nil
}
