package model

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"sort"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

// ToolMarketBuiltinServiceInput is supplied only by the compiled-in registry.
// It is intentionally separate from author drafts and has no owner, endpoint,
// visibility or execution-type inputs that a client could use to impersonate
// a system service. Prices are always zero, including on updates.
type ToolMarketBuiltinServiceInput struct {
	Key         string
	Name        string
	Description string
	Tools       []ToolMarketToolInput
}

// Stable public identities let installations and grants survive restarts.
// Schema changes produce a new immutable version and require reauthorization.
func ToolMarketBuiltinServiceID(key string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://api.lmm.best/tool-market/builtin/"+key)).String()
}

// ToolMarketBuiltinVersionID lets a local dispatcher verify that the persisted
// exact-version grant matches the definitions compiled into this process.
func ToolMarketBuiltinVersionID(input ToolMarketBuiltinServiceInput) (string, error) {
	normalized, err := normalizeMarketBuiltin(input)
	if err != nil {
		return "", err
	}
	return marketBuiltinVersionID(normalized), nil
}

func marketBuiltinVersionID(normalized ToolMarketBuiltinServiceInput) string {
	return uuid.NewSHA1(uuid.MustParse(ToolMarketBuiltinServiceID(normalized.Key)), []byte(marketDigest(normalized))).String()
}

func marketBuiltinVersion(service ToolMarketService, version ToolMarketVersion) bool {
	return service.OwnerID == 0 && version.ServiceID == service.ID && version.ExecutionType == "builtin" && version.Endpoint == ""
}

func canonicalMarketSchema(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) > 16384 {
		return nil, ErrToolMarketInput
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil || value == nil {
		return nil, ErrToolMarketInput
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, ErrToolMarketInput
	}
	return json.Marshal(value)
}

func normalizeMarketBuiltin(in ToolMarketBuiltinServiceInput) (ToolMarketBuiltinServiceInput, error) {
	if !toolMarketName.MatchString(in.Key) {
		return in, ErrToolMarketInput
	}
	in.Tools = append([]ToolMarketToolInput(nil), in.Tools...)
	for i := range in.Tools {
		tool := &in.Tools[i]
		tool.PriceQuota = 0
		var err error
		tool.InputSchema, err = canonicalMarketSchema(tool.InputSchema)
		if err != nil {
			return in, err
		}
		tool.OutputSchema, err = canonicalMarketSchema(tool.OutputSchema)
		if err != nil {
			return in, err
		}
		tool.Permissions = append([]string(nil), tool.Permissions...)
		sort.Strings(tool.Permissions)
	}
	sort.Slice(in.Tools, func(i, j int) bool { return in.Tools[i].Name < in.Tools[j].Name })
	// Reuse structural schema/permission limits, without pretending that this
	// local registry passed remote network validation.
	err := validateMarketDraft(ToolMarketDraftInput{Name: in.Name, Description: in.Description, ExecutionType: "serverless", Visibility: "public", Tools: in.Tools})
	return in, err
}

