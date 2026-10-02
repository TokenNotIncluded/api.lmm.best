package model

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Use only an explicitly disposable MySQL 8 server. Each test creates its own
// database and removes it afterwards. PROCESS and performance_schema read
// permissions are needed to observe the actual row-lock wait, not a timer.
func setupHeroSMSSMSExpiredQuoteMySQLDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("HERO_SMS_MYSQL_TEST_DSN"))
	if dsn == "" {
		t.Skip("HERO_SMS_MYSQL_TEST_DSN is not configured")
	}
	require.Equal(t, "1", os.Getenv("HERO_SMS_MYSQL_ISOLATED_DATABASE"), "acknowledge disposable database creation")
	config, err := mysqlDriver.ParseDSN(dsn)
	require.NoError(t, err)
	config.DBName, config.ParseTime = "", true
	config.Timeout, config.ReadTimeout, config.WriteTimeout = 5*time.Second, 20*time.Second, 20*time.Second
	open := func() *gorm.DB {
		db, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		return db
	}
	admin := open()
	name := fmt.Sprintf("lmm_sms_expiry_%d_%d", os.Getpid(), time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE DATABASE `"+name+"`").Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP DATABASE IF EXISTS `"+name+"`").Error)
		pool, err := admin.DB()
		require.NoError(t, err)
		require.NoError(t, pool.Close())
	})
	config.DBName = name
	db := open()
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(8)
	pool.SetMaxIdleConns(8)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	var version, isolation string
	require.NoError(t, db.Raw("SELECT VERSION(), @@transaction_isolation").Row().Scan(&version, &isolation))
	require.True(t, strings.HasPrefix(version, "8."), "requires actual MySQL 8")
	require.Equal(t, "REPEATABLE-READ", isolation)
	t.Logf("actual MySQL %s, default isolation %s", version, isolation)
	previousDB, previousOptions, previousRedis := DB, common.OptionMap, common.RedisEnabled
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	DB, common.OptionMap, common.RedisEnabled = db, map[string]string{}, false
	common.SetDatabaseTypes(common.DatabaseTypeMySQL, previousLog)
	t.Setenv("HERO_SMS_ENCRYPTION_KEY", "test-hero-sms-expiry-mysql-encryption-key")
	t.Cleanup(func() {
		DB, common.OptionMap, common.RedisEnabled = previousDB, previousOptions, previousRedis
		common.SetDatabaseTypes(previousMain, previousLog)
	})
	require.NoError(t, db.AutoMigrate(&User{}, &Option{}, &HeroSMSEmailOrder{}, &HeroSMSEmailActivation{}, &HeroSMSEmailQuotaLedger{}, &HeroSMSSMSOrder{}, &HeroSMSSMSQuotaLedger{}, &HeroSMSProviderPurchaseLease{}))
	InitOptionMap()
	return db
}

func heroSMSSMSMySQLConnection(tx *gorm.DB) int64 {
	var connection int64
	if err := tx.Statement.ConnPool.QueryRowContext(tx.Statement.Context, "SELECT CONNECTION_ID()").Scan(&connection); err != nil {
		tx.AddError(err)
	}
	return connection
}

func awaitHeroSMSSMSMySQLUserWait(t *testing.T, db *gorm.DB, requester, blocker int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		var count int64
		err := db.Raw("SELECT COUNT(*) FROM performance_schema.data_lock_waits AS w JOIN performance_schema.threads AS req ON req.THREAD_ID = w.REQUESTING_THREAD_ID JOIN performance_schema.threads AS blk ON blk.THREAD_ID = w.BLOCKING_THREAD_ID JOIN performance_schema.data_locks AS dl ON dl.ENGINE_LOCK_ID = w.REQUESTING_ENGINE_LOCK_ID AND dl.ENGINE = w.ENGINE WHERE req.PROCESSLIST_ID = ? AND blk.PROCESSLIST_ID = ? AND dl.OBJECT_SCHEMA = DATABASE() AND dl.OBJECT_NAME = 'users' AND dl.INDEX_NAME = 'PRIMARY'", requester, blocker).Scan(&count).Error
		return err == nil && count > 0
	}, 5*time.Second, 10*time.Millisecond, "request must actually wait on the other transaction's User PRIMARY lock")
	var isolation string
	require.NoError(t, db.Raw("SELECT ev.ISOLATION_LEVEL FROM performance_schema.events_transactions_current AS ev JOIN performance_schema.threads AS th ON th.THREAD_ID = ev.THREAD_ID WHERE th.PROCESSLIST_ID = ? AND ev.STATE = 'ACTIVE'", requester).Scan(&isolation).Error)
	require.Equal(t, "REPEATABLE READ", isolation, "qualify the active transaction under RR, not only the server default")
	t.Log("observed the active RR transaction waiting on User PRIMARY")
}

func heroSMSSMSMySQLBarrier(tx *gorm.DB, release <-chan struct{}) {
	select {
	case <-release:
	case <-time.After(15 * time.Second):
		tx.AddError(fmt.Errorf("SMS MySQL concurrency barrier timed out"))
	}
}

