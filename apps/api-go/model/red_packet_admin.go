package model

import (
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

// BeforeCreate prevents a live redemption/discount code from being placed into
// multiple packets through normal application writes. The existing packet-local
// unique index remains a final duplicate guard inside one packet.
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

func ReplaceRedPacketItems(packetId int, inputs []RedPacketItemInput) error {
	if packetId <= 0 || len(inputs) == 0 {
		return ErrRedPacketInvalidItem
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var packet RedPacket
		if err := lockForUpdate(tx).First(&packet, "id = ?", packetId).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrRedPacketNotFound
			}
			return err
		}
		var claims int64
		if err := tx.Model(&RedPacketClaim{}).Where("packet_id = ?", packetId).Count(&claims).Error; err != nil {
			return err
		}
		if claims > 0 {
			return errors.New("已有领取记录的红包不能替换奖励库存")
		}
		if err := tx.Where("packet_id = ?", packetId).Delete(&RedPacketItem{}).Error; err != nil {
			return err
		}
		now := common.GetTimestamp()
		seen := make(map[string]struct{}, len(inputs))
		for _, input := range inputs {
			input.ItemType = strings.ToLower(strings.TrimSpace(input.ItemType))
			if input.SourceId <= 0 || (input.ItemType != RedPacketItemRedemption && input.ItemType != RedPacketItemDiscount) {
				return ErrRedPacketInvalidItem
			}
			key := input.ItemType + ":" + string(rune(input.SourceId))
			if _, exists := seen[key]; exists {
				return ErrRedPacketItemDuplicated
			}
			seen[key] = struct{}{}
			if input.Weight <= 0 {
				input.Weight = 1
			}
			if err := validateRedPacketSource(tx, input.ItemType, input.SourceId, now); err != nil {
				return err
			}
			item := RedPacketItem{
				PacketId: packetId, ItemType: input.ItemType, SourceId: input.SourceId,
				Weight: input.Weight, CreatedAt: now,
			}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
