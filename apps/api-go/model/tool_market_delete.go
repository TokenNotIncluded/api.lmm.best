package model

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

const ToolMarketServiceDeleted = "deleted"

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
		if service.Status == ToolMarketServiceDeleted {
			return nil
		}
		before := service.Status
		if err := tx.Model(&service).Updates(map[string]any{"status": ToolMarketServiceDeleted, "updated_at": common.GetTimestamp()}).Error; err != nil {
			return err
		}
		return marketEvent(tx, actor, serviceID, "service.delete", map[string]string{
			"before_status": before, "live_version_id": service.LiveVersionID, "draft_version_id": service.DraftVersionID,
		})
	})
}