// EnsureToolMarketBuiltinServices registers trusted local handlers. It never
// enables third-party trading, changes a user's draft, or rewrites a published
// version. Registering the same definitions is idempotent across processes.
func EnsureToolMarketBuiltinServices(definitions []ToolMarketBuiltinServiceInput) error {
	if len(definitions) == 0 || len(definitions) > 32 {
		return ErrToolMarketInput
	}
	normalized := make([]ToolMarketBuiltinServiceInput, len(definitions))
	seen := make(map[string]bool, len(definitions))
	for i, definition := range definitions {
		input, err := normalizeMarketBuiltin(definition)
		if err != nil {
			return err
		}
		if seen[input.Key] {
			return ErrToolMarketInput
		}
		seen[input.Key] = true
		normalized[i] = input
	}
	return marketTransaction(DB, func(tx *gorm.DB) error {
		for _, input := range normalized {
			serviceID := ToolMarketBuiltinServiceID(input.Key)
			now := common.GetTimestamp()
			service := ToolMarketService{ID: serviceID, OwnerID: 0, Status: "published", CreatedAt: now, UpdatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&service).Error; err != nil {
				return err
			}
			if err := lockForUpdate(tx).First(&service, "id = ?", serviceID).Error; err != nil {
				return err
			}
			if service.OwnerID != 0 || service.DraftVersionID != "" {
				return ErrToolMarketDenied
			}
			if service.LiveVersionID != "" {
				var live ToolMarketVersion
				if err := tx.First(&live, "id = ?", service.LiveVersionID).Error; err != nil {
					return err
				}
				if !marketBuiltinVersion(service, live) {
					return ErrToolMarketDenied
				}
			}
			digest := marketDigest(input)
			versionID := marketBuiltinVersionID(input)
			version := ToolMarketVersion{ID: versionID, ServiceID: serviceID, Status: "published", Name: input.Name, Description: input.Description,
				ExecutionType: "builtin", Visibility: "public", Digest: digest, CreatedAt: now, PublishedAt: now}
			q := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&version)
			if q.Error != nil {
				return q.Error
			}
			if q.RowsAffected == 0 {
				var prior ToolMarketVersion
				if err := tx.First(&prior, "id = ?", versionID).Error; err != nil {
					return err
				}
				if !marketBuiltinVersion(service, prior) || prior.Digest != digest || prior.Status != "published" || prior.Visibility != "public" {
					return ErrToolMarketConflict
				}
			} else {
				for _, definition := range input.Tools {
					toolID := uuid.NewSHA1(uuid.MustParse(serviceID), []byte(definition.Name)).String()
					tool := ToolMarketTool{ID: toolID, ServiceID: serviceID, Name: definition.Name}
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tool).Error; err != nil {
						return err
					}
					var prior ToolMarketTool
					if err := tx.First(&prior, "id = ?", toolID).Error; err != nil {
						return err
					}
					if prior.ServiceID != serviceID || prior.Name != definition.Name {
						return ErrToolMarketConflict
					}
					permissions, _ := json.Marshal(definition.Permissions)
					row := ToolMarketToolVersion{VersionID: versionID, ToolID: toolID, Name: definition.Name, Description: definition.Description,
						InputSchema: string(definition.InputSchema), OutputSchema: string(definition.OutputSchema), Permissions: string(permissions), PriceQuota: 0}
					if err := tx.Create(&row).Error; err != nil {
						return err
					}
				}
			}
			if service.LiveVersionID == versionID && service.Status == "published" {
				continue
			}
			if err := tx.Model(&service).Updates(map[string]any{"live_version_id": versionID, "status": "published", "updated_at": now}).Error; err != nil {
				return err
			}
			if err := marketEvent(tx, 0, serviceID, "builtin.register", map[string]string{"key": input.Key, "version_id": versionID, "digest": digest}); err != nil {
				return err
			}
		}
		return nil
	})
}

