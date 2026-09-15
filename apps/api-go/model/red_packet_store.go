package model

import (
	"errors"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

func CreateRedPacket(packet *RedPacket, inputs []RedPacketItemInput) error {
	if packet == nil || len(inputs) == 0 {
		return ErrRedPacketInvalidItem
	}
	packet.Slug = strings.TrimSpace(packet.Slug)
	if packet.Slug == "" {
		packet.Slug = common.GetUUID()
	}
	packet.DrawMode = NormalizeRedPacketDrawMode(packet.DrawMode)
	if packet.PerUserLimit <= 0 {
		packet.PerUserLimit = 1
	}
	now := common.GetTimestamp()
	packet.CreatedAt = now
	packet.UpdatedAt = now

	return DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(packet).Error; err != nil {
			return err
		}
		return insertRedPacketItems(tx, packet.Id, inputs, now)
	})
}

func GetRedPacketBySlug(slug string) (*RedPacket, error) {
	var packet RedPacket
	if err := DB.Where("slug = ?", strings.TrimSpace(slug)).First(&packet).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRedPacketNotFound
		}
		return nil, err
	}
	return &packet, nil
}

func redPacketCounts(db *gorm.DB, packetID int) (total, remaining, claims int64, err error) {
	if err = db.Model(&RedPacketItem{}).Where("packet_id = ?", packetID).Count(&total).Error; err != nil {
		return
	}
	if err = db.Model(&RedPacketItem{}).Where("packet_id = ? AND claimed_by = 0", packetID).Count(&remaining).Error; err != nil {
		return
	}
	err = db.Model(&RedPacketClaim{}).Where("packet_id = ?", packetID).Count(&claims).Error
	return
}

func GetRedPacketPublic(slug string) (*RedPacketPublicView, error) {
	packet, err := GetRedPacketBySlug(slug)
	if err != nil {
		return nil, err
	}
	total, remaining, claims, err := redPacketCounts(DB, packet.Id)
	if err != nil {
		return nil, err
	}
	return &RedPacketPublicView{
		Slug:           packet.Slug,
		Title:          packet.Title,
		Description:    packet.Description,
		CoverImage:     packet.CoverImage,
		DrawMode:       packet.DrawMode,
		PerUserLimit:   packet.PerUserLimit,
		StartAt:        packet.StartAt,
		EndAt:          packet.EndAt,
		Enabled:        packet.Enabled,
		TotalItems:     total,
		RemainingItems: remaining,
		ClaimCount:     claims,
	}, nil
}

func ListRedPackets() ([]RedPacketAdminView, error) {
	var packets []RedPacket
	if err := DB.Order("id DESC").Find(&packets).Error; err != nil {
		return nil, err
	}
	result := make([]RedPacketAdminView, 0, len(packets))
	for _, packet := range packets {
		total, remaining, claims, err := redPacketCounts(DB, packet.Id)
		if err != nil {
			return nil, err
		}
		result = append(result, RedPacketAdminView{
			RedPacket:      packet,
			TotalItems:     total,
			RemainingItems: remaining,
			ClaimCount:     claims,
		})
	}
	return result, nil
}

func UpdateRedPacket(packetID int, patch RedPacket) error {
	if packetID <= 0 {
		return ErrRedPacketNotFound
	}
	patch.DrawMode = NormalizeRedPacketDrawMode(patch.DrawMode)
	if patch.PerUserLimit <= 0 {
		patch.PerUserLimit = 1
	}
	if patch.EndAt > 0 && patch.StartAt > 0 && patch.EndAt <= patch.StartAt {
		return errors.New("红包结束时间必须晚于开始时间")
	}
	return DB.Model(&RedPacket{}).Where("id = ?", packetID).Updates(map[string]interface{}{
		"title":          patch.Title,
		"description":    patch.Description,
		"cover_image":    patch.CoverImage,
		"cover_prompt":   patch.CoverPrompt,
		"draw_mode":      patch.DrawMode,
		"per_user_limit": patch.PerUserLimit,
		"start_at":       patch.StartAt,
		"end_at":         patch.EndAt,
		"enabled":        patch.Enabled,
		"updated_at":     common.GetTimestamp(),
	}).Error
}

func DeleteRedPacket(packetID int) error {
	if packetID <= 0 {
		return ErrRedPacketNotFound
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var claimed int64
		if err := tx.Model(&RedPacketItem{}).Where("packet_id = ? AND claimed_by <> 0", packetID).Count(&claimed).Error; err != nil {
			return err
		}
		if claimed > 0 {
			return errors.New("已有领取记录的红包不可删除，请停用以保留审计记录")
		}
		if err := tx.Where("packet_id = ?", packetID).Delete(&RedPacketItem{}).Error; err != nil {
			return err
		}
		return tx.Delete(&RedPacket{}, packetID).Error
	})
}
