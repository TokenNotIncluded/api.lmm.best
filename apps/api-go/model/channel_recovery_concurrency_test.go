package model

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

func TestChannelRecoveryConcurrentWithoutCache(t *testing.T) {
	setupChannelStatusTest(t)
	channel := Channel{Key: "a\nb", Status: common.ChannelStatusAutoDisabled, ChannelInfo: ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusAutoDisabled}}}
	require.NoError(t, DB.Create(&channel).Error)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, key := range []string{"a", "b"} {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			<-start
			if !RecoverChannelKey(channel.Id, i, key) {
				t.Errorf("key %d was not recovered", i)
			}
		}(i, key)
	}
	close(start)
	wg.Wait()
	var stored Channel
	require.NoError(t, DB.First(&stored, channel.Id).Error)
	require.Empty(t, stored.ChannelInfo.MultiKeyStatusList)
	require.Equal(t, common.ChannelStatusEnabled, stored.Status)
}

func TestChannelRecoveryPostgresManualWriteWins(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &Channel{}, &Ability{})
	usePostgresDatabaseType(t)
	previousDB, previousCache := DB, common.MemoryCacheEnabled
	DB, common.MemoryCacheEnabled = db, false
	t.Cleanup(func() { DB, common.MemoryCacheEnabled = previousDB, previousCache })
	for _, manualKey := range []bool{false, true} {
		t.Run(fmt.Sprintf("key=%v", manualKey), func(t *testing.T) {
			channel := postgresTestChannel(0, "recovery", common.ChannelStatusAutoDisabled, "")
			channel.Key = "a\nb"
			channel.ChannelInfo = ChannelInfo{IsMultiKey: true, MultiKeyStatusList: map[int]int{0: common.ChannelStatusAutoDisabled, 1: common.ChannelStatusAutoDisabled}}
			require.NoError(t, db.Create(&channel).Error)
			tx := db.Begin()
			require.NoError(t, tx.Error)
			defer tx.Rollback()
			var locked Channel
			require.NoError(t, tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, channel.Id).Error)
			if manualKey {
				locked.ChannelInfo.MultiKeyStatusList[0] = common.ChannelStatusManuallyDisabled
			} else {
				locked.Status = common.ChannelStatusManuallyDisabled
			}
			require.NoError(t, locked.saveStatusStateWithDB(tx))
			result := make(chan bool, 1)
			go func() { result <- RecoverChannelKey(channel.Id, 0, "a") }()
			select {
			case <-result:
				t.Fatal("recovery did not wait for the database row lock")
			case <-time.After(100 * time.Millisecond):
			}
			require.NoError(t, tx.Commit().Error)
			select {
			case changed := <-result:
				require.False(t, changed)
			case <-time.After(5 * time.Second):
				t.Fatal("recovery remained blocked")
			}
			var stored Channel
			require.NoError(t, db.First(&stored, channel.Id).Error)
			require.Equal(t, locked.Status, stored.Status)
			require.Equal(t, locked.ChannelInfo.MultiKeyStatusList, stored.ChannelInfo.MultiKeyStatusList)
			if manualKey {
				require.True(t, RecoverChannelKey(channel.Id, 1, "b"))
				require.NoError(t, db.First(&stored, channel.Id).Error)
				require.Equal(t, common.ChannelStatusEnabled, stored.Status)
				require.Equal(t, common.ChannelStatusManuallyDisabled, stored.ChannelInfo.MultiKeyStatusList[0])
				require.Zero(t, stored.ChannelInfo.MultiKeyStatusList[1])
			}
		})
	}
}
