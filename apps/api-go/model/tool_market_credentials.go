package model

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var ErrToolMarketCredentialUnavailable = errors.New("tool market credential encryption is unavailable")

// Credentials are separate from public version snapshots and their digests.
// Even accidental serialization of this persistence model exposes no fields.
type ToolMarketCredential struct {
	ID             string `json:"-" gorm:"primaryKey;size:36"`
	OwnerID        int    `json:"-" gorm:"not null;index"`
	ServiceID      string `json:"-" gorm:"size:36;not null;index"`
	VersionID      string `json:"-" gorm:"size:36;not null;uniqueIndex"`
	EndpointDigest string `json:"-" gorm:"size:64;not null"`
	Mode           string `json:"-" gorm:"size:16;not null"`
	Ciphertext     string `json:"-" gorm:"type:text;not null"`
	UpdatedAt      int64  `json:"-"`
}

type ToolMarketCredentialMetadata struct {
	Mode       string `json:"mode"`
	Configured bool   `json:"configured"`
	UpdatedAt  int64  `json:"updated_at"`
}

// Resolved credentials belong only to the trusted remote execution adapter.
type ToolMarketResolvedCredential struct {
	ID     string `json:"-"`
	Mode   string `json:"-"`
	Secret string `json:"-"`
}

func ValidateToolMarketCredential(mode, secret string) error {
	if mode == "none" {
		if secret != "" {
			return ErrToolMarketInput
		}
		return nil
	}
	if (mode != "bearer" && mode != "api_key") || len(secret) == 0 || len(secret) > 4096 || !utf8.ValidString(secret) || strings.TrimSpace(secret) == "" {
		return ErrToolMarketInput
	}
	for _, character := range secret {
		if unicode.IsControl(character) || (mode == "bearer" && unicode.IsSpace(character)) {
			return ErrToolMarketInput
		}
	}
	// These are LMM login/client credentials, never third-party service keys.
	for _, prefix := range []string{"lmm_at_", "lmm_rt_", "lmm_market_", "lmm_mcp_", "lmm_drawing_mcp_", "lmm_bounty_mcp_"} {
		if strings.HasPrefix(strings.TrimSpace(secret), prefix) {
			return ErrToolMarketInput
		}
	}
	return nil
}

func marketCredentialEndpointDigest(endpoint string) (string, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Port() != "" && parsed.Port() != "443") {
		return "", ErrToolMarketInput
	}
	// Bind the exact path and URL spelling accepted for this immutable version.
	return marketDigest(endpoint), nil
}

func marketCredentialPurpose(row ToolMarketCredential) string {
	return fmt.Sprintf("tool_market.credential:%s:%d:%s:%s:%s:%s", row.ID, row.OwnerID, row.ServiceID, row.VersionID, row.EndpointDigest, row.Mode)
}

func marketCredentialDB(db *gorm.DB) *gorm.DB {
	// Errors from SQL writers/readers must never dump credential parameters.
	return db.Session(&gorm.Session{Logger: db.Logger.LogMode(logger.Silent)})
}

func marketCredentialVersion(db *gorm.DB, ownerID int, serviceID, versionID, endpoint string) (*ToolMarketVersion, string, error) {
	var service ToolMarketService
	if err := db.First(&service, "id = ?", serviceID).Error; err != nil {
		return nil, "", err
	}
	if ownerID <= 0 || service.OwnerID != ownerID {
		return nil, "", ErrToolMarketDenied
	}
	var version ToolMarketVersion
	if err := db.First(&version, "id = ? AND service_id = ?", versionID, serviceID).Error; err != nil {
		return nil, "", err
	}
	if version.ExecutionType != "remote" || (endpoint != "" && version.Endpoint != endpoint) {
		return nil, "", ErrToolMarketConflict
	}
	digest, err := marketCredentialEndpointDigest(version.Endpoint)
	return &version, digest, err
}

func marketResolveCredential(db *gorm.DB, ownerID int, serviceID, versionID, endpoint string) (*ToolMarketResolvedCredential, error) {
	_, digest, err := marketCredentialVersion(db, ownerID, serviceID, versionID, endpoint)
	if err != nil {
		return nil, err
	}
	var row ToolMarketCredential
	err = db.First(&row, "version_id = ?", versionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &ToolMarketResolvedCredential{Mode: "none"}, nil
	}
	if err != nil {
		return nil, err
	}
	if row.ID == "" || row.OwnerID != ownerID || row.ServiceID != serviceID || row.VersionID != versionID || row.EndpointDigest != digest || (row.Mode != "bearer" && row.Mode != "api_key") || row.Ciphertext == "" {
		return nil, ErrToolMarketConflict
	}
	secret, err := common.DecryptPersistentString(marketCredentialPurpose(row), "TOOL_MARKET_ENCRYPTION_KEY", "CRYPTO_SECRET", row.Ciphertext)
	if err != nil || ValidateToolMarketCredential(row.Mode, secret) != nil {
		return nil, ErrToolMarketCredentialUnavailable
	}
	return &ToolMarketResolvedCredential{ID: row.ID, Mode: row.Mode, Secret: secret}, nil
}

