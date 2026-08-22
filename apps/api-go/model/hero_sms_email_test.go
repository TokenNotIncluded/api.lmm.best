package model

import (
	"context"
	"encoding/json"
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
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupHeroSMSTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	require.NoError(t, db.AutoMigrate(&User{}, &Option{}, &HeroSMSEmailOrder{}, &HeroSMSEmailActivation{}, &HeroSMSEmailQuotaLedger{}, &SystemTask{}, &SystemTaskLock{}))
	oldMap := common.OptionMap
	common.OptionMap = map[string]string{}
	InitOptionMap()
	t.Cleanup(func() { common.OptionMap = oldMap })
	oldEnabled := setting.HeroSMSEnabled
	oldKey := setting.HeroSMSAPIKey
	oldMultiplier := setting.HeroSMSPriceMultiplierValue
	t.Cleanup(func() {
		setting.HeroSMSEnabled = oldEnabled
		setting.HeroSMSAPIKey = oldKey
		setting.HeroSMSPriceMultiplierValue = oldMultiplier
	})
	oldEnv, hadEnv := os.LookupEnv("HERO_SMS_ENCRYPTION_KEY")
	require.NoError(t, os.Setenv("HERO_SMS_ENCRYPTION_KEY", "test-hero-sms-encryption-key-32-bytes"))
	t.Cleanup(func() {
		if hadEnv {
			_ = os.Setenv("HERO_SMS_ENCRYPTION_KEY", oldEnv)
		} else {
			_ = os.Unsetenv("HERO_SMS_ENCRYPTION_KEY")
		}
	})
	return db
}

func createHeroSMSTestUser(t *testing.T, db *gorm.DB, id int, quota int) User {
	t.Helper()
	user := User{Id: id, Username: fmt.Sprintf("hero-user-%d", id), Password: "password123", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: quota, Group: "default", AffCode: fmt.Sprintf("hero-aff-%d", id)}
	require.NoError(t, db.Create(&user).Error)
	return user
}

func heroSMSHTTPFactory(baseURL string, apiKey string, timeout time.Duration) func(string, string) herosms.Client {
	return func(_ string, _ string) herosms.Client {
		client := herosms.NewClient(baseURL, apiKey)
		client.TimeoutForTest(timeout)
		return client
	}
}

func TestHeroSMSEmailProductsPricingAndCurrency(t *testing.T) {
	setupHeroSMSTestDB(t)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "secret"}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": "domain-1", "domain": "mail.test", "site": "demo", "stock": 5, "cost": "0.0000011", "currency": "USD", "currency_code": 840}}, "page": 1, "size": 10, "total": 1})
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	products, err := ListHeroSMSEmailProducts(context.Background(), 1, 10, "")
	require.NoError(t, err)
	require.Len(t, products.Data, 1)
	require.Equal(t, "10", products.Data[0].PriceMultiplier)
	require.Equal(t, "0.000001", products.Data[0].UpstreamCostUSD)
	require.Equal(t, "0.000011", products.Data[0].CustomerPriceUSD)
	require.Equal(t, 6, products.Data[0].ChargeQuota)

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{PriceMultiplier: "12.5"}))
	products, err = ListHeroSMSEmailProducts(context.Background(), 1, 10, "")
	require.NoError(t, err)
	require.Equal(t, "12.5", products.Data[0].PriceMultiplier)

	mismatch := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": "domain-1", "domain": "mail.test", "site": "demo", "stock": 5, "cost": "1.23", "currency": "EUR", "currency_code": 978}}, "page": 1, "size": 10, "total": 1})
	}))
	defer mismatch.Close()
	restore = SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(mismatch.URL, "secret") }, mismatch.URL)
	defer restore()
	_, err = ListHeroSMSEmailProducts(context.Background(), 1, 10, "")
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "CURRENCY_MISMATCH", apiErr.Code)
}

func TestHeroSMSEmailPurchaseIdempotencyAndConcurrency(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 101, 2000000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "secret"}))
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": "domain-1", "domain": "mail.test", "site": "demo", "stock": 5, "cost": "0.10", "currency": "USD", "currency_code": 840}}, "page": 1, "size": 100, "total": 1})
		case http.MethodPost + " /emails":
			seq := posts.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": fmt.Sprintf("email-%d", seq), "email": fmt.Sprintf("user-%d@mail.test", seq), "cost": "0.10", "currency": "USD", "currency_code": 840, "status": "active"})
		case http.MethodGet + " /emails/email-1":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "email-1", "email": "user-1@mail.test", "cost": "0.10", "currency": "USD", "currency_code": 840, "status": "active"})
		case http.MethodGet + " /emails/email-2":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "email-2", "email": "user-2@mail.test", "cost": "0.10", "currency": "USD", "currency_code": 840, "status": "active"})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	request := HeroSMSEmailPurchaseRequest{DomainID: "domain-1", Quantity: 1}
	order, status, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "idem-1", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, int32(1), posts.Load())

	replayed, replayStatus, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "idem-1", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, replayStatus)
	require.Equal(t, order.ID, replayed.ID)
	require.Equal(t, int32(1), posts.Load())

	_, _, err = CreateHeroSMSEmailActivations(context.Background(), user.Id, "idem-1", HeroSMSEmailPurchaseRequest{DomainID: "domain-1", Quantity: 2})
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "IDEMPOTENCY_MISMATCH", apiErr.Code)

	var wg sync.WaitGroup
	results := make(chan *HeroSMSEmailOrderView, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, _, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "idem-2", request)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var ids []string
	for result := range results {
		ids = append(ids, result.ID)
	}
	require.Len(t, ids, 2)
	require.Equal(t, ids[0], ids[1])
	require.Equal(t, int32(2), posts.Load())
}

