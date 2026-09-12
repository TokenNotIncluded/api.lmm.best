package model

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func subscriptionBillingModelFixture(t *testing.T, postgres bool) *gorm.DB {
	t.Helper()
	var db *gorm.DB
	if postgres {
		db = openIsolatedPostgresCacheTestDB(t, &User{}, &Token{}, &SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{})
		previous := DB
		DB = db
		t.Cleanup(func() { DB = previous })
		usePostgresDatabaseType(t)
	} else {
		db = setupSubscriptionPreConsumeErrorTestDB(t)
		require.NoError(t, db.AutoMigrate(&User{}, &Token{}))
	}
	previousRedis, previousBatch := common.RedisEnabled, common.BatchUpdateEnabled
	common.RedisEnabled, common.BatchUpdateEnabled = false, true
	t.Cleanup(func() { common.RedisEnabled, common.BatchUpdateEnabled = previousRedis, previousBatch })
	require.NoError(t, db.Create(&User{Id: 9001, Username: "billing-model", Quota: 1000000}).Error)
	require.NoError(t, db.Create(&Token{Id: 9002, UserId: 9001, Key: "billing-model-token", RemainQuota: 1000000}).Error)
	require.NoError(t, db.Create(&SubscriptionPlan{Id: 9003, Title: "billing", DurationUnit: SubscriptionDurationDay, DurationValue: 1, QuotaResetPeriod: SubscriptionResetNever}).Error)
	require.NoError(t, db.Create(&UserSubscription{Id: 9101, UserId: 9001, PlanId: 9003, AmountTotal: 100000, Status: "active", EndTime: time.Now().Unix() + 3600, AllowWalletOverflow: true}).Error)
	return db
}

func TestSubscriptionBillingConcurrentPostgres(t *testing.T) {
	db := subscriptionBillingModelFixture(t, true)
	const n = 6
	for i := 0; i < n; i++ {
		_, err := PreConsumeSubscriptionBilling(fmt.Sprint("request-", i), 9001, 9002, "model", 10000, true)
		require.NoError(t, err)
	}
	start := make(chan struct{})
	errs := make(chan error, n*2)
	var wg sync.WaitGroup
	// Each request is settled twice concurrently, competing for the same last
	// 40000 subscription quota. PostgreSQL must serialize before splitting.
	for i := 0; i < n*2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := SettleSubscriptionBilling(fmt.Sprint("request-", i%n), 9001, 30000)
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var sub UserSubscription
	require.NoError(t, db.First(&sub, 9101).Error)
	require.EqualValues(t, 100000, sub.AmountUsed)
	var user User
	require.NoError(t, db.First(&user, 9001).Error)
	require.Equal(t, 920000, user.Quota)
	var token Token
	require.NoError(t, db.First(&token, 9002).Error)
	require.Equal(t, 820000, token.RemainQuota)
	require.Equal(t, 180000, token.UsedQuota)
	var wallet int64
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).Select("SUM(wallet_consumed)").Scan(&wallet).Error)
	require.EqualValues(t, 80000, wallet)
}

func TestSubscriptionBillingUserLockPostgres(t *testing.T) {
	db := subscriptionBillingModelFixture(t, true)
	_, err := PreConsumeSubscriptionBilling("lock-request", 9001, 9002, "model", 60000, true)
	require.NoError(t, err)
	assertSubscriptionMutationWaitsForUser(t, db, func() error {
		_, err := SettleSubscriptionBilling("lock-request", 9001, 160000)
		return err
	})
}

func TestSubscriptionBillingPreconsumeAtomicReplayPostgres(t *testing.T) {
	db := subscriptionBillingModelFixture(t, true)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := PreConsumeSubscriptionBilling("same-request", 9001, 9002, "model", 60000, true)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var token Token
	require.NoError(t, db.First(&token, 9002).Error)
	require.Equal(t, 60000, token.UsedQuota)
	require.NoError(t, RefundSubscriptionBilling("same-request", 9001))
	require.NoError(t, RefundSubscriptionBilling("same-request", 9001))
	require.NoError(t, db.First(&token, 9002).Error)
	require.Zero(t, token.UsedQuota)
}

