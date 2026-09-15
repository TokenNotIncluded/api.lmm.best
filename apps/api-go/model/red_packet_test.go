package model

import (
	"errors"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRedPacketTestDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	previousDB, previousLogDB := DB, LOG_DB
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:red-packet-%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	DB, LOG_DB = db, db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, db.AutoMigrate(models...))
	t.Cleanup(func() {
		DB, LOG_DB = previousDB, previousLogDB
	})
	return db
}

func TestClaimRedPacketHonorsPerUserLimitAndInventory(t *testing.T) {
	db := setupRedPacketTestDB(t, &Redemption{}, &DiscountCode{}, &RedPacket{}, &RedPacketItem{}, &RedPacketClaim{})
	now := common.GetTimestamp()
	for index := 0; index < 2; index++ {
		row := Redemption{
			UserId: 1,
			Key: fmt.Sprintf("packet-code-%d", index),
			Status: common.RedemptionCodeStatusEnabled,
			Name: fmt.Sprintf("reward-%d", index),
			Quota: 100 + index,
			RewardType: RedemptionRewardQuota,
			CreatedTime: now,
		}
		require.NoError(t, db.Create(&row).Error)
	}
	var rows []Redemption
	require.NoError(t, db.Order("id ASC").Find(&rows).Error)

	packet := RedPacket{
		Title: "fair packet",
		DrawMode: RedPacketDrawSequence,
		PerUserLimit: 1,
		Enabled: true,
		CreatedBy: 1,
	}
	require.NoError(t, CreateRedPacket(&packet, []RedPacketItemInput{
		{ItemType: RedPacketItemRedemption, SourceId: rows[0].Id, Weight: 1},
		{ItemType: RedPacketItemRedemption, SourceId: rows[1].Id, Weight: 1},
	}))

	first, err := ClaimRedPacket(packet.Slug, 1001)
	require.NoError(t, err)
	require.Equal(t, rows[0].Key, first.Code)

	_, err = ClaimRedPacket(packet.Slug, 1001)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrRedPacketClaimLimit))

	second, err := ClaimRedPacket(packet.Slug, 1002)
	require.NoError(t, err)
	require.Equal(t, rows[1].Key, second.Code)

	public, err := GetRedPacketPublic(packet.Slug)
	require.NoError(t, err)
	require.EqualValues(t, 2, public.TotalItems)
	require.EqualValues(t, 0, public.RemainingItems)
	require.EqualValues(t, 2, public.ClaimCount)
}

func TestClaimRedPacketBindsDiscountCodeToClaimant(t *testing.T) {
	db := setupRedPacketTestDB(t, &DiscountCode{}, &RedPacket{}, &RedPacketItem{}, &RedPacketClaim{})
	now := common.GetTimestamp()
	code := DiscountCode{
		Code: "PACKET10",
		Name: "packet discount",
		DiscountPercent: 10,
		Status: DiscountCodeStatusEnabled,
		MaxUses: 1,
		CreatedTime: now,
		UpdatedTime: now,
	}
	require.NoError(t, db.Create(&code).Error)
	packet := RedPacket{Title: "discount", DrawMode: RedPacketDrawRandom, PerUserLimit: 1, Enabled: true, CreatedBy: 1}
	require.NoError(t, CreateRedPacket(&packet, []RedPacketItemInput{{ItemType: RedPacketItemDiscount, SourceId: code.Id, Weight: 1}}))

	reward, err := ClaimRedPacket(packet.Slug, 77)
	require.NoError(t, err)
	require.Equal(t, code.Code, reward.Code)

	var stored DiscountCode
	require.NoError(t, db.First(&stored, code.Id).Error)
	require.Equal(t, 77, stored.OwnerUserID)
}

func TestRedeemWithResultCreatesBankedResetVoucher(t *testing.T) {
	db := setupRedPacketTestDB(t, &SubscriptionPlan{}, &SubscriptionResetVoucher{}, &Redemption{}, &Log{})
	plan := SubscriptionPlan{Title: "Resettable plan", Enabled: true}
	require.NoError(t, db.Create(&plan).Error)

	redemption := Redemption{
		UserId: 1,
		Key: "reset-voucher-code",
		Status: common.RedemptionCodeStatusEnabled,
		Name: "banked reset",
		RewardType: RedemptionRewardResetVoucher,
		ResetPlanId: plan.Id,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, db.Create(&redemption).Error)

	result, err := RedeemWithResult(redemption.Key, 42)
	require.NoError(t, err)
	require.Equal(t, RedemptionRewardResetVoucher, result.RewardType)
	require.Equal(t, plan.Id, result.ResetPlanId)
	require.NotZero(t, result.ResetVoucherId)

	var voucher SubscriptionResetVoucher
	require.NoError(t, db.First(&voucher, result.ResetVoucherId).Error)
	require.Equal(t, 42, voucher.UserId)
	require.Equal(t, plan.Id, voucher.PlanId)
	require.Equal(t, SubscriptionResetVoucherAvailable, voucher.Status)
	require.Equal(t, fmt.Sprintf("redemption:%d", redemption.Id), voucher.OperationId)
}
