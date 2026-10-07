package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const ToolMarketServiceDeleted = "deleted"

// A reserved UUIDv5 in the existing draft pointer permanently retires a service.
// Merchant-created versions use UUIDv4. Older writers reject this non-resolving
// draft and the empty live pointer, even if an old report changes the status.
// This is a retirement guard, never an actual draft; no version/ledger is deleted.
const toolMarketRetirementVersionID = "5ccac1a6-3c42-5e56-9ab3-47c234d77a2e"

func marketServiceRetired(service ToolMarketService) bool {
	return service.Status == ToolMarketServiceDeleted || service.DraftVersionID == toolMarketRetirementVersionID
}

// DeleteToolMarketService retires discovery and future calls under the same
// service lock used by reservations and dispatch. Immutable versions, grants,
// calls, results and financial/review history remain available to their owners.
func DeleteToolMarketService(actor int, serviceID string) error {
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
		if marketServiceRetired(service) {
			return nil
		}
		before, live, draft := service.Status, service.LiveVersionID, service.DraftVersionID
		if err := tx.Model(&service).Updates(map[string]any{"status": ToolMarketServiceDeleted, "live_version_id": "", "draft_version_id": toolMarketRetirementVersionID, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, serviceID, "service.delete", map[string]string{
			"before_status": before, "live_version_id": live, "draft_version_id": draft,
		})
	})
}
