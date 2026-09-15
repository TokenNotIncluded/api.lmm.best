package model

import (
	"errors"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// BeforeCreate prevents a live redemption or discount code from being placed
// into multiple packets through normal application writes. The database-level
// source unique index is the final concurrency boundary.
func (item *RedPacketItem) BeforeCreate(tx *gorm.DB) error {
	if item == nil || item.SourceId <= 0 {
		return ErrRedPacketInvalidItem
	}
	var count int64
	if err := tx.Model(&RedPacketItem{}).
		Where("item_type = ? AND source_id = ?", item.ItemType, item.SourceId).
		Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return ErrRedPacketItemDuplicated
	}
	return nil
}

func ReplaceRedPacketItems(packetID int, inputs []RedPacketItemInput) error {
	if packetID <= 0 || len(inputs) == 0 {
		return ErrRedPacketInvalidItem
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var packet RedPacket
		if err := lockForUpdate(tx).First(&packet, "id = ?", packetID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRedPacketNotFound
			}
			return err
		}
		var claims int64
		if err := tx.Model(&RedPacketClaim{}).Where("packet_id = ?", packetID).Count(&claims).Error; err != nil {
			return err
		}
		if claims > 0 {
			return errors.New("已有领取记录的红包不能替换奖励库存")
		}
		if err := tx.Where("packet_id = ?", packetID).Delete(&RedPacketItem{}).Error; err != nil {
			return err
		}
		return insertRedPacketItems(tx, packetID, inputs, common.GetTimestamp())
	})
}