// VerifyToolMarketBuiltinServices never repairs or registers records. Workers
// can become ready only after the migration writer publishes this exact catalog.
func VerifyToolMarketBuiltinServices(ctx context.Context, definitions []ToolMarketBuiltinServiceInput) error {
	if len(definitions) == 0 || len(definitions) > 32 {
		return ErrToolMarketInput
	}
	seen := make(map[string]bool, len(definitions))
	db := DB.WithContext(ctx)
	for _, definition := range definitions {
		input, err := normalizeMarketBuiltin(definition)
		if err != nil {
			return err
		}
		if seen[input.Key] {
			return ErrToolMarketInput
		}
		seen[input.Key] = true
		serviceID := ToolMarketBuiltinServiceID(input.Key)
		versionID := marketBuiltinVersionID(input)
		var service ToolMarketService
		if err := db.First(&service, "id = ?", serviceID).Error; err != nil {
			return err
		}
		if service.OwnerID != 0 || service.Status != "published" || service.DraftVersionID != "" || service.LiveVersionID != versionID {
			return ErrToolMarketConflict
		}
		var version ToolMarketVersion
		if err := db.First(&version, "id = ? AND service_id = ?", versionID, serviceID).Error; err != nil {
			return err
		}
		if !marketBuiltinVersion(service, version) || version.Status != "published" || version.Visibility != "public" || version.AllowedUsers != "" || version.Digest != marketDigest(input) || version.Name != input.Name || version.Description != input.Description {
			return ErrToolMarketConflict
		}
		var tools []ToolMarketToolVersion
		if err := db.Where("version_id = ?", versionID).Order("name, tool_id").Find(&tools).Error; err != nil {
			return err
		}
		if len(tools) != len(input.Tools) {
			return ErrToolMarketConflict
		}
		for i, tool := range tools {
			expected := input.Tools[i]
			toolID := uuid.NewSHA1(uuid.MustParse(serviceID), []byte(expected.Name)).String()
			permissions, _ := json.Marshal(expected.Permissions)
			if tool.ToolID != toolID || tool.Name != expected.Name || tool.Description != expected.Description || tool.PriceQuota != 0 || tool.RemoteDigest != "" || tool.InputSchema != string(expected.InputSchema) || tool.OutputSchema != string(expected.OutputSchema) || tool.Permissions != string(permissions) {
				return ErrToolMarketConflict
			}
			var identity ToolMarketTool
			if err := db.First(&identity, "id = ?", toolID).Error; err != nil {
				return err
			}
			if identity.ServiceID != serviceID || identity.Name != expected.Name {
				return ErrToolMarketConflict
			}
		}
	}
	return nil
}

func marketExecutionConfig(tx *gorm.DB, service ToolMarketService, versionID string, price int) (ToolMarketConfig, error) {
	var version ToolMarketVersion
	if err := tx.First(&version, "id = ? AND service_id = ?", versionID, service.ID).Error; err != nil {
		return ToolMarketConfig{}, err
	}
	if marketBuiltinVersion(service, version) {
		if price != 0 {
			return ToolMarketConfig{}, ErrToolMarketDenied
		}
		return ToolMarketConfig{}, nil
	}
	// A forged builtin marker cannot turn an ordinary service into a free
	// trusted dispatch, even if an invalid record was inserted out of band.
	if service.OwnerID == 0 || version.ExecutionType == "builtin" {
		return ToolMarketConfig{}, ErrToolMarketDenied
	}
	var config ToolMarketConfig
	if err := lockForShare(tx).First(&config, 1).Error; err != nil {
		return config, err
	}
	if !config.Enabled || config.FeeBPS < 0 || config.FeeBPS > 10000 {
		return config, ErrToolMarketDenied
	}
	if err := marketUser(tx, config.RecipientID, common.RoleRootUser); err != nil {
		return config, err
	}
	return config, marketUser(tx, service.OwnerID, common.RoleCommonUser)
}

func marketAuthorizeCallDispatch(tx *gorm.DB, call ToolMarketCall) error {
	service, tool, err := marketLiveTool(tx, call.UserID, call.ToolID, call.VersionID)
	if err != nil {
		return err
	}
	if _, err := marketExecutionConfig(tx, *service, call.VersionID, tool.PriceQuota); err != nil {
		return err
	}
	var grant ToolMarketGrant
	if err := tx.Scopes(marketExactTextScope("client_id", call.ClientID)).First(&grant, "id = ? AND user_id = ? AND tool_id = ? AND version_id = ?", call.GrantID, call.UserID, call.ToolID, call.VersionID).Error; err != nil {
		return err
	}
	if grant.RevokedAt != 0 || grant.ExpiresAt <= common.GetTimestamp() {
		return ErrToolMarketDenied
	}
	var installation ToolMarketInstallation
	return tx.Scopes(marketExactTextScope("client_id", call.ClientID)).First(&installation, "user_id = ? AND tool_id = ? AND version_id = ?", call.UserID, call.ToolID, call.VersionID).Error
}

// Confirmation is a delivery stage of the same call, not a successful call.
// This separate short-lived row leaves the final immutable result available
// for idempotent replay and holds the original grant slot until completion.
type ToolMarketBuiltinContinuation struct {
	CallID      string                 `json:"-" gorm:"primaryKey;size:64"`
	StateDigest string                 `json:"-" gorm:"size:64;not null"`
	AuthVersion int64                  `json:"-" gorm:"not null"`
	Data        ToolMarketDeliveryData `json:"-"`
	CreatedAt   int64                  `json:"-"`
}

