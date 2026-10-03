package model

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type heroSMSSMSExpiredQuoteResult struct {
	order *HeroSMSSMSOrderView
	quota int
	err   error
}

func setupHeroSMSSMSExpiredQuoteProvider(t *testing.T) *atomic.Int32 {
	t.Helper()
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled: ptrBool(true), SMSEnabled: ptrBool(true), APIKey: "expired-quote-fixture-key", PriceMultiplier: "1",
	}))
	var purchases atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if writeHeroSMSTestOffer(w, r, "1", 5) {
			return
		}
		switch r.URL.Query().Get("action") {
		case "getActiveActivations":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			purchases.Add(1)
			_, _ = w.Write([]byte(`{"activationId":9027,"phoneNumber":"79001112233","activationCost":1,"currencyCode":840,"countryCode":6,"canGetAnotherSms":false}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL+"/api/v1", "fixture") }, server.URL+"/api/v1",
	))
	return &purchases
}

func heroSMSSMSUserLockTestUpdate(tx *gorm.DB) bool {
	if tx.Statement.Schema == nil || tx.Statement.Schema.Table != "users" {
		return false
	}
	values, ok := tx.Statement.Dest.(map[string]any)
	if !ok {
		return false
	}
	expr, ok := values["quota"].(clause.Expr)
	return ok && expr.SQL == "quota"
}

func awaitHeroSMSSMSQuoteExpiry(expiresAt time.Time) {
	timer := time.NewTimer(time.Until(expiresAt) + 25*time.Millisecond)
	defer timer.Stop()
	<-timer.C
}

func testHeroSMSSMSExpiredQuoteRejectsLateReservation(t *testing.T, db *gorm.DB) {
	user := createHeroSMSTestUser(t, db, 9027, common.GetTrustQuota())
	purchases := setupHeroSMSSMSExpiredQuoteProvider(t)
	var before User
	require.NoError(t, db.First(&before, user.Id).Error)
	beforeLock := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var lockAttempts atomic.Int32
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:sms_late_reservation", func(tx *gorm.DB) {
		if heroSMSSMSUserLockTestUpdate(tx) && lockAttempts.Add(1) == 1 {
			close(beforeLock)
			<-release
		}
	}))
	expiresAt := time.Now().Add(3 * time.Second).Truncate(time.Second)
	request := HeroSMSSMSPurchaseRequest{OfferID: heroSMSSMSExpiringTestQuote(t, user.Id, expiresAt)}
	originalDone := make(chan heroSMSSMSExpiredQuoteResult, 1)
	go func() {
		order, quota, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, request, "expired-late-reservation")
		originalDone <- heroSMSSMSExpiredQuoteResult{order: order, quota: quota, err: err}
	}()
	collected := false
	defer func() {
		releaseOnce.Do(func() { close(release) })
		if !collected {
			select {
			case <-originalDone:
			case <-time.After(5 * time.Second):
			}
		}
		_ = db.Callback().Update().Remove("test:sms_late_reservation")
	}()
	select {
	case <-beforeLock:
	case result := <-originalDone:
		collected = true
		t.Fatalf("original purchase never reached the reservation barrier: %v", result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("original purchase did not reach the reservation barrier")
	}
	awaitHeroSMSSMSQuoteExpiry(expiresAt)
	resolved, _, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, request, "expired-late-reservation")
	require.Nil(t, resolved)
	var apiErr *HeroSMSError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "PURCHASE_NOT_CREATED", apiErr.Code)
	releaseOnce.Do(func() { close(release) })
	original := <-originalDone
	collected = true
	require.Nil(t, original.order)
	require.ErrorAs(t, original.err, &apiErr)
	require.Equal(t, "PRICE_CHANGED", apiErr.Code)
	require.EqualValues(t, 2, lockAttempts.Load())
	require.Zero(t, purchases.Load())
	var after User
	require.NoError(t, db.First(&after, user.Id).Error)
	require.Equal(t, before, after)
	var orders, ledgers int64
	require.NoError(t, db.Model(&HeroSMSSMSOrder{}).Count(&orders).Error)
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
	require.Zero(t, orders)
	require.Zero(t, ledgers)
}

func TestHeroSMSSMSExpiredQuoteSQLiteRejectsLateReservation(t *testing.T) {
	testHeroSMSSMSExpiredQuoteRejectsLateReservation(t, setupHeroSMSTestDB(t))
}

func setupHeroSMSSMSExpiredQuotePostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("HERO_SMS_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("HERO_SMS_POSTGRES_TEST_DSN is not configured")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	schema := "sms_expiry_" + strings.ReplaceAll(common.GetUUID(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), &gorm.Config{TranslateError: true})
	require.NoError(t, err)
	previousDB, previousOptions, previousRedis := DB, common.OptionMap, common.RedisEnabled
	previousDatabaseType := common.MainDatabaseType()
	DB, common.OptionMap, common.RedisEnabled = db, map[string]string{}, false
	common.SetMainDatabaseType(common.DatabaseTypePostgreSQL)
	t.Setenv("HERO_SMS_ENCRYPTION_KEY", "test-hero-sms-expiry-encryption-key")
	t.Cleanup(func() {
		DB, common.OptionMap, common.RedisEnabled = previousDB, previousOptions, previousRedis
		common.SetMainDatabaseType(previousDatabaseType)
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		for _, connection := range []*gorm.DB{db, admin} {
			sqlDB, err := connection.DB()
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
		}
	})
	require.NoError(t, db.AutoMigrate(&User{}, &Option{}, &HeroSMSEmailOrder{}, &HeroSMSEmailActivation{}, &HeroSMSEmailQuotaLedger{}, &HeroSMSSMSOrder{}, &HeroSMSSMSQuotaLedger{}, &HeroSMSProviderPurchaseLease{}))
	InitOptionMap()
	return db
}

func TestHeroSMSSMSExpiredQuotePostgresRejectsLateReservation(t *testing.T) {
	testHeroSMSSMSExpiredQuoteRejectsLateReservation(t, setupHeroSMSSMSExpiredQuotePostgresDB(t))
}

func TestHeroSMSSMSExpiredQuotePostgresWaitsForUncommittedOrderAfterLeaseExpiry(t *testing.T) {
	db := setupHeroSMSSMSExpiredQuotePostgresDB(t)
	user := createHeroSMSTestUser(t, db, 9028, common.GetTrustQuota())
	purchases := setupHeroSMSSMSExpiredQuoteProvider(t)
	inserted := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	var insertedOnce sync.Once
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:sms_uncommitted_order", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "hero_sms_sms_orders" {
			insertedOnce.Do(func() { close(inserted) })
			<-release
		}
	}))
	resolverLockAttempt := make(chan struct{})
	var lockAttempts atomic.Int32
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("test:sms_resolver_lock", func(tx *gorm.DB) {
		if heroSMSSMSUserLockTestUpdate(tx) && lockAttempts.Add(1) == 2 {
			close(resolverLockAttempt)
		}
	}))
	expiresAt := time.Now().Add(3 * time.Second).Truncate(time.Second)
	request := HeroSMSSMSPurchaseRequest{OfferID: heroSMSSMSExpiringTestQuote(t, user.Id, expiresAt)}
	purchase := func(done chan<- heroSMSSMSExpiredQuoteResult) {
		order, quota, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, request, "expired-uncommitted-order")
		done <- heroSMSSMSExpiredQuoteResult{order: order, quota: quota, err: err}
	}
	originalDone := make(chan heroSMSSMSExpiredQuoteResult, 1)
	resolverDone := make(chan heroSMSSMSExpiredQuoteResult, 1)
	go purchase(originalDone)
	originalCollected := false
	resolverStarted, resolverCollected := false, false
	defer func() {
		releaseOnce.Do(func() { close(release) })
		if !originalCollected {
			select {
			case <-originalDone:
			case <-time.After(5 * time.Second):
			}
		}
		if resolverStarted && !resolverCollected {
			select {
			case <-resolverDone:
			case <-time.After(5 * time.Second):
			}
		}
		_ = db.Callback().Create().Remove("test:sms_uncommitted_order")
		_ = db.Callback().Update().Remove("test:sms_resolver_lock")
	}()
	select {
	case <-inserted:
	case result := <-originalDone:
		originalCollected = true
		t.Fatalf("original purchase did not reserve an order: %v", result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("original purchase did not reach the uncommitted-order barrier")
	}
	awaitHeroSMSSMSQuoteExpiry(expiresAt)
	require.NoError(t, db.Model(&HeroSMSProviderPurchaseLease{}).Where("name = ?", heroSMSProviderPurchaseLeaseName).UpdateColumn("expires_at", 0).Error)
	resolverStarted = true
	go purchase(resolverDone)
	select {
	case <-resolverLockAttempt:
	case result := <-resolverDone:
		resolverCollected = true
		t.Fatalf("recovery never attempted the user lock: %v", result.err)
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not reach the user-lock barrier")
	}
	select {
	case result := <-resolverDone:
		resolverCollected = true
		t.Fatalf("recovery bypassed an uncommitted reservation: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	original := <-originalDone
	originalCollected = true
	resolved := <-resolverDone
	resolverCollected = true
	require.NoError(t, original.err)
	require.NoError(t, resolved.err)
	require.NotNil(t, original.order)
	require.NotNil(t, resolved.order)
	require.Equal(t, original.order.ID, resolved.order.ID)
	require.Equal(t, original.quota, resolved.quota)
	require.EqualValues(t, 1, purchases.Load())
	var orders, ledgers int64
	require.NoError(t, db.Model(&HeroSMSSMSOrder{}).Count(&orders).Error)
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgers).Error)
	require.EqualValues(t, 1, orders)
	require.EqualValues(t, 1, ledgers)
	require.Equal(t, user.Quota-original.order.ChargeQuota, getUserQuotaValue(user.Id), fmt.Sprintf("one purchase must debit once for order %s", original.order.ID))
}
