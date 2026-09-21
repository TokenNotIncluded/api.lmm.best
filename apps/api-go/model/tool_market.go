package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrToolMarketInput    = errors.New("invalid tool market input")
	ErrToolMarketDenied   = errors.New("tool market operation not permitted")
	ErrToolMarketConflict = errors.New("tool market state or request conflicts")
	ErrToolMarketBudget   = errors.New("tool market spending limit exceeded")
	ErrToolMarketBalance  = errors.New("insufficient available balance")
)

// Published versions are immutable snapshots. Editing a service only changes
// its draft pointer; installations and grants remain bound to exact versions.
type ToolMarketService struct {
	ID             string `json:"id" gorm:"primaryKey;size:36"`
	OwnerID        int    `json:"owner_id" gorm:"not null;index"`
	LiveVersionID  string `json:"live_version_id" gorm:"size:36"`
	DraftVersionID string `json:"draft_version_id" gorm:"size:36"`
	Status         string `json:"status" gorm:"size:24;not null;index"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

type ToolMarketVersion struct {
	ID               string `json:"id" gorm:"primaryKey;size:36"`
	ServiceID        string `json:"service_id" gorm:"size:36;not null;index"`
	Status           string `json:"status" gorm:"size:24;not null;index"`
	Name             string `json:"name" gorm:"size:120;not null"`
	Description      string `json:"description" gorm:"type:text"`
	ExecutionType    string `json:"execution_type" gorm:"size:24;not null"`
	Visibility       string `json:"visibility" gorm:"size:24;not null"`
	Endpoint         string `json:"endpoint" gorm:"size:2048"`
	AllowedUsers     string `json:"-" gorm:"type:text"`
	Digest           string `json:"digest" gorm:"size:64;not null"`
	ValidationDigest string `json:"-" gorm:"size:64"`
	ReviewedBy       int    `json:"reviewed_by"`
	ReviewNote       string `json:"review_note" gorm:"size:1000"`
	CreatedAt        int64  `json:"created_at"`
	PublishedAt      int64  `json:"published_at"`
}

// Tool identities survive schema and price changes, scoped to their service.
type ToolMarketTool struct {
	ID        string `json:"id" gorm:"primaryKey;size:36"`
	ServiceID string `json:"service_id" gorm:"size:36;not null;uniqueIndex:idx_tm_tool_name,priority:1"`
	Name      string `json:"name" gorm:"size:128;not null;uniqueIndex:idx_tm_tool_name,priority:2"`
}

type ToolMarketToolVersion struct {
	VersionID    string `json:"version_id" gorm:"primaryKey;size:36"`
	ToolID       string `json:"tool_id" gorm:"primaryKey;size:36"`
	Name         string `json:"name" gorm:"size:128;not null"`
	Description  string `json:"description" gorm:"type:text"`
	InputSchema  string `json:"input_schema" gorm:"type:text"`
	OutputSchema string `json:"output_schema" gorm:"type:text"`
	Permissions  string `json:"permissions" gorm:"type:text"`
	PriceQuota   int    `json:"price_quota" gorm:"not null"`
	RemoteDigest string `json:"-" gorm:"size:64"`
}

type ToolMarketFavorite struct {
	UserID    int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	ServiceID string `json:"service_id" gorm:"primaryKey;size:36"`
	CreatedAt int64  `json:"created_at"`
}

type ToolMarketAccess struct {
	VersionID string `json:"version_id" gorm:"primaryKey;size:36"`
	UserID    int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
}

type ToolMarketInstallation struct {
	UserID    int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	ClientID  string `json:"client_id" gorm:"primaryKey;size:128"`
	ToolID    string `json:"tool_id" gorm:"primaryKey;size:36"`
	VersionID string `json:"version_id" gorm:"size:36;not null"`
	CreatedAt int64  `json:"created_at"`
}

type ToolMarketGrant struct {
	ID              string `json:"id" gorm:"primaryKey;size:36"`
	UserID          int    `json:"user_id" gorm:"not null;index"`
	ClientID        string `json:"client_id" gorm:"size:128;not null"`
	ToolID          string `json:"tool_id" gorm:"size:36;not null"`
	VersionID       string `json:"version_id" gorm:"size:36;not null"`
	MaxPriceQuota   int    `json:"max_price_quota"`
	MaxTotalQuota   int    `json:"max_total_quota"`
	MaxCalls        int    `json:"max_calls"`
	ReservedQuota   int    `json:"reserved_quota"`
	SpentQuota      int    `json:"spent_quota"`
	ReservedCalls   int    `json:"reserved_calls"`
	SuccessfulCalls int    `json:"successful_calls"`
	CreatedAt       int64  `json:"created_at"`
	ExpiresAt       int64  `json:"expires_at"`
	RevokedAt       int64  `json:"revoked_at"`
}

// Budgets have no implicit unlimited value: zero means zero paid spending.
// An absent budget imposes no additional restriction beyond the required grant.
type ToolMarketBudget struct {
	UserID        int    `json:"user_id" gorm:"primaryKey;autoIncrement:false"`
	Scope         string `json:"scope" gorm:"primaryKey;size:16"`
	ScopeID       string `json:"scope_id" gorm:"primaryKey;size:128"`
	LimitQuota    int    `json:"limit_quota"`
	ReservedQuota int    `json:"reserved_quota"`
	SpentQuota    int    `json:"spent_quota"`
}

// A root-controlled singleton, read transactionally when reserving a call.
type ToolMarketConfig struct {
	ID          int   `json:"-" gorm:"primaryKey;autoIncrement:false"`
	Enabled     bool  `json:"enabled"`
	FeeBPS      int   `json:"fee_bps"`
	RecipientID int   `json:"recipient_id"`
	UpdatedBy   int   `json:"updated_by"`
	UpdatedAt   int64 `json:"updated_at"`
}

type ToolMarketEvent struct {
	ID        string `json:"id" gorm:"primaryKey;size:36"`
	ActorID   int    `json:"actor_id" gorm:"index"`
	ObjectID  string `json:"object_id" gorm:"size:128;index"`
	Action    string `json:"action" gorm:"size:48"`
	Details   string `json:"details" gorm:"type:text"`
	CreatedAt int64  `json:"created_at"`
}

func toolMarketModels() []interface{} {
	return []interface{}{&ToolMarketService{}, &ToolMarketVersion{}, &ToolMarketTool{}, &ToolMarketToolVersion{}, &ToolMarketAccess{},
		&ToolMarketFavorite{}, &ToolMarketInstallation{}, &ToolMarketGrant{}, &ToolMarketBudget{}, &ToolMarketConfig{},
		&ToolMarketEvent{}, &ToolMarketCall{}, &ToolMarketTransfer{}, &ToolMarketResult{}, &ToolMarketToken{}}
}

type ToolMarketToolInput struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
	Permissions  []string        `json:"permissions"`
	PriceQuota   int             `json:"price_quota"`
}

type ToolMarketDraftInput struct {
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	ExecutionType string                `json:"execution_type"`
	Visibility    string                `json:"visibility"`
	Endpoint      string                `json:"endpoint"`
	AllowedUsers  []int                 `json:"allowed_users"`
	Tools         []ToolMarketToolInput `json:"tools"`
}

var toolMarketName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

func marketQuotaValid(n int) bool { return n >= 0 && common.ValidateWalletQuota(n) == nil }

func marketDigest(value any) string {
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func marketUser(tx *gorm.DB, id int, role int) error {
	if id <= 0 {
		return ErrToolMarketDenied
	}
	var count int64
	if err := tx.Model(&User{}).Where("id = ? AND status = ? AND role >= ?", id, common.UserStatusEnabled, role).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrToolMarketDenied
	}
	return nil
}

func marketEvent(tx *gorm.DB, actor int, object, action string, details any) error {
	data, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return tx.Create(&ToolMarketEvent{ID: uuid.NewString(), ActorID: actor, ObjectID: object, Action: action, Details: string(data), CreatedAt: common.GetTimestamp()}).Error
}

func validateMarketDraft(in ToolMarketDraftInput) error {
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 120 || len(in.Description) > 8000 || len(in.Tools) == 0 || len(in.Tools) > 100 || len(in.AllowedUsers) > 100 {
		return ErrToolMarketInput
	}
	if in.ExecutionType != "remote" && in.ExecutionType != "serverless" {
		return ErrToolMarketInput
	}
	if in.Visibility != "public" && in.Visibility != "private" && in.Visibility != "shared" {
		return ErrToolMarketInput
	}
	if in.Visibility != "shared" && len(in.AllowedUsers) != 0 {
		return ErrToolMarketInput
	}
	for _, id := range in.AllowedUsers {
		if id <= 0 {
			return ErrToolMarketInput
		}
	}
	if in.ExecutionType == "remote" {
		u, err := url.Parse(in.Endpoint)
		if err != nil || len(in.Endpoint) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return ErrToolMarketInput
		}
	} else if in.Endpoint != "" {
		return ErrToolMarketInput
	}
	seen := map[string]bool{}
	for _, tool := range in.Tools {
		if !toolMarketName.MatchString(tool.Name) || seen[tool.Name] || len(tool.Description) > 4000 || !marketQuotaValid(tool.PriceQuota) || len(tool.Permissions) > 16 {
			return ErrToolMarketInput
		}
		seen[tool.Name] = true
		for _, schema := range []json.RawMessage{tool.InputSchema, tool.OutputSchema} {
			if len(schema) > 16384 {
				return ErrToolMarketInput
			}
			if len(schema) == 0 {
				continue
			}
			var obj map[string]any
			if json.Unmarshal(schema, &obj) != nil || obj == nil || obj["type"] != "object" {
				return ErrToolMarketInput
			}
		}
		if len(tool.InputSchema) == 0 {
			return ErrToolMarketInput
		}
		for _, permission := range tool.Permissions {
			switch permission {
			case "read", "write", "delete", "send", "network", "files", "external_account":
			default:
				return ErrToolMarketInput
			}
		}
	}
	return nil
}

// SaveToolMarketDraft does structural validation only, never remote connection
// validation. Even structurally valid drafts cannot be published by this API.
func SaveToolMarketDraft(actor int, serviceID string, in ToolMarketDraftInput) (*ToolMarketService, error) {
	in.Tools = append([]ToolMarketToolInput(nil), in.Tools...)
	for i := range in.Tools {
		if strings.TrimSpace(string(in.Tools[i].OutputSchema)) == "null" {
			in.Tools[i].OutputSchema = nil
		}
	}
	if err := validateMarketDraft(in); err != nil {
		return nil, err
	}
	var service ToolMarketService
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		now := common.GetTimestamp()
		if serviceID == "" {
			service = ToolMarketService{ID: uuid.NewString(), OwnerID: actor, Status: "draft", CreatedAt: now}
			if err := tx.Create(&service).Error; err != nil {
				return err
			}
		} else {
			if err := lockForUpdate(tx).Where("id = ? AND owner_id = ?", serviceID, actor).First(&service).Error; err != nil {
				return err
			}
			if service.DraftVersionID != "" {
				var prior ToolMarketVersion
				if err := tx.First(&prior, "id = ?", service.DraftVersionID).Error; err != nil {
					return err
				}
				if prior.Status == "pending" {
					return ErrToolMarketConflict
				}
			}
		}
		users, _ := json.Marshal(in.AllowedUsers)
		version := ToolMarketVersion{ID: uuid.NewString(), ServiceID: service.ID, Status: "draft", Name: in.Name, Description: in.Description,
			ExecutionType: in.ExecutionType, Visibility: in.Visibility, Endpoint: in.Endpoint, AllowedUsers: string(users), Digest: marketDigest(in), CreatedAt: now}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		seenUsers := map[int]bool{}
		for _, id := range in.AllowedUsers {
			if seenUsers[id] {
				continue
			}
			seenUsers[id] = true
			if err := marketUser(tx, id, common.RoleCommonUser); err != nil {
				return ErrToolMarketInput
			}
			if err := tx.Create(&ToolMarketAccess{VersionID: version.ID, UserID: id}).Error; err != nil {
				return err
			}
		}
		for _, input := range in.Tools {
			var tool ToolMarketTool
			err := tx.Where("service_id = ? AND name = ?", service.ID, input.Name).First(&tool).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				tool = ToolMarketTool{ID: uuid.NewString(), ServiceID: service.ID, Name: input.Name}
				if err = tx.Create(&tool).Error; err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			permissions, _ := json.Marshal(input.Permissions)
			tv := ToolMarketToolVersion{VersionID: version.ID, ToolID: tool.ID, Name: tool.Name, Description: input.Description, InputSchema: string(input.InputSchema), OutputSchema: string(input.OutputSchema), Permissions: string(permissions), PriceQuota: input.PriceQuota}
			if err := tx.Create(&tv).Error; err != nil {
				return err
			}
		}
		service.DraftVersionID, service.UpdatedAt = version.ID, now
		if err := tx.Save(&service).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, service.ID, "draft.save", map[string]string{"version_id": version.ID, "digest": version.Digest})
	})
	return &service, err
}

func SubmitToolMarketDraft(actor int, serviceID, versionID string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var service ToolMarketService
		if err := lockForUpdate(tx).Where("id = ? AND owner_id = ?", serviceID, actor).First(&service).Error; err != nil {
			return err
		}
		if versionID == "" || service.DraftVersionID != versionID {
			return ErrToolMarketConflict
		}
		q := tx.Model(&ToolMarketVersion{}).Where("id = ? AND status IN ?", versionID, []string{"draft", "rejected"}).Update("status", "pending")
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected != 1 {
			return ErrToolMarketConflict
		}
		return marketEvent(tx, actor, serviceID, "draft.submit", map[string]string{"version_id": versionID})
	})
}

// ReviewToolMarketVersion cannot bypass validation. ValidationDigest must be
// recorded by a trusted executor validator for this exact immutable snapshot.
// No public endpoint may set ValidationDigest or mark an execution successful.
func ReviewToolMarketVersion(actor int, serviceID, versionID string, approve bool, note string) error {
	if strings.TrimSpace(note) == "" || len(note) > 1000 {
		return ErrToolMarketInput
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleAdminUser); err != nil {
			return err
		}
		var service ToolMarketService
		if err := lockForUpdate(tx).First(&service, "id = ?", serviceID).Error; err != nil {
			return err
		}
		if service.DraftVersionID != versionID {
			return ErrToolMarketConflict
		}
		var version ToolMarketVersion
		if err := tx.First(&version, "id = ? AND service_id = ? AND status = ?", versionID, serviceID, "pending").Error; err != nil {
			return err
		}
		version.Status, version.ReviewedBy, version.ReviewNote = "rejected", actor, note
		if approve {
			if version.ValidationDigest == "" || version.ValidationDigest != version.Digest {
				return ErrToolMarketDenied
			}
			version.Status, version.PublishedAt = "published", common.GetTimestamp()
			service.LiveVersionID, service.DraftVersionID = version.ID, ""
			if service.Status != "paused" && service.Status != "suspended" {
				service.Status = "published"
			}
			service.UpdatedAt = version.PublishedAt
			if err := tx.Save(&service).Error; err != nil {
				return err
			}
		}
		if err := tx.Save(&version).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, serviceID, "version.review", map[string]any{"version_id": versionID, "approve": approve, "note": note})
	})
}

func marketCanView(service ToolMarketService, version ToolMarketVersion, userID int) bool {
	if userID > 0 && service.OwnerID == userID {
		return true
	}
	if version.Visibility == "public" {
		return true
	}
	if userID <= 0 || version.Visibility != "shared" {
		return false
	}
	var users []int
	if json.Unmarshal([]byte(version.AllowedUsers), &users) != nil {
		return false
	}
	for _, id := range users {
		if id == userID {
			return true
		}
	}
	return false
}

func marketLiveTool(tx *gorm.DB, userID int, toolID, versionID string) (*ToolMarketService, *ToolMarketToolVersion, error) {
	var identity ToolMarketTool
	if err := tx.First(&identity, "id = ?", toolID).Error; err != nil {
		return nil, nil, err
	}
	var service ToolMarketService
	if err := tx.First(&service, "id = ? AND status = ? AND live_version_id = ?", identity.ServiceID, "published", versionID).Error; err != nil {
		return nil, nil, err
	}
	var version ToolMarketVersion
	if err := tx.First(&version, "id = ? AND service_id = ? AND status = ?", versionID, service.ID, "published").Error; err != nil {
		return nil, nil, err
	}
	if !marketCanView(service, version, userID) {
		return nil, nil, gorm.ErrRecordNotFound
	}
	var tool ToolMarketToolVersion
	if err := tx.First(&tool, "version_id = ? AND tool_id = ?", versionID, toolID).Error; err != nil {
		return nil, nil, err
	}
	return &service, &tool, nil
}
