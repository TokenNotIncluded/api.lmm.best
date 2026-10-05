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
	"gorm.io/gorm/schema"
)

const ToolMarketWebClient = "web-market"

const (
	ToolMarketResultMaxBytes = 2 << 20
	// Match the normal relay response ceiling, allowing the MCP wrapper.
	ToolMarketDrawingResponseMaxBytes = 32 << 20
	ToolMarketDrawingResultMaxBytes   = ToolMarketDrawingResponseMaxBytes + (64 << 10)
)

// TEXT is only 64 KiB on MySQL; delivery payloads need the same capacity as
// their accepted envelopes. PostgreSQL and SQLite TEXT already support them.
type ToolMarketDeliveryData string

func (ToolMarketDeliveryData) GormDataType() string { return "text" }
func (ToolMarketDeliveryData) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db != nil && db.Dialector.Name() == "mysql" {
		return "LONGTEXT"
	}
	return "TEXT"
}

// Results are short-lived delivery data, never author analytics or logs.
type ToolMarketResult struct {
	CallID           string                 `json:"call_id" gorm:"primaryKey;size:64"`
	UserID           int                    `json:"-" gorm:"index"`
	Success          bool                   `json:"success"`
	InputTokens      int                    `json:"-" gorm:"not null;default:0"`
	UsageRecorded    bool                   `json:"-" gorm:"not null;default:false"`
	MeteringVerified bool                   `json:"-" gorm:"not null;default:false"`
	UsageQuantities  map[string]int64       `json:"-" gorm:"serializer:json;type:text"`
	Data             ToolMarketDeliveryData `json:"-"`
	// A valid generated image is recoverable before normal model settlement.
	// This flag keeps that delivery outcome from completing the market call.
	BuiltinBillingPending bool  `json:"-" gorm:"not null;default:false"`
	CreatedAt             int64 `json:"created_at"`
	ExpiresAt             int64 `json:"expires_at" gorm:"index"`
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
	err := marketTransaction(DB, func(tx *gorm.DB) error {
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
	return marketTransaction(DB, func(tx *gorm.DB) error {
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
	return RecordToolMarketValidationWithCredential(actor, serviceID, versionID, digest, tools, "")
}

// Credential identity is captured before remote discovery and compared under
// the service lock. Rotation/removal must invalidate an older validation even
// if that network request finishes after the new credential was saved.
func RecordToolMarketValidationWithCredential(actor int, serviceID, versionID, digest string, tools map[string]string, expectedCredentialID string) error {
	return marketTransaction(DB, func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var service ToolMarketService
		if err := lockForUpdate(tx).First(&service, "id = ?", serviceID).Error; err != nil {
			return err
		}
		if service.OwnerID == 0 {
			return ErrToolMarketDenied
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
		if err := lockForUpdate(tx).First(&version, "id = ? AND service_id = ?", versionID, serviceID).Error; err != nil {
			return err
		}
		if version.Digest != digest || version.ExecutionType != "remote" || (version.Status != "draft" && version.Status != "pending") {
			return ErrToolMarketConflict
		}
		var credential ToolMarketCredential
		if err := tx.Select("id", "owner_id", "service_id").First(&credential, "version_id = ?", versionID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		} else if credential.OwnerID != service.OwnerID || credential.ServiceID != service.ID {
			return ErrToolMarketConflict
		}
		if credential.ID != expectedCredentialID {
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
	q := DB.Where("user_id = ? AND tool_id = ? AND version_id = ? AND revoked_at = 0 AND expires_at > ?", userID, toolID, versionID, common.GetTimestamp()).Scopes(marketExactTextScope("client_id", clientID))
	if grantID != "" {
		q = q.Where("id = ?", grantID)
	}
	if err := q.Order("created_at DESC, id").First(&grant).Error; err != nil {
		return nil, err
	}
	var installation ToolMarketInstallation
	if err := DB.Scopes(marketExactTextScope("client_id", clientID)).First(&installation, "user_id = ? AND tool_id = ? AND version_id = ?", userID, toolID, versionID).Error; err != nil {
		return nil, err
	}
	return &ToolMarketExecution{Service: *service, Version: version, Tool: *tool, Grant: grant}, nil
}

func ListToolMarketExecutions(userID int, clientID string) ([]ToolMarketExecution, error) {
	var installs []ToolMarketInstallation
	if err := DB.Where("user_id = ?", userID).Scopes(marketExactTextScope("client_id", clientID)).Order("tool_id").Limit(101).Find(&installs).Error; err != nil {
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
		q = q.Scopes(marketExactTextScope("client_id", clientID))
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
	return recordToolMarketResult(callID, success, data, false, false)
}

// Only the code-owned drawing adapter can use the larger delivery envelope.
func RecordToolMarketBuiltinDrawingResult(callID string, data json.RawMessage) error {
	return recordToolMarketResult(callID, true, data, true, false)
}

func PrepareToolMarketBuiltinDrawingResult(callID string, data json.RawMessage) error {
	return recordToolMarketResult(callID, true, data, true, true)
}

func marketDrawingResultCall(tx *gorm.DB, call *ToolMarketCall) error {
	if call.OwnerID != 0 || call.PriceQuota != 0 || call.ServiceID != ToolMarketBuiltinServiceID("drawing") {
		return ErrToolMarketDenied
	}
	var version ToolMarketVersion
	if err := tx.First(&version, "id = ? AND execution_type = ?", call.VersionID, "builtin").Error; err != nil {
		return ErrToolMarketDenied
	}
	var tool ToolMarketToolVersion
	if err := tx.First(&tool, "tool_id = ? AND version_id = ? AND name = ?", call.ToolID, call.VersionID, "drawing.generate").Error; err != nil {
		return ErrToolMarketDenied
	}
	return nil
}

func CompleteToolMarketBuiltinDrawingBilling(callID string, settled bool) error {
	return marketCallTx(callID, func(tx *gorm.DB, call *ToolMarketCall) error {
		if err := marketDrawingResultCall(tx, call); err != nil {
			return err
		}
		var result ToolMarketResult
		if err := lockForUpdate(tx).Select("call_id", "builtin_billing_pending").First(&result, "call_id = ? AND success = ?", callID, true).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrToolMarketConflict
			}
			return err
		}
		if !settled {
			return nil
		}
		if result.BuiltinBillingPending {
			if err := tx.Model(&ToolMarketResult{}).Where("call_id = ?", callID).Update("builtin_billing_pending", false).Error; err != nil {
				return err
			}
		}
		if call.ExecutionStatus == "unknown" && call.SettlementStatus == "released" {
			// Expiry released the market reservation while model billing was
			// pending. Resolve only the now-known execution; preserve released
			// budgets/slots and never repeat model or marketplace settlement.
			return tx.Model(call).Update("execution_status", "succeeded").Error
		}
		return nil
	})
}

func recordToolMarketResult(callID string, success bool, data json.RawMessage, drawing, billingPending bool) error {
	limit := ToolMarketResultMaxBytes
	if drawing {
		limit = ToolMarketDrawingResultMaxBytes
	}
	if len(data) > limit || !json.Valid(data) {
		return ErrToolMarketInput
	}
	return marketCallTx(callID, func(tx *gorm.DB, call *ToolMarketCall) error {
		// Delivery payloads must not become SQL parameter dumps on an error.
		tx = tx.Session(&gorm.Session{Logger: tx.Logger.LogMode(logger.Silent)})
		if drawing {
			if err := marketDrawingResultCall(tx, call); err != nil {
				return err
			}
		}
		if call.SettlementStatus != "held" || (call.ExecutionStatus != "running" && call.ExecutionStatus != "unknown") {
			return ErrToolMarketConflict
		}
		inputTokens, usageRecorded := 0, false
		var quantities map[string]int64
		if success && call.BillingMode != "" {
			var err error
			quantities, err = VerifyToolMarketMeteringResult(tx, *call, data)
			if err != nil {
				return err
			}
			inputTokens, usageRecorded = int(quantities["input_tokens"]), true
		}
		now := common.GetTimestamp()
		row := ToolMarketResult{InputTokens: inputTokens, UsageRecorded: usageRecorded, MeteringVerified: usageRecorded, UsageQuantities: quantities, CallID: callID, UserID: call.UserID, Success: success, Data: ToolMarketDeliveryData(data), BuiltinBillingPending: billingPending, CreatedAt: now, ExpiresAt: now + 3600}
		q := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected == 0 {
			var prior ToolMarketResult
			if err := lockForUpdate(tx).First(&prior, "call_id = ?", callID).Error; err != nil {
				return err
			}
			if prior.Success != success || string(prior.Data) != string(data) || (!billingPending && prior.BuiltinBillingPending) {
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