func TestHeroSMSEmailTimeoutPendingReconciliationDoesNotRepost(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 102, 1000000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "secret"}))
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": "domain-1", "domain": "mail.test", "site": "demo", "stock": 5, "cost": "0.10", "currency": "USD", "currency_code": 840}}, "page": 1, "size": 100, "total": 1})
		case http.MethodPost + " /emails":
			posts.Add(1)
			time.Sleep(100 * time.Millisecond)
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "email-1", "email": "a@mail.test", "cost": "0.10", "currency": "USD", "currency_code": 840})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client {
		client := herosms.NewClient(server.URL, "secret")
		client.TimeoutForTest(10 * time.Millisecond)
		return client
	}, server.URL)
	defer restore()

	order, status, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "timeout-idem", HeroSMSEmailPurchaseRequest{DomainID: "domain-1", Quantity: 1})
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, HeroSMSEmailOrderStatusPurchaseUnknown, order.Status)
	require.Equal(t, int32(1), posts.Load())

	replayed, replayStatus, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "timeout-idem", HeroSMSEmailPurchaseRequest{DomainID: "domain-1", Quantity: 1})
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, replayStatus)
	require.Equal(t, order.ID, replayed.ID)
	require.Equal(t, int32(1), posts.Load())
}

func TestHeroSMSIDORInsufficientBalanceAndRefundIdempotent(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	owner := createHeroSMSTestUser(t, db, 201, 500000)
	other := createHeroSMSTestUser(t, db, 202, 500000)
	activation := HeroSMSEmailActivation{OrderID: "order-1", UserID: owner.Id, Slot: 1, Status: HeroSMSEmailActivationStatusActive, DomainID: "domain-1"}
	require.NoError(t, db.Create(&HeroSMSEmailOrder{ID: "order-1", UserID: owner.Id, Operation: "purchase", IdempotencyKeyHash: "h", RequestPayloadHash: "p", DomainID: "domain-1", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, PriceMultiplier: "10", ReservedUnitCostMicros: 1000000, CustomerUnitPriceMicros: 10000000, ChargeQuota: 5000000, Currency: "USD", CurrencyCode: 840}).Error)
	require.NoError(t, db.Create(&activation).Error)

	_, err := GetHeroSMSEmailActivation(other.Id, activation.ID)
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "NOT_FOUND", apiErr.Code)

	poor := createHeroSMSTestUser(t, db, 203, 1)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "secret"}))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": "domain-1", "domain": "mail.test", "site": "demo", "stock": 5, "cost": "0.10", "currency": "USD", "currency_code": 840}}, "page": 1, "size": 100, "total": 1})
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()
	_, _, err = CreateHeroSMSEmailActivations(context.Background(), poor.Id, "poor-idem", HeroSMSEmailPurchaseRequest{DomainID: "domain-1", Quantity: 1})
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INSUFFICIENT_BALANCE", apiErr.Code)

	refundOrder := HeroSMSEmailOrder{ID: "refund-order", UserID: owner.Id, ChargeQuota: 100}
	refundActivation := HeroSMSEmailActivation{ID: "refund-activation"}
	quotaBefore := owner.Quota
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return heroSMSRefundActivationTx(tx, &refundOrder, &refundActivation, 100, "same")
	}))
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return heroSMSRefundActivationTx(tx, &refundOrder, &refundActivation, 100, "same")
	}))
	var refreshed User
	require.NoError(t, db.First(&refreshed, owner.Id).Error)
	require.Equal(t, quotaBefore+100, refreshed.Quota)
	var ledgers int64
	require.NoError(t, db.Model(&HeroSMSEmailQuotaLedger{}).Where("activation_id = ?", refundActivation.ID).Count(&ledgers).Error)
	require.Equal(t, int64(1), ledgers)
}

func TestHeroSMSSettingsEncryptionRetentionAndClear(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "super-secret", PriceMultiplier: "11"}))
	view := GetHeroSMSSettingsView()
	require.True(t, view.Enabled)
	require.True(t, view.APIKeyConfigured)
	require.Equal(t, "11", view.PriceMultiplier)

	var stored Option
	require.NoError(t, db.Where("key = ?", setting.HeroSMSOptionAPIKey).First(&stored).Error)
	require.NotEqual(t, "super-secret", stored.Value)
	require.True(t, strings.HasPrefix(stored.Value, "v1:"))

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true)}))
	require.Equal(t, "super-secret", setting.HeroSMSAPIKey)

	err := ClearHeroSMSAPIKey()
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(false)}))
	require.NoError(t, ClearHeroSMSAPIKey())
	require.Empty(t, setting.HeroSMSAPIKey)
	var remaining int64
	require.NoError(t, db.Model(&Option{}).Where("key = ?", setting.HeroSMSOptionAPIKey).Count(&remaining).Error)
	require.Zero(t, remaining)
}

func TestHeroSMSSQLiteMigration(t *testing.T) {
	setupHeroSMSTestDB(t)
	models := mainMigrationModels()
	require.NotEmpty(t, models)
	var foundOrder, foundActivation, foundLedger bool
	for _, candidate := range models {
		switch candidate.(type) {
		case *HeroSMSEmailOrder:
			foundOrder = true
		case *HeroSMSEmailActivation:
			foundActivation = true
		case *HeroSMSEmailQuotaLedger:
			foundLedger = true
		}
	}
	require.True(t, foundOrder)
	require.True(t, foundActivation)
	require.True(t, foundLedger)
}

func ptrBool(value bool) *bool { return &value }
