package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestDeleteRedPacketPreservesClaimAndSourceContracts(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		name := "unclaimed"
		if claimed {
			name = "claimed"
		}
		t.Run(name, func(t *testing.T) {
			previousType := common.MainDatabaseType()
			common.SetMainDatabaseType(common.DatabaseTypeSQLite)
			t.Cleanup(func() { common.SetMainDatabaseType(previousType) })
			db := redPacketSchemaTestDB(t)
			require.NoError(t, db.AutoMigrate(&Redemption{}, &RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}))
			source := Redemption{UserId: 1, Key: "synthetic-delete-contract", Name: "reward", Quota: 100,
				Status: common.RedemptionCodeStatusEnabled, RewardType: RedemptionRewardQuota}
			require.NoError(t, db.Create(&source).Error)
			packet := RedPacket{Title: "packet", Enabled: true, CreatedBy: 1}
			require.NoError(t, CreateRedPacket(&packet, []RedPacketItemInput{{ItemType: RedPacketItemRedemption, SourceId: source.Id}}))
			if claimed {
				_, err := ClaimRedPacket(packet.Slug, 42)
				require.NoError(t, err)
				require.ErrorContains(t, DeleteRedPacket(packet.Id), "已有领取记录")
			} else {
				require.NoError(t, DeleteRedPacket(packet.Id))
				require.NoError(t, DeleteRedPacket(packet.Id), "an already absent packet remains idempotent")
			}
			for _, model := range []any{&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}} {
				var count int64
				require.NoError(t, db.Model(model).Count(&count).Error)
				if claimed {
					require.EqualValues(t, 1, count)
				} else {
					require.Zero(t, count)
				}
			}
			var rewardCount int64
			require.NoError(t, db.Model(&Redemption{}).Count(&rewardCount).Error)
			require.EqualValues(t, 1, rewardCount, "packet deletion must not destroy its reward source")
		})
	}
}

func TestDeleteRedPacketInvalidIDsDoNotUseDatabase(t *testing.T) {
	for _, id := range []int{0, -1} {
		require.ErrorIs(t, DeleteRedPacket(id), ErrRedPacketNotFound)
	}
}