func TestSubscriptionBillingPreconsumeTokenFailure(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	require.NoError(t, db.Model(&Token{}).Where("id = ?", 9002).Update("remain_quota", 1).Error)
	_, err := PreConsumeSubscriptionBilling("insufficient-token", 9001, 9002, "model", 60000, true)
	require.Error(t, err)
	var sub UserSubscription
	require.NoError(t, db.First(&sub, 9101).Error)
	require.Zero(t, sub.AmountUsed)
	var count int64
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestSubscriptionBillingRefundFailureRetryAndRetention(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	_, err := PreConsumeSubscriptionBilling("refund-retry", 9001, 9002, "model", 60000, true)
	require.NoError(t, err)
	_, err = ReserveSubscriptionBilling("refund-retry", 9001, 90000)
	require.NoError(t, err)
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("fail_refund_token", func(tx *gorm.DB) {
		if tx.Statement.Table == "tokens" {
			tx.AddError(errors.New("refund token failure"))
		}
	}))
	require.Error(t, RefundSubscriptionBilling("refund-retry", 9001))
	var sub UserSubscription
	require.NoError(t, db.First(&sub, 9101).Error)
	require.EqualValues(t, 90000, sub.AmountUsed)
	require.NoError(t, db.Callback().Update().Remove("fail_refund_token"))
	require.NoError(t, RefundSubscriptionBilling("refund-retry", 9001))
	require.NoError(t, RefundSubscriptionBilling("refund-retry", 9001))
	require.NoError(t, db.First(&sub, 9101).Error)
	require.Zero(t, sub.AmountUsed)
	// A cleanup must never remove the deduplication boundary for managed work.
	require.NoError(t, db.Model(&SubscriptionPreConsumeRecord{}).Where("request_id = ?", "refund-retry").UpdateColumn("updated_at", 1).Error)
	removed, err := CleanupSubscriptionPreConsumeRecords(1)
	require.NoError(t, err)
	require.Zero(t, removed)
	_, err = PreConsumeSubscriptionBilling("refund-retry", 9001, 9002, "model", 60000, true)
	require.Error(t, err)
}

func TestSubscriptionBillingSettlementWalletFailureAndPolicyRetry(t *testing.T) {
	db := subscriptionBillingModelFixture(t, false)
	_, err := PreConsumeSubscriptionBilling("wallet-retry", 9001, 9002, "model", 60000, true)
	require.NoError(t, err)
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", 9101).Update("allow_wallet_overflow", false).Error)
	r, err := SettleSubscriptionBilling("wallet-retry", 9001, 160000)
	require.ErrorIs(t, err, ErrSubscriptionQuotaInsufficient)
	require.Equal(t, "settling", r.Status)
	require.NoError(t, db.Model(&UserSubscription{}).Where("id = ?", 9101).Update("allow_wallet_overflow", true).Error)
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("fail_wallet", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("wallet write failure"))
		}
	}))
	_, err = SettleSubscriptionBilling("wallet-retry", 9001, 160000)
	require.ErrorContains(t, err, "wallet write failure")
	var sub UserSubscription
	require.NoError(t, db.First(&sub, 9101).Error)
	require.EqualValues(t, 60000, sub.AmountUsed)
	require.NoError(t, db.Callback().Update().Remove("fail_wallet"))
	r, err = SettleSubscriptionBilling("wallet-retry", 9001, 160000)
	require.NoError(t, err)
	require.EqualValues(t, 100000, r.SubscriptionQuota)
	require.EqualValues(t, 60000, r.WalletQuota)
}

func TestSubscriptionBillingMigrationPostgres(t *testing.T) {
	db := subscriptionBillingModelFixture(t, true)
	var schema string
	require.NoError(t, db.Raw("SELECT current_schema()").Scan(&schema).Error)
	var required []interface{}
	for _, candidate := range mainMigrationModels() {
		if _, ok := candidate.(*SubscriptionPreConsumeRecord); ok {
			required = append(required, candidate)
		}
	}
	require.Len(t, required, 1, "CLI apply and verify must include the billing ledger")
	inventory, err := buildPostgresSchemaInventory(db, schema, required)
	require.NoError(t, err)
	columns := []string{"billing_managed", "token_id", "token_consumed", "wallet_overflow", "actual_quota", "wallet_consumed"}
	for _, column := range columns {
		require.NoError(t, db.Migrator().DropColumn(&SubscriptionPreConsumeRecord{}, column))
	}
	require.NoError(t, db.Exec("INSERT INTO subscription_pre_consume_records (request_id,user_id,user_subscription_id,pre_consumed,status) VALUES ('legacy',9001,9101,1,'consumed')").Error)
	require.Error(t, verifyPostgresSchemaInventory(db, inventory), "verify must reject pre-fix schema")
	require.NoError(t, db.AutoMigrate(required...))
	require.NoError(t, verifyPostgresSchemaInventory(db, inventory))
	var legacy SubscriptionPreConsumeRecord
	require.NoError(t, db.Where("request_id = ?", "legacy").First(&legacy).Error)
	require.False(t, legacy.BillingManaged)
	require.EqualValues(t, 1, legacy.PreConsumed)
	require.Zero(t, legacy.TokenConsumed)
}