func RecordToolMarketBuiltinConfirmation(callID, state string, data json.RawMessage) error {
	if state == "" || len(state) > 512 || len(data) > 2<<20 || !json.Valid(data) {
		return ErrToolMarketInput
	}
	return marketCallTx(callID, func(tx *gorm.DB, call *ToolMarketCall) error {
		if call.OwnerID != 0 || call.PriceQuota != 0 || call.ExecutionStatus != "running" || call.SettlementStatus != "held" || call.ResolveBy <= common.GetTimestamp() {
			return ErrToolMarketConflict
		}
		if err := marketLockUsers(tx, call.UserID); err != nil {
			return err
		}
		if err := marketUser(tx, call.UserID, common.RoleCommonUser); err != nil {
			return err
		}
		var user User
		if err := tx.Select("id", "auth_version").First(&user, call.UserID).Error; err != nil {
			return err
		}
		tx = tx.Session(&gorm.Session{Logger: tx.Logger.LogMode(logger.Silent)})
		row := ToolMarketBuiltinContinuation{CallID: callID, StateDigest: marketDigest(state), AuthVersion: user.AuthVersion, Data: ToolMarketDeliveryData(data), CreatedAt: common.GetTimestamp()}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Model(call).Update("execution_status", "awaiting_confirmation").Error
	})
}

func GetToolMarketBuiltinConfirmation(userID int, clientID, callID string) (json.RawMessage, error) {
	call, err := GetToolMarketCall(userID, clientID, callID)
	if err != nil {
		return nil, err
	}
	if call.ExecutionStatus != "awaiting_confirmation" || call.SettlementStatus != "held" {
		return nil, gorm.ErrRecordNotFound
	}
	var row ToolMarketBuiltinContinuation
	err = DB.First(&row, "call_id = ?", callID).Error
	return json.RawMessage(row.Data), err
}

// ResumeToolMarketBuiltinCall has the same single-winner dispatch guarantee as
// StartToolMarketCall and rechecks grants after the user confirms. A stale or
// cross-call state cannot resume execution; already-running replays return false.
func ResumeToolMarketBuiltinCall(callID, state string) (bool, error) {
	if state == "" || len(state) > 512 {
		return false, ErrToolMarketInput
	}
	started := false
	err := marketCallTx(callID, func(tx *gorm.DB, call *ToolMarketCall) error {
		if call.ExecutionStatus != "awaiting_confirmation" || call.SettlementStatus != "held" {
			return nil
		}
		if call.OwnerID != 0 || call.PriceQuota != 0 || call.ResolveBy <= common.GetTimestamp() {
			return ErrToolMarketDenied
		}
		if err := marketLockUsers(tx, call.UserID); err != nil {
			return err
		}
		if err := marketUser(tx, call.UserID, common.RoleCommonUser); err != nil {
			return err
		}
		if err := marketAuthorizeCallDispatch(tx, *call); err != nil {
			return err
		}
		var row ToolMarketBuiltinContinuation
		if err := tx.First(&row, "call_id = ?", callID).Error; err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(row.StateDigest), []byte(marketDigest(state))) != 1 {
			return ErrToolMarketDenied
		}
		var user User
		if err := tx.Select("id", "auth_version").First(&user, call.UserID).Error; err != nil {
			return err
		}
		if row.AuthVersion != user.AuthVersion {
			return ErrToolMarketDenied
		}
		if err := tx.Where("call_id = ?", callID).Delete(&ToolMarketBuiltinContinuation{}).Error; err != nil {
			return err
		}
		// Confirmation time does not consume the next execution window. The
		// pending state must still be valid before this bounded extension.
		if err := tx.Model(call).Updates(map[string]any{"execution_status": "running", "resolve_by": common.GetTimestamp() + 120}).Error; err != nil {
			return err
		}
		started = true
		return nil
	})
	return started && err == nil, err
}
