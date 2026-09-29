package model

import (
	cryptorand "crypto/rand"
	"errors"
	"math/big"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"gorm.io/gorm"
)

func redPacketIsActive(packet *RedPacket, now int64) error {
	if packet == nil || packet.DeletedAt.Valid || !packet.Enabled {
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
	reward := &RedPacketReward{
		ClaimId:   claim.Id,
		ItemType:  claim.ItemType,
		ClaimedAt: claim.CreatedAt,
	}
	switch claim.ItemType {
	case RedPacketItemRedemption:
		var row Redemption
		if err := tx.First(&row, "id = ?", claim.SourceId).Error; err != nil {
			return nil, err
		}
		reward.Code = row.Key
		reward.Name = row.Name
		reward.RewardType = NormalizeRedemptionRewardType(row.RewardType)
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

func ClaimRedPacket(slug string, userID int) (*RedPacketReward, error) {
	if userID <= 0 {
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
		if err := tx.Model(&RedPacketClaim{}).Where("packet_id = ? AND user_id = ?", packet.Id, userID).Count(&claimCount).Error; err != nil {
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
			Updates(map[string]interface{}{"claimed_by": userID, "claimed_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrRedPacketExhausted
		}

		if item.ItemType == RedPacketItemDiscount {
			owned := tx.Model(&DiscountCode{}).
				Where("id = ? AND owner_user_id = 0", item.SourceId).
				Update("owner_user_id", userID)
			if owned.Error != nil {
				return owned.Error
			}
			if owned.RowsAffected != 1 {
				return ErrRedPacketInvalidItem
			}
		}

		claim := RedPacketClaim{
			PacketId:   packet.Id,
			UserId:     userID,
			ClaimIndex: int(claimCount) + 1,
			ItemId:     item.Id,
			ItemType:   item.ItemType,
			SourceId:   item.SourceId,
			CreatedAt:  now,
		}
		if err := tx.Create(&claim).Error; err != nil {
			return err
		}
		reward, err = redPacketRewardForSource(tx, &claim)
		return err
	})
	return reward, err
}

func ListUserRedPacketClaims(slug string, userID int) ([]RedPacketReward, error) {
	packet, err := getRedPacketBySlug(DB.Unscoped(), slug)
	if err != nil {
		return nil, err
	}
	var claims []RedPacketClaim
	if err := DB.Where("packet_id = ? AND user_id = ?", packet.Id, userID).Order("claim_index ASC").Find(&claims).Error; err != nil {
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
