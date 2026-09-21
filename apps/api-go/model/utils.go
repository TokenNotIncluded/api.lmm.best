package model

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const (
	BatchUpdateTypeUserQuota = iota
	BatchUpdateTypeTokenQuota
	BatchUpdateTypeUsedQuota
	BatchUpdateTypeChannelUsedQuota
	BatchUpdateTypeRequestCount
	BatchUpdateTypeCount // if you add a new type, you need to add a new map and a new lock
)

var batchUpdateStores []map[int]int
var batchUpdateLocks []sync.Mutex
var batchUpdateFlushLock sync.Mutex

func init() {
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateStores = append(batchUpdateStores, make(map[int]int))
		batchUpdateLocks = append(batchUpdateLocks, sync.Mutex{})
	}
}

func InitBatchUpdater() {
	gopool.Go(func() { RunBatchUpdater(context.Background()) })
}

// RunBatchUpdater periodically persists queued updates until ctx is cancelled.
// InitBatchUpdater remains as a source-compatible background wrapper.
func RunBatchUpdater(ctx context.Context) {
	interval := time.Duration(common.BatchUpdateInterval) * time.Second
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			batchUpdate()
		}
	}
}

// FlushBatchUpdates synchronously persists all updates queued before the call.
func FlushBatchUpdates() {
	batchUpdate()
}

func saturatingAdd(a, b int) int {
	if b > 0 && a > common.MaxWalletQuota-b {
		return common.MaxWalletQuota
	}
	if b < 0 && a < common.MinWalletQuota-b {
		return common.MinWalletQuota
	}
	return a + b
}

func addNewRecord(type_ int, id int, value int) {
	batchUpdateLocks[type_].Lock()
	defer batchUpdateLocks[type_].Unlock()
	batchUpdateStores[type_][id] = saturatingAdd(batchUpdateStores[type_][id], value)
}

func batchUpdate() {
	batchUpdateFlushLock.Lock()
	defer batchUpdateFlushLock.Unlock()

	// check if there's any data to update
	hasData := false
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		if len(batchUpdateStores[i]) > 0 {
			hasData = true
			batchUpdateLocks[i].Unlock()
			break
		}
		batchUpdateLocks[i].Unlock()
	}

	if !hasData {
		return
	}

	common.SysLog("batch update started")
	stores := make([]map[int]int, BatchUpdateTypeCount)
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		stores[i] = batchUpdateStores[i]
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}

	for i, store := range stores {
		if i == BatchUpdateTypeUserQuota || i == BatchUpdateTypeUsedQuota || i == BatchUpdateTypeRequestCount {
			continue
		}
		for key, value := range store {
			switch i {
			case BatchUpdateTypeTokenQuota:
				err := increaseTokenQuota(key, value)
				if err != nil {
					common.SysLog("failed to batch update token quota: " + err.Error())
				}
			case BatchUpdateTypeChannelUsedQuota:
				updateChannelUsedQuota(key, value)
			}
		}
	}

	userQuotaStore := stores[BatchUpdateTypeUserQuota]
	usedQuotaStore := stores[BatchUpdateTypeUsedQuota]
	requestCountStore := stores[BatchUpdateTypeRequestCount]

	userIDs := make(map[int]struct{}, len(userQuotaStore)+len(usedQuotaStore)+len(requestCountStore))
	for key := range userQuotaStore {
		userIDs[key] = struct{}{}
	}
	for key := range usedQuotaStore {
		userIDs[key] = struct{}{}
	}
	for key := range requestCountStore {
		userIDs[key] = struct{}{}
	}
	for key := range userIDs {
		quotaDelta := userQuotaStore[key]
		if err := updateUserQuotaUsedQuotaAndRequestCount(key, quotaDelta, usedQuotaStore[key], requestCountStore[key]); err == nil {
			// A queued wallet mutation reaches Redis only after its database
			// UPDATE has succeeded. This avoids exposing credits from a batch
			// that was rejected by the final wallet boundary predicate.
			syncUserQuotaDeltaCacheAsync(key, quotaDelta, "sync batched user quota")
		}
	}
	common.SysLog("batch update finished")
}

func RecordExist(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}

func shouldUpdateRedis(fromDB bool, err error) bool {
	return common.RedisEnabled && fromDB && err == nil
}
