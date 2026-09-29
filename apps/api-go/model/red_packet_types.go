package model

import (
	"errors"
	"fmt"
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

// RedPacketCoverImage accepts either an HTTP(S) URL or a data:image/* URL.
// MySQL needs LONGTEXT for uploaded/generated data URLs while PostgreSQL and
// SQLite can use TEXT.
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
	DeletedAt    gorm.DeletedAt      `json:"-" gorm:"index"`
}

func (RedPacket) TableName() string { return "red_packets" }

type RedPacketItem struct {
	Id        int    `json:"id" gorm:"primaryKey;autoIncrement"`
	PacketId  int    `json:"packet_id" gorm:"not null;index"`
	ItemType  string `json:"item_type" gorm:"type:varchar(16);not null;index;uniqueIndex:idx_red_packet_source,priority:1"`
	SourceId  int    `json:"source_id" gorm:"not null;index;uniqueIndex:idx_red_packet_source,priority:2"`
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
	ClaimId               int    `json:"claim_id"`
	ItemType              string `json:"item_type"`
	Code                  string `json:"code"`
	Name                  string `json:"name"`
	ClaimedAt             int64  `json:"claimed_at"`
	RewardType            string `json:"reward_type,omitempty"`
	Quota                 int    `json:"quota,omitempty"`
	ResetPlanId           int    `json:"reset_plan_id,omitempty"`
	ResetVoucherExpiresAt int64  `json:"reset_voucher_expires_at,omitempty"`
	DiscountPercent       int    `json:"discount_percent,omitempty"`
	MinAmount             int64  `json:"min_amount,omitempty"`
	ExpiresAt             int64  `json:"expires_at,omitempty"`
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

func insertRedPacketItems(tx *gorm.DB, packetID int, inputs []RedPacketItemInput, now int64) error {
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
			PacketId:  packetID,
			ItemType:  input.ItemType,
			SourceId:  input.SourceId,
			Weight:    input.Weight,
			CreatedAt: now,
		}
		if err := tx.Create(&item).Error; err != nil {
			if errors.Is(err, ErrRedPacketItemDuplicated) {
				return err
			}
			return ErrRedPacketItemDuplicated
		}
	}
	return nil
}

func validateRedPacketSource(tx *gorm.DB, itemType string, sourceID int, now int64) error {
	switch itemType {
	case RedPacketItemRedemption:
		var row Redemption
		if err := tx.First(&row, "id = ?", sourceID).Error; err != nil {
			return ErrRedPacketInvalidItem
		}
		if row.Status != common.RedemptionCodeStatusEnabled || (row.ExpiredTime > 0 && row.ExpiredTime < now) {
			return ErrRedPacketInvalidItem
		}
	case RedPacketItemDiscount:
		var row DiscountCode
		if err := tx.First(&row, "id = ?", sourceID).Error; err != nil {
			return ErrRedPacketInvalidItem
		}
		if row.OwnerUserID != 0 || row.Status != DiscountCodeStatusEnabled ||
			(row.StartsTime > 0 && row.StartsTime > now) ||
			(row.ExpiredTime > 0 && row.ExpiredTime < now) ||
			(row.MaxUses > 0 && row.UsedCount >= row.MaxUses) {
			return ErrRedPacketInvalidItem
		}
	default:
		return ErrRedPacketInvalidItem
	}
	return nil
}