func TestHeroSMSSMSExpiredQuoteMySQLRejectsLateReservation(t *testing.T) {
	testHeroSMSSMSExpiredQuoteRejectsLateReservation(t, setupHeroSMSSMSExpiredQuoteMySQLDB(t))
}

func TestHeroSMSSMSExpiredQuoteMySQLWaitsForUncommittedReservation(t *testing.T) {
	db := setupHeroSMSSMSExpiredQuoteMySQLDB(t)
	user := createHeroSMSTestUser(t, db, 9030, common.GetTrustQuota())
	purchases := setupHeroSMSSMSExpiredQuoteProvider(t)
	inserted, resolverAttempt, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce, insertedOnce sync.Once
	var reserveConnection, resolverConnection atomic.Int64
	var lockAttempts atomic.Int32
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:sms_mysql_uncommitted", func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == "hero_sms_sms_orders" {
			insertedOnce.Do(func() {
				reserveConnection.Store(heroSMSSMSMySQLConnection(tx))
				close(inserted)
			})
			heroSMSSMSMySQLBarrier(tx, release)
		}
	}))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:sms_mysql_resolver", func(tx *gorm.DB) {
		if heroSMSSMSUserLockTestUpdate(tx) && lockAttempts.Add(1) == 2 {
			resolverConnection.Store(heroSMSSMSMySQLConnection(tx))
			close(resolverAttempt)
		}
	}))
	var workers sync.WaitGroup
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		workers.Wait()
		_ = db.Callback().Create().Remove("test:sms_mysql_uncommitted")
		_ = db.Callback().Update().Remove("test:sms_mysql_resolver")
	})
	expiresAt := time.Now().Add(3 * time.Second).Truncate(time.Second)
	request := HeroSMSSMSPurchaseRequest{OfferID: heroSMSSMSExpiringTestQuote(t, user.Id, expiresAt)}
	purchase := func(done chan<- heroSMSSMSExpiredQuoteResult) {
		defer workers.Done()
		order, quota, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, request, "mysql-uncommitted-reservation")
		done <- heroSMSSMSExpiredQuoteResult{order: order, quota: quota, err: err}
	}
	originalDone, resolverDone := make(chan heroSMSSMSExpiredQuoteResult, 1), make(chan heroSMSSMSExpiredQuoteResult, 1)
	workers.Add(1)
	go purchase(originalDone)
	select {
	case <-inserted:
	case result := <-originalDone:
		t.Fatalf("purchase ended before uncommitted reservation barrier: %v", result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("purchase did not insert an uncommitted reservation")
	}
	awaitHeroSMSSMSQuoteExpiry(expiresAt)
	require.NoError(t, db.Model(&HeroSMSProviderPurchaseLease{}).Where("name = ?", heroSMSProviderPurchaseLeaseName).UpdateColumn("expires_at", 0).Error)
	workers.Add(1)
	go purchase(resolverDone)
	select {
	case <-resolverAttempt:
	case result := <-resolverDone:
		t.Fatalf("recovery ended before attempting the User lock: %v", result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not attempt the User lock")
	}
	awaitHeroSMSSMSMySQLUserWait(t, db, resolverConnection.Load(), reserveConnection.Load())
	releaseOnce.Do(func() { close(release) })
	original, resolved := <-originalDone, <-resolverDone
	require.NoError(t, original.err)
	require.NoError(t, resolved.err, "the first consistent order read must see the reservation committed before acquiring User")
	require.NotNil(t, original.order)
	require.NotNil(t, resolved.order)
	require.Equal(t, original.order.ID, resolved.order.ID)
	require.Equal(t, original.quota, resolved.quota)
	require.EqualValues(t, 1, purchases.Load())
	var orders, reserves, ledgers int64
	require.NoError(t, db.Model(&HeroSMSSMSOrder{}).Count(&orders).Error)
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Where("entry_type = ?", HeroSMSSMSLedgerReserve).Count(&reserves).Error)
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
	require.EqualValues(t, 1, orders)
	require.EqualValues(t, 1, reserves)
	require.EqualValues(t, 1, ledgers)
	require.Equal(t, user.Quota-original.order.ChargeQuota, getUserQuotaValue(user.Id))
}

