package model

import (
	cryptorand "crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const (
	RedPacketDrawRandom   = "random"
	RedPacketDrawWeighted = "weighted"
	RedPacketDrawSequence = "sequence"

	RedPacketItemRedemption = "redemption"
	RedPacketItemDiscount   = "discount"
)

var (
	ErrRedPacketNotFound       = errors.New("红包不存在")
	ErrRedPacketNotStarted     = errors.New("红包尚未开始")
	ErrRedPacketExpired        = errors.New("红包已结束")
	ErrRedPacketExhausted      = errors.New("红包已经领完")
	ErrRedPacketClaimLimit     = errors.New("已达到该红包的领取次数上限")
	ErrRedPacketInvalidItem    = errors.New("红包包含无效奖励")
	ErrRedPacketItemDuplicated = errors.New("奖励已被其他红包占用")
)

// RedPacketCoverImage accepts either an https URL or a data:image/* URL. MySQL
// needs LONGTEXT for uploaded/generated data URLs while PostgreSQL/SQLite use TEXT.
type RedPacketCoverImage string

func (RedPacketCoverImage) GormDataType() string { return "text" }

func (RedPacketCoverImage) GormDBDataType(db *gorm.DB, _ *schema.Field) string {
	if db != nil && db.Dialector != nil && db.Dialector.Name() == "mysql" {
		return "LONGTEXT"
	}
	return "TEXT"
}

type RedPacket struct {
	Id           int                 `json:"id" gorm:"primaryKey;autoIncrement"`
	Slug         string              `json:"slug" gorm:"type:varchar(64);not null;uniqueIndex"`
	Title        string              `json:"title" gorm:"type:varchar(80);not null"`
	Description  string              `json:"description" gorm:"type:varchar(500);not null;default:''"`
	CoverImage   RedPacketCoverImage `json:"cover_image"`
	CoverPrompt  string              `json:"cover_prompt" gorm:"type:varchar(1000);not null;default:''"`
	DrawMode     string              `json:"draw_mode" gorm:"type:varchar(16);not null;default:'random'"`
	PerUserLimit int                 `json:"per_user_limit" gorm:"not null;default:1"`
	StartAt      int64               `json:"start_at" gorm:"bigint;not null;default:0;index"`
	EndAt        int64               `json:"end_at" gorm:"bigint;not null;default:0;index"`
	Enabled      bool                `json:"enabled" gorm:"not null;default:true;index"`
	CreatedBy    int                 `json:"created_by" gorm:"not null;index"`
	CreatedAt    int64               `json:"created_at" gorm:"bigint;not null"`
	UpdatedAt    int64               `json:"updated_at" gorm:"bigint;not null"`
}

func (RedPacket) TableName() string { return "red_packets" }

type RedPacketItem struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	PacketId  int    `json:"packet_id" gorm:"not null;index;uniqueIndex:idx_red_packet_source,priority:1"`
	ItemType  string `json:"item_type" gorm:"type:varchar(16);not null;index;uniqueIndex:idx_red_packet_source,priority:2"`
	SourceId  int    `json:"source_id" gorm:"not null;index;uniqueIndex:idx_red_packet_source,priority:3"`
	Weight    int    `json:"weight" gorm:"not null;default:1"`
	ClaimedBy int    `json:"claimed_by" gorm:"not null;default:0;index"`
	ClaimedAt int64  `json:"claimed_at" gorm:"bigint;not null;default:0"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;not null"`
}

func (RedPacketItem) TableName() string { return "red_packet_items" }

type RedPacketClaim struct {
	Id         int    `json:"id" gorm:"primaryKey;autoIncrement"`
	PacketId   int    `json:"packet_id" gorm:"not null;index;uniqueIndex:idx_red_packet_user_claim,priority:1"`
	UserId     int    `json:"user_id" gorm:"not null;index;uniqueIndex:idx_red_packet_user_claim,priority:2"`
	ClaimIndex int    `json:"claim_index" gorm:"not null;uniqueIndex:idx_red_packet_user_claim,priority:3"`
	ItemId     int    `json:"item_id" gorm:"not null;uniqueIndex"`
	ItemType   string `json:"item_type" gorm:"type:varchar(16);not null"`
	SourceId   int    `json:"source_id" gorm:"not null"`
	CreatedAt  int64  `json:"created_at" gorm:"bigint;not null;index"`
}

