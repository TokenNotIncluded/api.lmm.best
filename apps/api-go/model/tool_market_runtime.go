package model

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const ToolMarketWebClient = "web-market"

// Results are short-lived delivery data, never author analytics or logs.
type ToolMarketResult struct {
	CallID    string `json:"call_id" gorm:"primaryKey;size:64"`
	UserID    int    `json:"-" gorm:"index"`
	Success   bool   `json:"success"`
	Data      string `json:"-" gorm:"type:text"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at" gorm:"index"`
}

type ToolMarketToken struct {
	ID          string `json:"id" gorm:"primaryKey;size:36"`
	UserID      int    `json:"-" gorm:"index"`
	ClientID    string `json:"client_id" gorm:"size:128"`
	Digest      string `json:"-" gorm:"uniqueIndex;size:64"`
	AuthVersion int64  `json:"-"`
	CanInvoke   bool   `json:"can_invoke"`
	CanManage   bool   `json:"can_manage"`
	CreatedAt   int64  `json:"created_at"`
	ExpiresAt   int64  `json:"expires_at"`
	RevokedAt   int64  `json:"revoked_at"`
}

func CreateToolMarketToken(userID int, clientID string, invoke, manage bool, expiresAt int64) (string, *ToolMarketToken, error) {
	if !marketClientValid(clientID) || clientID == ToolMarketWebClient || strings.HasPrefix(clientID, "oauth:") || expiresAt <= common.GetTimestamp() || expiresAt > common.GetTimestamp()+90*86400 {
		return "", nil, ErrToolMarketInput
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", nil, err
	}
	raw := "lmm_market_" + base64.RawURLEncoding.EncodeToString(secret[:])
	row := ToolMarketToken{ID: uuid.NewString(), UserID: userID, ClientID: clientID, Digest: marketDigest(raw), CanInvoke: invoke, CanManage: manage, CreatedAt: common.GetTimestamp(), ExpiresAt: expiresAt}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := marketLockUsers(tx, userID); err != nil {
			return err
		}
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		var user User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		row.AuthVersion = user.AuthVersion
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return marketEvent(tx, userID, row.ID, "token.create", map[string]any{"client_id": clientID, "invoke": invoke, "manage": manage, "expires_at": expiresAt})
	})
	if err != nil {
		return "", nil, err
	}
	return raw, &row, nil
}

func VerifyToolMarketToken(raw string) (*ToolMarketToken, error) {
	if !strings.HasPrefix(raw, "lmm_market_") || len(raw) != len("lmm_market_")+43 {
		return nil, ErrToolMarketDenied
	}
	var row ToolMarketToken
	if err := DB.Where("digest = ? AND revoked_at = 0 AND expires_at > ?", marketDigest(raw), common.GetTimestamp()).First(&row).Error; err != nil {
		return nil, ErrToolMarketDenied
	}
	var user User
	if err := DB.First(&user, row.UserID).Error; err != nil || user.Status != common.UserStatusEnabled || user.AuthVersion != row.AuthVersion {
		return nil, ErrToolMarketDenied
	}
	return &row, nil
}

func RevokeToolMarketToken(userID int, id string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, userID, common.RoleCommonUser); err != nil {
			return err
		}
		q := tx.Model(&ToolMarketToken{}).Where("id = ? AND user_id = ?", id, userID).Update("revoked_at", common.GetTimestamp())
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return marketEvent(tx, userID, id, "token.revoke", nil)
	})
}

func RecordToolMarketValidation(actor int, serviceID, versionID, digest string, tools map[string]string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var service ToolMarketService
		if err := lockForUpdate(tx).First(&service, "id = ?", serviceID).Error; err != nil {
			return err
		}
		if service.OwnerID != actor {
			if err := marketUser(tx, actor, common.RoleAdminUser); err != nil {
				return err
			}
		}
		if service.DraftVersionID != versionID {
			return ErrToolMarketConflict
		}
		var version ToolMarketVersion
		if err := tx.First(&version, "id = ? AND service_id = ?", versionID, serviceID).Error; err != nil {
			return err
		}
		if version.Digest != digest || version.ExecutionType != "remote" || (version.Status != "draft" && version.Status != "pending") {
			return ErrToolMarketConflict
		}
		var definitions []ToolMarketToolVersion
		if err := tx.Where("version_id = ?", versionID).Find(&definitions).Error; err != nil {
			return err
		}
		if len(tools) != len(definitions) || len(tools) == 0 {
			return ErrToolMarketInput
		}
		for _, tool := range definitions {
			value, ok := tools[tool.ToolID]
			if !ok || len(value) != 64 {
				return ErrToolMarketInput
			}
			if err := tx.Model(&ToolMarketToolVersion{}).Where("version_id = ? AND tool_id = ?", versionID, tool.ToolID).Update("remote_digest", value).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&version).Update("validation_digest", digest).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, serviceID, "version.validate", map[string]string{"version_id": versionID, "digest": digest})
	})
}

func GetToolMarketReview(actor int, serviceID string) (*ToolMarketDetail, error) {
	if err := marketUser(DB, actor, common.RoleAdminUser); err != nil {
		return nil, err
	}
	var service ToolMarketService
	if err := DB.First(&service, "id = ?", serviceID).Error; err != nil {
		return nil, err
	}
	return GetToolMarketDetail(service.OwnerID, serviceID, true)
}

func ListToolMarketReviewQueue(actor int) ([]ToolMarketService, error) {
	if err := marketUser(DB, actor, common.RoleAdminUser); err != nil {
		return nil, err
	}
	rows := []ToolMarketService{}
	err := DB.Where("draft_version_id IN (?)", DB.Model(&ToolMarketVersion{}).Select("id").Where("status = ?", "pending")).Order("updated_at, id").Limit(100).Find(&rows).Error
	return rows, err
}

