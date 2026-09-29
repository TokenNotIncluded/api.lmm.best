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
	return getRedPacketBySlug(DB, slug)
}

// Only read-only history views may include a deleted packet. Claim and inventory
// mutations keep GORM's default scope so a tombstone can never be claimed again.
func getRedPacketBySlug(db *gorm.DB, slug string) (*RedPacket, error) {
	var packet RedPacket
	if err := db.Where("slug = ?", strings.TrimSpace(slug)).First(&packet).Error; err != nil {
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
	packet, err := getRedPacketBySlug(DB.Unscoped(), slug)
	if err != nil {
		return nil, err
	}
	total, remaining, claims, err := redPacketCounts(DB, packet.Id)
	if err != nil {
		return nil, err
	}
	// Keep the original share URL usable for recipients to retrieve their rewards,
	// but never advertise a deleted packet as claimable.
	if packet.DeletedAt.Valid {
		packet.Enabled = false
		remaining = 0
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
		// Claims and inventory replacement lock the packet first. Use the same
		// ordering so an uncommitted claim cannot look like unclaimed stock.
		var packet RedPacket
		if err := lockForUpdate(tx).First(&packet, "id = ?", packetID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil // Preserve idempotent deletion of an absent packet.
			}
			return err
		}
		var claimed, claims, remaining int64
		if err := tx.Model(&RedPacketItem{}).Where("packet_id = ? AND claimed_by <> 0", packetID).Count(&claimed).Error; err != nil {
			return err
		}
		if err := tx.Model(&RedPacketClaim{}).Where("packet_id = ?", packetID).Count(&claims).Error; err != nil {
			return err
		}
		if err := tx.Model(&RedPacketItem{}).Where("packet_id = ? AND claimed_by = 0", packetID).Count(&remaining).Error; err != nil {
			return err
		}
		hasHistory := claimed > 0 || claims > 0
		expired := packet.EndAt > 0 && common.GetTimestamp() >= packet.EndAt
		if hasHistory && packet.Enabled && !expired && remaining > 0 {
			return errors.New("已有领取记录且仍在进行的红包不可删除，请先停用")
		}
		// Release unused inventory bindings, not the underlying redemption/discount
		// codes. Claimed bindings and their uniqueness constraints remain intact.
		if err := tx.Where("packet_id = ? AND claimed_by = 0", packetID).
			Where("id NOT IN (?)", tx.Model(&RedPacketClaim{}).Select("item_id").Where("packet_id = ?", packetID)).
			Delete(&RedPacketItem{}).Error; err != nil {
			return err
		}
		if hasHistory {
			// Disabled also protects read paths that do not know about tombstones.
			if err := tx.Model(&packet).Updates(map[string]interface{}{
				"enabled": false, "updated_at": common.GetTimestamp(),
			}).Error; err != nil {
				return err
			}
			return tx.Delete(&packet).Error // Soft-delete: keep packet and claim audit.
		}
		return tx.Unscoped().Delete(&packet).Error
	})
}
