package model

import (
	"encoding/hex"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// ActivateToolMarketPrivate publishes a validated free remote service for its
// owner. Public, shared and paid services still require administrator review.
// The trusted remote validator must run before this transaction; the author
// cannot supply either the validation digest or the remote tool fingerprints.
func ActivateToolMarketPrivate(actor int, serviceID, versionID string) error {
	if serviceID == "" || versionID == "" {
		return ErrToolMarketInput
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := marketUser(tx, actor, common.RoleCommonUser); err != nil {
			return err
		}
		var service ToolMarketService
		// Publication and dispatch serialize on the service row, as do normal
		// review and suspension. No account lock is acquired after this lock.
		if err := lockForUpdate(tx).First(&service, "id = ?", serviceID).Error; err != nil {
			return err
		}
		if service.OwnerID != actor {
			return ErrToolMarketDenied
		}
		if service.Status == "paused" || service.Status == "suspended" {
			return ErrToolMarketDenied
		}
		if service.Status != "draft" && service.Status != "published" {
			return ErrToolMarketConflict
		}
		var version ToolMarketVersion
		if err := tx.First(&version, "id = ? AND service_id = ?", versionID, serviceID).Error; err != nil {
			return err
		}
		alreadyActive := service.Status == "published" && service.LiveVersionID == versionID && version.Status == "published"
		if !alreadyActive && (service.DraftVersionID != versionID || version.Status != "draft") {
			return ErrToolMarketConflict
		}
		if version.ExecutionType != "remote" || version.Visibility != "private" || version.ValidationDigest == "" || version.ValidationDigest != version.Digest {
			return ErrToolMarketDenied
		}
		var tools []ToolMarketToolVersion
		if err := tx.Where("version_id = ?", versionID).Find(&tools).Error; err != nil {
			return err
		}
		if len(tools) == 0 {
			return ErrToolMarketDenied
		}
		for _, tool := range tools {
			if tool.PriceQuota != 0 || len(tool.RemoteDigest) != 64 {
				return ErrToolMarketDenied
			}
			if _, err := hex.DecodeString(tool.RemoteDigest); err != nil {
				return ErrToolMarketDenied
			}
		}
		if alreadyActive {
			return nil
		}
		now := common.GetTimestamp()
		version.Status, version.PublishedAt = "published", now
		if err := tx.Save(&version).Error; err != nil {
			return err
		}
		service.Status, service.LiveVersionID, service.DraftVersionID, service.UpdatedAt = "published", version.ID, "", now
		if err := tx.Save(&service).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, service.ID, "private.activate", map[string]string{"version_id": version.ID})
	})
}
