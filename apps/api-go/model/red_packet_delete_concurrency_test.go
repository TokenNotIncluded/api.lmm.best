package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Pause a real claim immediately before its transaction commits. A concurrent
// delete must lock the same packet first, not count an uncommitted claim as zero
// and then remove the newly claimed inventory after waiting for its item lock.
func TestRedPacketDeleteCannotEraseConcurrentClaimPostgres(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &Redemption{}, &RedPacket{}, &RedPacketItem{}, &RedPacketClaim{})
	previousDB, previousLogDB := DB, LOG_DB
	usePostgresDatabaseType(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	DB, LOG_DB = db.WithContext(ctx), db.WithContext(ctx)
	t.Cleanup(func() { DB, LOG_DB = previousDB, previousLogDB })
	t.Cleanup(cancel)

	reward := Redemption{UserId: 1, Key: "00000000000000000000000000000001", Name: "test reward", Quota: 100,
		Status: common.RedemptionCodeStatusEnabled, RewardType: RedemptionRewardQuota}
	require.NoError(t, db.Create(&reward).Error)
	unused := Redemption{UserId: 1, Key: "00000000000000000000000000000002", Name: "unused reward", Quota: 100,
		Status: common.RedemptionCodeStatusEnabled, RewardType: RedemptionRewardQuota}
	require.NoError(t, db.Create(&unused).Error)
	packet := RedPacket{Title: "concurrent claim", DrawMode: RedPacketDrawSequence, PerUserLimit: 1, Enabled: true, CreatedBy: 1}
	require.NoError(t, CreateRedPacket(&packet, []RedPacketItemInput{{ItemType: RedPacketItemRedemption, SourceId: reward.Id}, {ItemType: RedPacketItemRedemption, SourceId: unused.Id}}))

	type pausedClaim struct {
		pid int
		err error
	}
	paused := make(chan pausedClaim, 1)
	release := make(chan struct{})
	released := false
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:pause_red_packet_claim", func(tx *gorm.DB) {
		if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "red_packet_claims" || tx.Error != nil {
			return
		}
		var pid int
		err := tx.Session(&gorm.Session{NewDB: true}).Raw("SELECT pg_backend_pid()").Scan(&pid).Error
		paused <- pausedClaim{pid: pid, err: err}
		select {
		case <-release:
		case <-ctx.Done():
			tx.AddError(ctx.Err())
		}
	}))
	claimResult := make(chan error, 1)
	claimDone := make(chan struct{})
	go func() {
		defer close(claimDone)
		_, err := ClaimRedPacket(packet.Slug, 1234)
		claimResult <- err
	}()
	t.Cleanup(func() {
		if !released {
			close(release)
		}
		cancel()
		select {
		case <-claimDone:
		case <-time.After(5 * time.Second):
			t.Error("claim goroutine did not stop")
		}
	})
	var claim pausedClaim
	select {
	case claim = <-paused:
		require.NoError(t, claim.err)
		require.Positive(t, claim.pid)
	case err := <-claimResult:
		t.Fatalf("claim exited before the transaction barrier: %v", err)
	case <-ctx.Done():
		t.Fatal("claim did not reach the transaction barrier")
	}

	deleteResult := make(chan error, 1)
	deleteDone := make(chan struct{})
	go func() {
		defer close(deleteDone)
		deleteResult <- DeleteRedPacket(packet.Id)
	}()
	t.Cleanup(func() {
		// Unblock both transactions before restoring the process-wide DB.
		if !released {
			close(release)
			released = true
		}
		cancel()
		select {
		case <-deleteDone:
		case <-time.After(5 * time.Second):
			t.Error("delete goroutine did not stop")
		}
	})
	waitForPostgresBlocker(t, db, claim.pid)
	close(release)
	released = true
	select {
	case err := <-claimResult:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("claim did not finish")
	}
	select {
	case err := <-deleteResult:
		require.NoError(t, err, "a live partially claimed packet is archived after the claim commits")
	case <-ctx.Done():
		t.Fatal("delete did not finish")
	}
	for _, table := range []any{&RedPacket{}, &RedPacketItem{}, &RedPacketClaim{}} {
		var count int64
		require.NoError(t, db.Unscoped().Model(table).Count(&count).Error)
		require.EqualValues(t, 1, count, fmt.Sprintf("%T audit row must survive", table))
	}
	var active int64
	require.NoError(t, db.Model(&RedPacket{}).Count(&active).Error)
	require.Zero(t, active, "the archived packet disappears from active queries")
	_, err := ClaimRedPacket(packet.Slug, 5678)
	require.ErrorIs(t, err, ErrRedPacketNotFound)
	rewards, err := ListUserRedPacketClaims(packet.Slug, 1234)
	require.NoError(t, err)
	require.Len(t, rewards, 1)
	require.Equal(t, reward.Key, rewards[0].Code)
	replacement := RedPacket{Title: "released inventory", Enabled: true, CreatedBy: 1}
	require.NoError(t, CreateRedPacket(&replacement, []RedPacketItemInput{{ItemType: RedPacketItemRedemption, SourceId: unused.Id}}))
}
