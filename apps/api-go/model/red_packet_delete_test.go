package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestDeleteRedPacketPreservesClaimAndSourceContracts(t *testing.T) {
	for _, state := range []string{"unclaimed", "exhausted", "expired", "paused", "live"} {
		t.Run(state, func(t *testing.T) {
			previousType := common.MainDatabaseType()
			common.SetMainDatabaseType(common.DatabaseTypeSQLite)
			t.Cleanup(func() { common.SetMainDatabaseType(previousType) })
			db := redPacketSchemaTestDB(t)
			require.NoError(t, db.AutoMigrate(&Redemption{}, &RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}))
			inputs := make([]RedPacketItemInput, 0, 2)
			for _, key := range []string{"synthetic-delete-1", "synthetic-delete-2"} {
				source := Redemption{UserId: 1, Key: key, Name: "reward", Quota: 100,
					Status: common.RedemptionCodeStatusEnabled, RewardType: RedemptionRewardQuota}
				require.NoError(t, db.Create(&source).Error)
				inputs = append(inputs, RedPacketItemInput{ItemType: RedPacketItemRedemption, SourceId: source.Id})
			}
			if state == "exhausted" {
				inputs = inputs[:1]
			}
			packet := RedPacket{Title: "packet", Enabled: true, CreatedBy: 1}
			require.NoError(t, CreateRedPacket(&packet, inputs))
			hasHistory := state != "unclaimed"
			if hasHistory {
				_, err := ClaimRedPacket(packet.Slug, 42)
				require.NoError(t, err)
			}
			if state == "expired" {
				// Inclusive end boundary must be treated as expired.
				require.NoError(t, db.Model(&packet).Update("end_at", common.GetTimestamp()).Error)
			}
			if state == "paused" {
				require.NoError(t, db.Model(&packet).Update("enabled", false).Error)
			}
			if state == "live" {
				require.ErrorContains(t, DeleteRedPacket(packet.Id), "仍在进行")
				packets, err := ListRedPackets()
				require.NoError(t, err)
				require.Len(t, packets, 1)
				require.EqualValues(t, 1, packets[0].RemainingItems)
				return
			}
			require.NoError(t, DeleteRedPacket(packet.Id))
			require.NoError(t, DeleteRedPacket(packet.Id), "deletion is idempotent")
			packets, err := ListRedPackets()
			require.NoError(t, err)
			require.Empty(t, packets)
			_, err = ClaimRedPacket(packet.Slug, 43)
			require.ErrorIs(t, err, ErrRedPacketNotFound)
			var active int64
			require.NoError(t, db.Model(&RedPacket{}).Count(&active).Error)
			require.Zero(t, active)
			for _, table := range []any{&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}} {
				var count int64
				require.NoError(t, db.Unscoped().Model(table).Count(&count).Error)
				if hasHistory {
					require.EqualValues(t, 1, count)
				} else {
					require.Zero(t, count)
				}
			}
			var rewardCount int64
			require.NoError(t, db.Model(&Redemption{}).Count(&rewardCount).Error)
			require.EqualValues(t, 2, rewardCount, "never destroy underlying reward sources")
			if hasHistory {
				rewards, err := ListUserRedPacketClaims(packet.Slug, 42)
				require.NoError(t, err)
				require.Len(t, rewards, 1)
				require.NotEmpty(t, rewards[0].Code)
				others, err := ListUserRedPacketClaims(packet.Slug, 43)
				require.NoError(t, err)
				require.Empty(t, others, "history remains scoped to its recipient")
				view, err := GetRedPacketPublic(packet.Slug)
				require.NoError(t, err)
				require.False(t, view.Enabled)
				require.Zero(t, view.RemainingItems)
				var archived RedPacket
				require.NoError(t, db.Unscoped().First(&archived, packet.Id).Error)
				require.True(t, archived.DeletedAt.Valid)
			}
		})
	}
}

func TestDeleteRedPacketInvalidIDsDoNotUseDatabase(t *testing.T) {
	for _, id := range []int{0, -1} {
		require.ErrorIs(t, DeleteRedPacket(id), ErrRedPacketNotFound)
	}
}
