package model

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func withSubscriptionResetSQLiteDB(t *testing.T, maxOpenConnections int) *gorm.DB {
	t.Helper()
	previousDB := DB
	dsn := "file:" + filepath.Join(t.TempDir(), "subscription-reset.db") + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(maxOpenConnections)
	require.NoError(t, db.AutoMigrate(
		&User{}, &SubscriptionPlan{}, &UserSubscription{},
		&SubscriptionResetVoucher{}, &SubscriptionResetEvent{},
		&SubscriptionResetPreview{}, &SubscriptionResetOperation{}, &Log{},
	))
	DB = db
	t.Cleanup(func() {
		DB = previousDB
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestSubscriptionResetConcurrentPreviewClaimsExecuteExactlyOnceSQLite(t *testing.T) {
	db := withSubscriptionResetSQLiteDB(t, 4)
	seedResetSubscription(t, 9791, 9792, 9793, 700)
	preview, err := AdminPreviewSubscriptionsReset(AdminSubscriptionResetBatchInput{
		ActorUserId: 1,
		Mode:        SubscriptionResetModeHard,
		Targets:     []SubscriptionResetTarget{{UserId: 9791, PlanId: 9792}},
	})
	require.NoError(t, err)

	start := make(chan struct{})
	var wait sync.WaitGroup
	errorsByAttempt := make([]error, 2)
	for index, operationID := range []string{"concurrent-reset-a", "concurrent-reset-b"} {
		wait.Add(1)
		go func(index int, operationID string) {
			defer wait.Done()
			<-start
			_, errorsByAttempt[index] = AdminResetSubscriptionsBatch(AdminSubscriptionResetBatchInput{
				ActorUserId: 1, OperationId: operationID, PreviewToken: preview.Token,
			})
		}(index, operationID)
	}
	close(start)
	wait.Wait()

	successCount := 0
	for _, err := range errorsByAttempt {
		if err == nil {
			successCount++
		}
	}
	require.Equal(t, 1, successCount)
	var operations, events, audits int64
	require.NoError(t, db.Model(&SubscriptionResetOperation{}).Count(&operations).Error)
	require.NoError(t, db.Model(&SubscriptionResetEvent{}).Count(&events).Error)
	require.NoError(t, db.Model(&Log{}).Where("other LIKE ?", "%\"action\":\"subscription.reset.execute\"%").Count(&audits).Error)
	require.EqualValues(t, 1, operations)
	require.EqualValues(t, 1, events)
	require.EqualValues(t, 1, audits)
	var subscription UserSubscription
	require.NoError(t, db.First(&subscription, 9793).Error)
	require.Zero(t, subscription.AmountUsed)
}

func TestEnsureSubscriptionPlanTableSQLiteAddsArchivedAt(t *testing.T) {
	db := withSubscriptionResetSQLiteDB(t, 1)
	require.NoError(t, db.Migrator().DropTable(&SubscriptionPlan{}))
	require.NoError(t, db.Exec(`CREATE TABLE subscription_plans (
		id integer PRIMARY KEY,
		title text NOT NULL,
		price_amount integer NOT NULL DEFAULT 0,
		currency text NOT NULL DEFAULT 'USD',
		duration_value integer NOT NULL DEFAULT 1,
		duration_unit text NOT NULL DEFAULT 'month',
		total_amount bigint NOT NULL DEFAULT 0,
		enabled numeric DEFAULT 1
	)`).Error)

	require.NoError(t, ensureSubscriptionPlanTableSQLite())
	require.True(t, db.Migrator().HasColumn(&SubscriptionPlan{}, "ArchivedAt"))
}
