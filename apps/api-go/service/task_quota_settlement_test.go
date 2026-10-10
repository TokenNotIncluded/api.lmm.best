package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Only a new temporary SQLite database is used. These tests never read a DSN,
// send a payment, or use Redis. They do not prove PostgreSQL lock behaviour.
func taskSettlementFixture(t *testing.T, subscription bool) *model.Task {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "settlement.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.User{}, &model.Token{}, &model.Channel{}, &model.Log{}, &model.UserSubscription{}))
	oldDB, oldLog := model.DB, model.LOG_DB
	oldRedis, oldMemory := common.RedisEnabled, common.MemoryCacheEnabled
	oldBatch, oldExport := common.BatchUpdateEnabled, common.DataExportEnabled
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.MemoryCacheEnabled = false, false
	common.BatchUpdateEnabled, common.DataExportEnabled = false, false
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.RedisEnabled, common.MemoryCacheEnabled = oldRedis, oldMemory
		common.BatchUpdateEnabled, common.DataExportEnabled = oldBatch, oldExport
	})
	wallet, source, subscriptionID := 900, BillingSourceWallet, 0
	if subscription {
		wallet, source, subscriptionID = 1000, BillingSourceSubscription, 9201
	}
	seedUser(t, 9201, wallet)
	if subscription {
		seedSubscription(t, subscriptionID, 9201, 1000, 100)
	}
	seedToken(t, 9201, 9201, "synthetic-settlement-token", 900)
	seedChannel(t, 9201)
	seedChargedAccounting(t, 9201, 9201, 9201, 100, 1)
	task := makeTask(9201, 9201, 100, 9201, source, subscriptionID)
	task.TaskID = "synthetic-settlement-task"
	require.NoError(t, task.Insert())
	return task
}

func assertTaskSettlement(t *testing.T, task *model.Task, final int, subscription bool) {
	t.Helper()
	var user model.User
	var token model.Token
	var channel model.Channel
	var stored model.Task
	require.NoError(t, model.DB.First(&user, task.UserId).Error)
	require.NoError(t, model.DB.First(&token, task.PrivateData.TokenId).Error)
	require.NoError(t, model.DB.First(&channel, task.ChannelId).Error)
	require.NoError(t, model.DB.First(&stored, task.ID).Error)
	wallet := 1000 - final
	if subscription {
		wallet = 1000
		var sub model.UserSubscription
		require.NoError(t, model.DB.First(&sub, task.PrivateData.SubscriptionId).Error)
		require.EqualValues(t, final, sub.AmountUsed)
	}
	require.EqualValues(t, wallet, user.Quota)
	require.EqualValues(t, final, user.UsedQuota)
	require.EqualValues(t, 1, user.RequestCount)
	require.EqualValues(t, 1000-final, token.RemainQuota)
	require.EqualValues(t, final, token.UsedQuota)
	require.EqualValues(t, final, channel.UsedQuota)
	require.EqualValues(t, final, stored.Quota)
	t.Logf("funding=%s wallet=%d token_remain=%d task=%d user_used=%d channel_used=%d",
		task.PrivateData.BillingSource, user.Quota, token.RemainQuota, stored.Quota, user.UsedQuota, channel.UsedQuota)
}

func TestTaskQuotaSettlementStaleReplay(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		for _, final := range []int{75, 150} {
			t.Run(testTaskSettlementName(subscription, final), func(t *testing.T) {
				task := taskSettlementFixture(t, subscription)
				stale := *task
				RecalculateTaskQuota(context.Background(), task, final, "synthetic final usage")
				RecalculateTaskQuota(context.Background(), &stale, final, "same final usage")
				assertTaskSettlement(t, task, final, subscription)
				require.Equal(t, final, stale.Quota)
			})
		}
	}
}

func testTaskSettlementName(subscription bool, final int) string {
	name := "wallet"
	if subscription {
		name = "subscription"
	}
	if final < 100 {
		return name + "_return_difference"
	}
	return name + "_charge_difference"
}

func TestTaskQuotaSettlementRollbackAndRetry(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		for _, table := range []string{"tasks", "tokens", "channels"} {
			t.Run(testTaskSettlementName(subscription, 150)+"/"+table, func(t *testing.T) {
				task := taskSettlementFixture(t, subscription)
				injected := errors.New("synthetic database write failure")
				callback := "test:task_settlement_failure"
				require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { _ = model.DB.Callback().Update().Remove(callback) })
				_, applied, err := model.ApplyTaskQuotaSettlement(task.ID, 100, 150)
				require.ErrorIs(t, err, injected)
				require.False(t, applied)
				assertTaskSettlement(t, task, 100, subscription)
				require.NoError(t, model.DB.Callback().Update().Remove(callback))
				RecalculateTaskQuota(context.Background(), task, 150, "retry after rollback")
				assertTaskSettlement(t, task, 150, subscription)
			})
		}
	}
}

func TestTaskQuotaSettlementConcurrentReplay(t *testing.T) {
	task := taskSettlementFixture(t, false)
	const workers = 8
	results := make(chan bool, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, applied, err := model.ApplyTaskQuotaSettlement(task.ID, 100, 150)
			results <- applied
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	appliedCount := 0
	for applied := range results {
		if applied {
			appliedCount++
		}
	}
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, appliedCount)
	assertTaskSettlement(t, task, 150, false)
}

func TestTaskQuotaSettlementRejectsConflictingState(t *testing.T) {
	task := taskSettlementFixture(t, false)
	_, applied, err := model.ApplyTaskQuotaSettlement(task.ID, 100, 150)
	require.NoError(t, err)
	require.True(t, applied)
	_, applied, err = model.ApplyTaskQuotaSettlement(task.ID, 100, 200)
	require.ErrorIs(t, err, model.ErrTaskQuotaSettlementConflict)
	require.False(t, applied)
	assertTaskSettlement(t, task, 150, false)
}

func TestTaskQuotaSettlementDoesNotRacePreparedRefund(t *testing.T) {
	task := taskSettlementFixture(t, false)
	_, pending, err := model.PrepareTaskRefundIntent(task.ID, 100)
	require.NoError(t, err)
	require.True(t, pending)
	_, applied, err := model.ApplyTaskQuotaSettlement(task.ID, 100, 150)
	require.ErrorIs(t, err, model.ErrTaskQuotaSettlementConflict)
	require.False(t, applied)
	assertTaskSettlement(t, task, 100, false)
}