type ToolMarketExecution struct {
	Service ToolMarketService
	Version ToolMarketVersion
	Tool    ToolMarketToolVersion
	Grant   ToolMarketGrant
}

func GetToolMarketExecution(userID int, clientID, toolID, versionID, grantID string) (*ToolMarketExecution, error) {
	if err := marketUser(DB, userID, common.RoleCommonUser); err != nil {
		return nil, err
	}
	service, tool, err := marketLiveTool(DB, userID, toolID, versionID)
	if err != nil {
		return nil, err
	}
	var version ToolMarketVersion
	if err := DB.First(&version, "id = ?", versionID).Error; err != nil {
		return nil, err
	}
	var grant ToolMarketGrant
	q := DB.Where("user_id = ? AND client_id = ? AND tool_id = ? AND version_id = ? AND revoked_at = 0 AND expires_at > ?", userID, clientID, toolID, versionID, common.GetTimestamp())
	if grantID != "" {
		q = q.Where("id = ?", grantID)
	}
	if err := q.Order("created_at DESC, id").First(&grant).Error; err != nil {
		return nil, err
	}
	var installation ToolMarketInstallation
	if err := DB.First(&installation, "user_id = ? AND client_id = ? AND tool_id = ? AND version_id = ?", userID, clientID, toolID, versionID).Error; err != nil {
		return nil, err
	}
	return &ToolMarketExecution{Service: *service, Version: version, Tool: *tool, Grant: grant}, nil
}

func ListToolMarketExecutions(userID int, clientID string) ([]ToolMarketExecution, error) {
	var installs []ToolMarketInstallation
	if err := DB.Where("user_id = ? AND client_id = ?", userID, clientID).Order("tool_id").Limit(101).Find(&installs).Error; err != nil {
		return nil, err
	}
	if len(installs) > 100 {
		return nil, ErrToolMarketInput
	}
	rows := []ToolMarketExecution{}
	for _, item := range installs {
		row, err := GetToolMarketExecution(userID, clientID, item.ToolID, item.VersionID, "")
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrToolMarketDenied) {
			continue
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, *row)
	}
	return rows, nil
}

func GetToolMarketCall(userID int, clientID, id string) (*ToolMarketCall, error) {
	q := DB.Where("id = ? AND user_id = ?", id, userID)
	if clientID != "" {
		q = q.Where("client_id = ?", clientID)
	}
	var row ToolMarketCall
	err := q.First(&row).Error
	// A caller querying an expired hold must not wait for the next maintenance
	// batch. The same idempotent transaction is safe against a concurrent sweep.
	if err == nil && row.SettlementStatus == "held" && row.ResolveBy <= common.GetTimestamp() {
		if err = ExpireToolMarketCall(row.ID); err != nil {
			return nil, err
		}
		err = DB.First(&row, "id = ? AND user_id = ?", row.ID, userID).Error
	}
	return &row, err
}

// Checks replays before remote discovery, so a provider outage/schema change
// cannot prevent the caller from retrieving an already completed request.
func LookupToolMarketReplay(in ToolMarketReserveInput) (*ToolMarketCall, error) {
	digest, err := marketArgumentDigest(in.Arguments)
	if err != nil {
		return nil, err
	}
	call, err := GetToolMarketCall(in.UserID, in.ClientID, marketDigest([]any{in.UserID, in.ClientID, in.RequestKey}))
	if err != nil {
		return nil, err
	}
	if call.ToolID != in.ToolID || call.VersionID != in.VersionID || call.InputDigest != digest || (in.GrantID != "" && call.GrantID != in.GrantID) {
		return nil, ErrToolMarketConflict
	}
	return call, nil
}

func RecordToolMarketResult(callID string, success bool, data json.RawMessage) error {
	if len(data) > 2<<20 || !json.Valid(data) {
		return ErrToolMarketInput
	}
	return marketCallTx(callID, func(tx *gorm.DB, call *ToolMarketCall) error {
		// Delivery payloads must not become SQL parameter dumps on an error.
		tx = tx.Session(&gorm.Session{Logger: tx.Logger.LogMode(logger.Silent)})
		if call.SettlementStatus != "held" || (call.ExecutionStatus != "running" && call.ExecutionStatus != "unknown") {
			return ErrToolMarketConflict
		}
		now := common.GetTimestamp()
		row := ToolMarketResult{CallID: callID, UserID: call.UserID, Success: success, Data: string(data), CreatedAt: now, ExpiresAt: now + 3600}
		q := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected == 0 {
			var prior ToolMarketResult
			if err := tx.First(&prior, "call_id = ?", callID).Error; err != nil {
				return err
			}
			if prior.Success != success || prior.Data != string(data) {
				return ErrToolMarketConflict
			}
		}
		return nil
	})
}

func GetToolMarketResult(userID int, clientID, callID string) (json.RawMessage, int64, error) {
	if _, err := GetToolMarketCall(userID, clientID, callID); err != nil {
		return nil, 0, err
	}
	var row ToolMarketResult
	err := DB.First(&row, "call_id = ? AND user_id = ? AND expires_at > ?", callID, userID, common.GetTimestamp()).Error
	return json.RawMessage(row.Data), row.ExpiresAt, err
}

func GetToolMarketConfig() (ToolMarketConfig, error) {
	var config ToolMarketConfig
	err := DB.First(&config, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return config, nil
	}
	return config, err
}