func ResolveToolMarketCredential(ownerID int, serviceID, versionID, endpoint string) (*ToolMarketResolvedCredential, error) {
	if endpoint == "" {
		return nil, ErrToolMarketInput
	}
	db := marketCredentialDB(DB)
	if err := marketUser(db, ownerID, common.RoleCommonUser); err != nil {
		return nil, err
	}
	return marketResolveCredential(db, ownerID, serviceID, versionID, endpoint)
}

func GetToolMarketCredentialMetadata(actor int, serviceID, versionID string) (*ToolMarketCredentialMetadata, error) {
	db := marketCredentialDB(DB)
	if err := marketUser(db, actor, common.RoleCommonUser); err != nil {
		return nil, err
	}
	_, digest, err := marketCredentialVersion(db, actor, serviceID, versionID, "")
	if err != nil {
		return nil, err
	}
	var row ToolMarketCredential
	err = db.First(&row, "version_id = ?", versionID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &ToolMarketCredentialMetadata{Mode: "none"}, nil
	}
	if err != nil {
		return nil, err
	}
	if row.OwnerID != actor || row.ServiceID != serviceID || row.VersionID != versionID || row.EndpointDigest != digest || (row.Mode != "bearer" && row.Mode != "api_key") || row.Ciphertext == "" {
		return nil, ErrToolMarketConflict
	}
	return &ToolMarketCredentialMetadata{Mode: row.Mode, Configured: true, UpdatedAt: row.UpdatedAt}, nil
}

// ConfigureToolMarketCredential changes only the owner's current mutable draft.
// Copying explicitly re-encrypts a prior version's secret for the new binding.
func ConfigureToolMarketCredential(actor int, serviceID, versionID, mode, secret, copyFromVersionID string) error {
	if versionID == "" || serviceID == "" || (copyFromVersionID != "" && (secret != "" || copyFromVersionID == versionID || mode == "none")) {
		return ErrToolMarketInput
	}
	if copyFromVersionID == "" && ValidateToolMarketCredential(mode, secret) != nil {
		return ErrToolMarketInput
	}
	return marketTransaction(marketCredentialDB(DB), func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var service ToolMarketService
		if err := lockForUpdate(tx).First(&service, "id = ? AND status <> ? AND COALESCE(draft_version_id, '') <> ?", serviceID, ToolMarketServiceDeleted, toolMarketRetirementVersionID).Error; err != nil {
			return err
		}
		if service.OwnerID != actor {
			return ErrToolMarketDenied
		}
		if service.DraftVersionID != versionID {
			return ErrToolMarketConflict
		}
		version, digest, err := marketCredentialVersion(tx, actor, serviceID, versionID, "")
		if err != nil {
			return err
		}
		if version.Status != "draft" {
			return ErrToolMarketConflict
		}
		if copyFromVersionID != "" {
			prior, err := marketResolveCredential(tx, actor, serviceID, copyFromVersionID, version.Endpoint)
			if err != nil {
				return err
			}
			if prior.Mode == "none" || (mode != "" && mode != prior.Mode) {
				return ErrToolMarketInput
			}
			mode, secret = prior.Mode, prior.Secret
		}
		row := ToolMarketCredential{ID: uuid.NewString(), OwnerID: actor, ServiceID: serviceID, VersionID: versionID, EndpointDigest: digest, Mode: mode, UpdatedAt: common.GetTimestamp()}
		if mode != "none" {
			row.Ciphertext, err = common.EncryptPersistentString(marketCredentialPurpose(row), "TOOL_MARKET_ENCRYPTION_KEY", "CRYPTO_SECRET", secret)
			if err != nil {
				return ErrToolMarketCredentialUnavailable
			}
		}
		if err := tx.Where("owner_id = ? AND service_id = ? AND version_id = ?", actor, serviceID, versionID).Delete(&ToolMarketCredential{}).Error; err != nil {
			return err
		}
		if mode != "none" {
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&ToolMarketVersion{}).Where("id = ? AND service_id = ?", versionID, serviceID).Update("validation_digest", "").Error; err != nil {
			return err
		}
		if err := tx.Model(&ToolMarketToolVersion{}).Where("version_id = ?", versionID).Update("remote_digest", "").Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, serviceID, "credential.configure", map[string]any{"version_id": versionID, "mode": mode, "configured": mode != "none", "updated_at": row.UpdatedAt})
	})
}