func (RedPacketClaim) TableName() string { return "red_packet_claims" }

type RedPacketItemInput struct {
	ItemType string `json:"item_type"`
	SourceId int    `json:"source_id"`
	Weight   int    `json:"weight"`
}

type RedPacketAdminView struct {
	RedPacket
	TotalItems     int64 `json:"total_items"`
	RemainingItems int64 `json:"remaining_items"`
	ClaimCount     int64 `json:"claim_count"`
}

type RedPacketPublicView struct {
	Slug           string              `json:"slug"`
	Title          string              `json:"title"`
	Description    string              `json:"description"`
	CoverImage     RedPacketCoverImage `json:"cover_image"`
	DrawMode       string              `json:"draw_mode"`
	PerUserLimit   int                 `json:"per_user_limit"`
	StartAt        int64               `json:"start_at"`
	EndAt          int64               `json:"end_at"`
	Enabled        bool                `json:"enabled"`
	TotalItems     int64               `json:"total_items"`
	RemainingItems int64               `json:"remaining_items"`
	ClaimCount     int64               `json:"claim_count"`
}

type RedPacketReward struct {
	ClaimId    int    `json:"claim_id"`
	ItemType   string `json:"item_type"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	ClaimedAt  int64  `json:"claimed_at"`
	RewardType string `json:"reward_type,omitempty"`
	Quota      int    `json:"quota,omitempty"`
	ResetPlanId int   `json:"reset_plan_id,omitempty"`
	ResetVoucherExpiresAt int64 `json:"reset_voucher_expires_at,omitempty"`
	DiscountPercent int   `json:"discount_percent,omitempty"`
	MinAmount       int64 `json:"min_amount,omitempty"`
	ExpiresAt       int64 `json:"expires_at,omitempty"`
}

func NormalizeRedPacketDrawMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case RedPacketDrawRandom, RedPacketDrawWeighted, RedPacketDrawSequence:
		return value
	default:
		return RedPacketDrawRandom
	}
}

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
		seen := make(map[string]struct{}, len(inputs))
		for _, input := range inputs {
			input.ItemType = strings.ToLower(strings.TrimSpace(input.ItemType))
			if input.SourceId <= 0 || (input.ItemType != RedPacketItemRedemption && input.ItemType != RedPacketItemDiscount) {
				return ErrRedPacketInvalidItem
			}
			key := fmt.Sprintf("%s:%d", input.ItemType, input.SourceId)
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
				PacketId: packet.Id,
				ItemType: input.ItemType,
				SourceId: input.SourceId,
				Weight: input.Weight,
				CreatedAt: now,
			}
			if err := tx.Create(&item).Error; err != nil {
				return ErrRedPacketItemDuplicated
			}
		}
		return nil
	})
}

func validateRedPacketSource(tx *gorm.DB, itemType string, sourceId int, now int64) error {
	switch itemType {
	case RedPacketItemRedemption:
		var row Redemption
		if err := tx.First(&row, "id = ?", sourceId).Error; err != nil {
			return ErrRedPacketInvalidItem
		}
		if row.Status != common.RedemptionCodeStatusEnabled || (row.ExpiredTime > 0 && row.ExpiredTime < now) {
			return ErrRedPacketInvalidItem
		}
	case RedPacketItemDiscount:
		var row DiscountCode
		if err := tx.First(&row, "id = ?", sourceId).Error; err != nil {
			return ErrRedPacketInvalidItem
		}
		if row.OwnerUserID != 0 || row.Status != DiscountCodeStatusEnabled ||
			(row.StartsTime > 0 && row.StartsTime > now) || (row.ExpiredTime > 0 && row.ExpiredTime < now) ||
			(row.MaxUses > 0 && row.UsedCount >= row.MaxUses) {
			return ErrRedPacketInvalidItem
		}
	default:
		return ErrRedPacketInvalidItem
	}
	return nil
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

func redPacketCounts(db *gorm.DB, packetId int) (total, remaining, claims int64, err error) {
	if err = db.Model(&RedPacketItem{}).Where("packet_id = ?", packetId).Count(&total).Error; err != nil {
		return
	}
	if err = db.Model(&RedPacketItem{}).Where("packet_id = ? AND claimed_by = 0", packetId).Count(&remaining).Error; err != nil {
		return
	}
	err = db.Model(&RedPacketClaim{}).Where("packet_id = ?", packetId).Count(&claims).Error
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
		Slug: packet.Slug, Title: packet.Title, Description: packet.Description,
		CoverImage: packet.CoverImage, DrawMode: packet.DrawMode, PerUserLimit: packet.PerUserLimit,
		StartAt: packet.StartAt, EndAt: packet.EndAt, Enabled: packet.Enabled,
		TotalItems: total, RemainingItems: remaining, ClaimCount: claims,
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
		result = append(result, RedPacketAdminView{RedPacket: packet, TotalItems: total, RemainingItems: remaining, ClaimCount: claims})
	}
	return result, nil
}

func redPacketIsActive(packet *RedPacket, now int64) error {
	if packet == nil || !packet.Enabled {
		return ErrRedPacketNotFound
	}
	if packet.StartAt > 0 && now < packet.StartAt {
		return ErrRedPacketNotStarted
	}
	if packet.EndAt > 0 && now >= packet.EndAt {
		return ErrRedPacketExpired
	}
	return nil
}

func selectRedPacketItem(tx *gorm.DB, packet *RedPacket, now int64) (*RedPacketItem, error) {
	var items []RedPacketItem
	if err := tx.Where("packet_id = ? AND claimed_by = 0", packet.Id).Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	valid := make([]RedPacketItem, 0, len(items))
	for _, item := range items {
		if validateRedPacketSource(tx, item.ItemType, item.SourceId, now) == nil {
			valid = append(valid, item)
		}
	}
	if len(valid) == 0 {
		return nil, ErrRedPacketExhausted
	}
	if packet.DrawMode == RedPacketDrawSequence {
		return &valid[0], nil
	}
	if packet.DrawMode != RedPacketDrawWeighted {
		index, err := cryptorand.Int(cryptorand.Reader, big.NewInt(int64(len(valid))))
		if err != nil {
			return nil, err
		}
		return &valid[index.Int64()], nil
	}
	var totalWeight int64
	for _, item := range valid {
		weight := item.Weight
		if weight <= 0 {
			weight = 1
		}
		totalWeight += int64(weight)
	}
	pick, err := cryptorand.Int(cryptorand.Reader, big.NewInt(totalWeight))
	if err != nil {
		return nil, err
	}
	cursor := pick.Int64()
	for index := range valid {
		weight := valid[index].Weight
		if weight <= 0 {
			weight = 1
		}
		if cursor < int64(weight) {
			return &valid[index], nil
		}
		cursor -= int64(weight)
	}
	return &valid[len(valid)-1], nil
}

func redPacketRewardForSource(tx *gorm.DB, claim *RedPacketClaim) (*RedPacketReward, error) {
	reward := &RedPacketReward{ClaimId: claim.Id, ItemType: claim.ItemType, ClaimedAt: claim.CreatedAt}
	switch claim.ItemType {
	case RedPacketItemRedemption:
		var row Redemption
		if err := tx.First(&row, "id = ?", claim.SourceId).Error; err != nil {
			return nil, err
		}
		reward.Code = row.Key
		reward.Name = row.Name
		reward.RewardType = row.RewardType
		reward.Quota = row.Quota
		reward.ResetPlanId = row.ResetPlanId
		reward.ResetVoucherExpiresAt = row.ResetVoucherExpiresAt
	case RedPacketItemDiscount:
		var row DiscountCode
		if err := tx.First(&row, "id = ?", claim.SourceId).Error; err != nil {
			return nil, err
		}
		reward.Code = row.Code
		reward.Name = row.Name
		reward.DiscountPercent = row.DiscountPercent
		reward.MinAmount = row.MinAmount
		reward.ExpiresAt = row.ExpiredTime
	default:
		return nil, ErrRedPacketInvalidItem
	}
	return reward, nil
}

func ClaimRedPacket(slug string, userId int) (*RedPacketReward, error) {
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	var reward *RedPacketReward
	err := DB.Transaction(func(tx *gorm.DB) error {
		var packet RedPacket
		if err := lockForUpdate(tx).Where("slug = ?", strings.TrimSpace(slug)).First(&packet).Error; err != nil {
			return ErrRedPacketNotFound
		}
		now := common.GetTimestamp()
		if err := redPacketIsActive(&packet, now); err != nil {
			return err
		}
		var claimCount int64
		if err := tx.Model(&RedPacketClaim{}).Where("packet_id = ? AND user_id = ?", packet.Id, userId).Count(&claimCount).Error; err != nil {
			return err
		}
		if claimCount >= int64(packet.PerUserLimit) {
			return ErrRedPacketClaimLimit
		}
		item, err := selectRedPacketItem(tx, &packet, now)
		if err != nil {
			return err
		}
		updated := tx.Model(&RedPacketItem{}).
			Where("id = ? AND claimed_by = 0", item.Id).
			Updates(map[string]interface{}{"claimed_by": userId, "claimed_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrRedPacketExhausted
		}
		if item.ItemType == RedPacketItemDiscount {
			owned := tx.Model(&DiscountCode{}).
				Where("id = ? AND owner_user_id = 0", item.SourceId).
				Update("owner_user_id", userId)
			if owned.Error != nil {
				return owned.Error
			}
			if owned.RowsAffected != 1 {
				return ErrRedPacketInvalidItem
			}
		}
		claim := RedPacketClaim{
			PacketId: packet.Id, UserId: userId, ClaimIndex: int(claimCount) + 1,
			ItemId: item.Id, ItemType: item.ItemType, SourceId: item.SourceId, CreatedAt: now,
		}
		if err := tx.Create(&claim).Error; err != nil {
			return err
		}
		reward, err = redPacketRewardForSource(tx, &claim)
		return err
	})
	return reward, err
}

func ListUserRedPacketClaims(slug string, userId int) ([]RedPacketReward, error) {
	packet, err := GetRedPacketBySlug(slug)
	if err != nil {
		return nil, err
	}
	var claims []RedPacketClaim
	if err := DB.Where("packet_id = ? AND user_id = ?", packet.Id, userId).Order("claim_index ASC").Find(&claims).Error; err != nil {
		return nil, err
	}
	result := make([]RedPacketReward, 0, len(claims))
	for index := range claims {
		reward, err := redPacketRewardForSource(DB, &claims[index])
		if err != nil {
			return nil, err
		}
		result = append(result, *reward)
	}
	return result, nil
}

func UpdateRedPacket(packetId int, patch RedPacket) error {
	if packetId <= 0 {
		return ErrRedPacketNotFound
	}
	patch.DrawMode = NormalizeRedPacketDrawMode(patch.DrawMode)
	if patch.PerUserLimit <= 0 {
		patch.PerUserLimit = 1
	}
	if patch.EndAt > 0 && patch.StartAt > 0 && patch.EndAt <= patch.StartAt {
		return errors.New("红包结束时间必须晚于开始时间")
	}
	return DB.Model(&RedPacket{}).Where("id = ?", packetId).Updates(map[string]interface{}{
		"title": patch.Title, "description": patch.Description, "cover_image": patch.CoverImage,
		"cover_prompt": patch.CoverPrompt, "draw_mode": patch.DrawMode, "per_user_limit": patch.PerUserLimit,
		"start_at": patch.StartAt, "end_at": patch.EndAt, "enabled": patch.Enabled, "updated_at": common.GetTimestamp(),
	}).Error
}

func DeleteRedPacket(packetId int) error {
	if packetId <= 0 {
		return ErrRedPacketNotFound
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var claimed int64
		if err := tx.Model(&RedPacketItem{}).Where("packet_id = ? AND claimed_by <> 0", packetId).Count(&claimed).Error; err != nil {
			return err
		}
		if claimed > 0 {
			return errors.New("已有领取记录的红包不可删除，请停用以保留审计记录")
		}
		if err := tx.Where("packet_id = ?", packetId).Delete(&RedPacketItem{}).Error; err != nil {
			return err
		}
		return tx.Delete(&RedPacket{}, packetId).Error
	})
}