func TestHeroSMSSMSExpiredQuoteMySQLResolutionDoesNotInvertRefundLocks(t *testing.T) {
	db := setupHeroSMSSMSExpiredQuoteMySQLDB(t)
	user := createHeroSMSTestUser(t, db, 9031, common.GetTrustQuota())
	purchases := setupHeroSMSSMSExpiredQuoteProvider(t)
	request := HeroSMSSMSPurchaseRequest{OfferID: heroSMSSMSExpiringTestQuote(t, user.Id, time.Now().Add(-time.Second))}
	const key = "mysql-expired-refund-race"
	missed, continueResolve := make(chan struct{}), make(chan struct{})
	refundLocked, continueRefund := make(chan struct{}), make(chan struct{})
	userLocked, continueRead := make(chan struct{}), make(chan struct{})
	refundAttempt := make(chan struct{})
	var resolveOnce, refundOnce, readOnce sync.Once
	var refundConnection, resolverConnection atomic.Int64
	previousHook := heroSMSSMSIdempotencyMissHook
	heroSMSSMSIdempotencyMissHook = func() { close(missed); <-continueResolve }
	var workers sync.WaitGroup
	t.Cleanup(func() {
		resolveOnce.Do(func() { close(continueResolve) })
		refundOnce.Do(func() { close(continueRefund) })
		readOnce.Do(func() { close(continueRead) })
		workers.Wait()
		heroSMSSMSIdempotencyMissHook = previousHook
		_ = db.Callback().Query().Remove("test:sms_mysql_refund_order")
		_ = db.Callback().Query().Remove("test:sms_mysql_proof_user")
		_ = db.Callback().Update().Remove("test:sms_mysql_refund_user")
	})
	resolvedDone := make(chan heroSMSSMSExpiredQuoteResult, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		order, quota, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, request, key)
		resolvedDone <- heroSMSSMSExpiredQuoteResult{order: order, quota: quota, err: err}
	}()
	select {
	case <-missed:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not reach its initial miss")
	}
	// Represent a reservation committed after the initial autocommit lookup,
	// before expired recovery takes User. Use the real debit/ledger transaction.
	payload, err := common.Marshal(request)
	require.NoError(t, err)
	order := HeroSMSSMSOrder{UserID: user.Id, IdempotencyKeyHash: sha256Hex(key), RequestPayloadHash: sha256Hex(string(payload)), CountryID: 6, Service: "tg", Status: HeroSMSSMSOrderStatusPendingProvider, PriceMultiplier: "1", ProviderPriceCNY: "1", CustomerPriceUSD: "1", ReservedQuota: 100, ChargeQuota: 100}
	_, err = reserveHeroSMSSMSQuota(&order, time.Now().Add(heroSMSSMSQuoteTTL))
	require.NoError(t, err)
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:sms_mysql_refund_order", func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == "hero_sms_sms_orders" {
			if _, locking := tx.Statement.Clauses["FOR"]; locking && refundConnection.CompareAndSwap(0, heroSMSSMSMySQLConnection(tx)) {
				close(refundLocked)
				heroSMSSMSMySQLBarrier(tx, continueRefund)
			}
		}
	}))
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:sms_mysql_proof_user", func(tx *gorm.DB) {
		if tx.Error == nil && tx.Statement.Table == "users" {
			if _, locking := tx.Statement.Clauses["FOR"]; locking && resolverConnection.CompareAndSwap(0, heroSMSSMSMySQLConnection(tx)) {
				close(userLocked)
				heroSMSSMSMySQLBarrier(tx, continueRead)
			}
		}
	}))
	var attempted atomic.Bool
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:sms_mysql_refund_user", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" && !heroSMSSMSUserLockTestUpdate(tx) && attempted.CompareAndSwap(false, true) {
			close(refundAttempt)
		}
	}))
	refundDone := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		refundDone <- refundHeroSMSSMSOrder(order.ID, HeroSMSSMSOrderStatusFailed, "FIXTURE_REFUND", "fixture refund", HeroSMSSMSOrderStatusPendingProvider)
	}()
	select {
	case <-refundLocked:
	case err := <-refundDone:
		t.Fatalf("refund did not lock Order first: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("refund did not reach the Order lock barrier")
	}
	resolveOnce.Do(func() { close(continueResolve) })
	select {
	case <-userLocked:
	case result := <-resolvedDone:
		t.Fatalf("recovery did not lock User: %v", result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not reach the User lock barrier")
	}
	refundOnce.Do(func() { close(continueRefund) })
	select {
	case <-refundAttempt:
	case <-time.After(5 * time.Second):
		t.Fatal("refund did not attempt its User update")
	}
	awaitHeroSMSSMSMySQLUserWait(t, db, refundConnection.Load(), resolverConnection.Load())
	readOnce.Do(func() { close(continueRead) })
	resolved := <-resolvedDone
	require.NoError(t, resolved.err, "ordinary Order read must not wait for refund's Order lock while holding User")
	require.NotNil(t, resolved.order)
	require.Equal(t, order.ID, resolved.order.ID)
	require.Equal(t, user.Quota-order.ChargeQuota, resolved.quota)
	require.NoError(t, <-refundDone)
	var current HeroSMSSMSOrder
	require.NoError(t, db.First(&current, "id = ?", order.ID).Error)
	require.Equal(t, HeroSMSSMSOrderStatusFailed, current.Status)
	require.Equal(t, order.ReservedQuota, current.RefundedQuota)
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
	require.Zero(t, purchases.Load())
	var reserves, refunds, ledgers int64
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Where("entry_type = ?", HeroSMSSMSLedgerReserve).Count(&reserves).Error)
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Where("entry_type = ?", HeroSMSSMSLedgerRefund).Count(&refunds).Error)
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
	require.EqualValues(t, 1, reserves)
	require.EqualValues(t, 1, refunds)
	require.EqualValues(t, 2, ledgers)
}
