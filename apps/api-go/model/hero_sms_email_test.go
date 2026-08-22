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
	"github.com/shopspring/decimal"
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

func heroSMSDomainResponse(cost any, count int) map[string]any {
	return map[string]any{"data": []map[string]any{{"name": "mail.test", "cost": cost, "count": count}}}
}

func heroSMSActivationResponse(id int, email string, cost any, currency int) map[string]any {
	return map[string]any{"status": true, "data": map[string]any{"id": id, "site": "demo.com", "email": email, "status": 3, "cost": cost, "currency": currency}}
}

func heroSMSTestQuoteID(t *testing.T, cost string) string {
	t.Helper()
	parsed, err := decimal.NewFromString(cost)
	require.NoError(t, err)
	quoteID, err := encodeHeroSMSQuoteID("demo.com", "mail.test", parsed, decimal.NewFromInt(10))
	require.NoError(t, err)
	return quoteID
}

func TestHeroSMSEmailProductsPricing(t *testing.T) {
	setupHeroSMSTestDB(t)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "demo.com", request.URL.Query().Get("site"))
		_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(0.0000011, 5))
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	products, err := ListHeroSMSEmailProducts(context.Background(), 1, 10, "demo.com")
	require.NoError(t, err)
	require.Len(t, products.Items, 1)
	require.Equal(t, "10", products.PriceMultiplier)
	require.Equal(t, "0.0000011", products.Items[0].CostUSD)
	require.Equal(t, "0.000011", products.Items[0].CustomerPriceUSD)
	require.Equal(t, 6, products.Items[0].ChargeQuota)
	require.True(t, products.Items[0].Available)
	quote, err := decodeHeroSMSQuoteID(products.Items[0].ID)
	require.NoError(t, err)
	require.Equal(t, "demo.com", quote.Site)
	require.Equal(t, "mail.test", quote.Domain)
	require.Equal(t, "0.0000011", quote.CostUSD)

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{PriceMultiplier: "12.5"}))
	products, err = ListHeroSMSEmailProducts(context.Background(), 1, 10, "demo.com")
	require.NoError(t, err)
	require.Equal(t, "12.5", products.PriceMultiplier)

	_, err = ListHeroSMSEmailProducts(context.Background(), 1, 10, "https://demo.com/path")
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)
}

func TestHeroSMSPurchaseRejectsChangedOrTamperedQuote(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 100, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var changed atomic.Bool
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			cost := 0.10
			if changed.Load() {
				cost = 0.20
			}
			_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(cost, 5))
		case http.MethodGet + " /emails":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []any{}})
		case http.MethodPost + " /emails":
			posts.Add(1)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	products, err := ListHeroSMSEmailProducts(context.Background(), 1, 10, "demo.com")
	require.NoError(t, err)
	require.Len(t, products.Items, 1)
	changed.Store(true)
	_, _, err = CreateHeroSMSEmailActivations(context.Background(), user.Id, "changed-quote", HeroSMSEmailPurchaseRequest{DomainID: products.Items[0].ID, Quantity: 1})
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "PRICE_CHANGED", apiErr.Code)

	replacement := byte('A')
	if products.Items[0].ID[10] == replacement {
		replacement = 'B'
	}
	tampered := products.Items[0].ID[:10] + string(replacement) + products.Items[0].ID[11:]
	_, _, err = CreateHeroSMSEmailActivations(context.Background(), user.Id, "tampered-quote", HeroSMSEmailPurchaseRequest{DomainID: tampered, Quantity: 1})
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)
	require.Zero(t, posts.Load())
	var refreshed User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	require.Equal(t, user.Quota, refreshed.Quota)
}

func TestHeroSMSEmailPurchaseIdempotencyAndConcurrency(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 101, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []any{}})
		case http.MethodPost + " /emails":
			seq := posts.Add(1)
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(heroSMSActivationResponse(int(seq), fmt.Sprintf("user-%d@mail.test", seq), 0.10, 840))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	request := HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 1}
	order, status, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "idem-1", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, int32(1), posts.Load())

	replayed, replayStatus, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "idem-1", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, replayStatus)
	require.Equal(t, order.ID, replayed.ID)
	require.Equal(t, int32(1), posts.Load())

	_, _, err = CreateHeroSMSEmailActivations(context.Background(), user.Id, "idem-1", HeroSMSEmailPurchaseRequest{DomainID: request.DomainID, Quantity: 2})
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "IDEMPOTENCY_MISMATCH", apiErr.Code)

	var wg sync.WaitGroup
	results := make(chan *HeroSMSEmailOrderView, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, _, purchaseErr := CreateHeroSMSEmailActivations(context.Background(), user.Id, "idem-2", request)
			if purchaseErr != nil {
				errs <- purchaseErr
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for purchaseErr := range errs {
		require.NoError(t, purchaseErr)
	}
	var ids []string
	for result := range results {
		ids = append(ids, result.ID)
	}
	require.Len(t, ids, 2)
	require.Equal(t, ids[0], ids[1])
	require.Equal(t, int32(2), posts.Load())
}

func TestHeroSMSEmailBatchPurchaseReconcilesProviderIDs(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 102, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var batchPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(0.10, 5))
		case http.MethodPost + " /emails/batch":
			batchPosts.Add(1)
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(map[string]any{"status": true, "data": []map[string]any{{"site": "demo.com", "domain": "mail.test", "email": "a@mail.test", "status": 1, "cost": 0.10}, {"site": "demo.com", "domain": "mail.test", "email": "b@mail.test", "status": 1, "cost": 0.10}}})
		case http.MethodGet + " /emails":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": 11, "email": "a@mail.test", "site": "demo.com", "status": 3}, {"id": 12, "email": "b@mail.test", "site": "demo.com", "status": 3}}})
		case http.MethodGet + " /emails/11":
			_ = json.NewEncoder(writer).Encode(heroSMSActivationResponse(11, "a@mail.test", 0.10, 840))
		case http.MethodGet + " /emails/12":
			_ = json.NewEncoder(writer).Encode(heroSMSActivationResponse(12, "b@mail.test", 0.10, 840))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	order, status, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "batch-idem", HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 2})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, HeroSMSEmailOrderStatusCompleted, order.Status)
	require.Len(t, order.Activations, 2)
	require.Equal(t, "a@mail.test", order.Activations[0].Email)
	require.Equal(t, "b@mail.test", order.Activations[1].Email)
	require.Equal(t, int32(1), batchPosts.Load())
}

func TestHeroSMSEmailTimeoutReconcilesWithoutReposting(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 103, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var posts atomic.Int32
	var purchased atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			if purchased.Load() {
				_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": 1, "email": "a@mail.test", "site": "demo.com", "status": 3}}})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []any{}})
		case http.MethodGet + " /emails/1":
			_ = json.NewEncoder(writer).Encode(heroSMSActivationResponse(1, "a@mail.test", 0.10, 840))
		case http.MethodPost + " /emails":
			posts.Add(1)
			purchased.Store(true)
			time.Sleep(100 * time.Millisecond)
			_ = json.NewEncoder(writer).Encode(heroSMSActivationResponse(1, "a@mail.test", 0.10, 840))
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

	request := HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 1}
	order, status, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "timeout-idem", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, HeroSMSEmailOrderStatusPurchaseUnknown, order.Status)
	require.Equal(t, int32(1), posts.Load())

	processed, err := RunHeroSMSEmailReconciliationOnce(context.Background(), 10)
	require.NoError(t, err)
	require.Positive(t, processed)
	replayed, replayStatus, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "timeout-idem", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, replayStatus)
	require.Equal(t, HeroSMSEmailOrderStatusCompleted, replayed.Status)
	require.Equal(t, "a@mail.test", replayed.Activations[0].Email)
	require.Equal(t, order.ID, replayed.ID)
	require.Equal(t, int32(1), posts.Load())
}

func TestHeroSMSUpstream500ReconcilesWithoutRefundOrRepost(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 106, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var posts atomic.Int32
	var purchased atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			if purchased.Load() {
				_ = json.NewEncoder(writer).Encode(map[string]any{"data": []map[string]any{{"id": 2, "email": "server-error@mail.test", "site": "demo.com", "status": 3}}})
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []any{}})
		case http.MethodGet + " /emails/2":
			_ = json.NewEncoder(writer).Encode(heroSMSActivationResponse(2, "server-error@mail.test", 0.10, 840))
		case http.MethodPost + " /emails":
			posts.Add(1)
			purchased.Store(true)
			writer.WriteHeader(http.StatusInternalServerError)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	request := HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 1}
	order, status, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "upstream-500-idem", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, HeroSMSEmailOrderStatusPurchaseUnknown, order.Status)
	processed, err := RunHeroSMSEmailReconciliationOnce(context.Background(), 10)
	require.NoError(t, err)
	require.Positive(t, processed)
	resolved, err := GetHeroSMSEmailOrderView(user.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, HeroSMSEmailOrderStatusCompleted, resolved.Status)
	require.Equal(t, int32(1), posts.Load())
	var refreshed User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	require.Equal(t, user.Quota-order.ChargeQuota, refreshed.Quota)
}

func TestHeroSMSCurrencyMismatchCancelsAndRefunds(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 104, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []any{}})
		case http.MethodPost + " /emails":
			writer.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(writer).Encode(heroSMSActivationResponse(1, "a@mail.test", 0.10, 978))
		case http.MethodDelete + " /emails/1":
			deletes.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	order, status, err := CreateHeroSMSEmailActivations(context.Background(), user.Id, "currency-idem", HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 1})
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, HeroSMSEmailOrderStatusFailed, order.Status)
	require.Equal(t, HeroSMSEmailActivationStatusRefunded, order.Activations[0].Status)
	require.Equal(t, int32(1), deletes.Load())
	var refreshed User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	require.Equal(t, user.Quota, refreshed.Quota)
}

func TestHeroSMSReorderUsesProviderReorderEndpoint(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 105, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	domainID := heroSMSTestQuoteID(t, "0.10")
	order := HeroSMSEmailOrder{ID: "source-order", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "source-hash", RequestPayloadHash: "source-payload", DomainID: domainID, Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, PriceMultiplier: "10", ReservedUnitCostMicros: 100_000, CustomerUnitPriceMicros: 1_000_000, ChargeQuota: 500_000, Currency: "USD", CurrencyCode: 840}
	require.NoError(t, db.Create(&order).Error)
	providerID := "71"
	activation := HeroSMSEmailActivation{OrderID: order.ID, UserID: user.Id, Slot: 1, Status: HeroSMSEmailActivationStatusCompleted, DomainID: domainID, Site: "demo.com", Domain: "mail.test", ProviderID: &providerID, ChargeQuota: 500_000}
	require.NoError(t, db.Create(&activation).Error)
	var reorderHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			_ = json.NewEncoder(writer).Encode(map[string]any{"data": []any{}})
		case http.MethodPost + " /emails/71/reorder":
			reorderHits.Add(1)
			_ = json.NewEncoder(writer).Encode(heroSMSActivationResponse(72, "again@mail.test", 0.10, 840))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	created, status, err := ReorderHeroSMSEmailActivation(context.Background(), user.Id, activation.ID, "reorder-idem")
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, "reorder", created.Operation)
	require.Equal(t, int32(1), reorderHits.Load())
	replayed, replayStatus, err := ReorderHeroSMSEmailActivation(context.Background(), user.Id, activation.ID, "reorder-idem")
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, replayStatus)
	require.Equal(t, created.ID, replayed.ID)
	require.Equal(t, int32(1), reorderHits.Load())
}

func TestHeroSMSIDORInsufficientBalanceAndRefundIdempotent(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	owner := createHeroSMSTestUser(t, db, 201, 500_000)
	other := createHeroSMSTestUser(t, db, 202, 500_000)
	domainID := heroSMSTestQuoteID(t, "0.10")
	order := HeroSMSEmailOrder{ID: "order-1", UserID: owner.Id, Operation: "purchase", IdempotencyKeyHash: "h", RequestPayloadHash: "p", DomainID: domainID, Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, PriceMultiplier: "10", ReservedUnitCostMicros: 1_000_000, CustomerUnitPriceMicros: 10_000_000, ChargeQuota: 5_000_000, Currency: "USD", CurrencyCode: 840}
	require.NoError(t, db.Create(&order).Error)
	activation := HeroSMSEmailActivation{OrderID: order.ID, UserID: owner.Id, Slot: 1, Status: HeroSMSEmailActivationStatusActive, DomainID: domainID, Site: "demo.com", Domain: "mail.test", ChargeQuota: order.ChargeQuota}
	require.NoError(t, db.Create(&activation).Error)

	_, err := GetHeroSMSEmailActivation(other.Id, activation.ID)
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "NOT_FOUND", apiErr.Code)

	poor := createHeroSMSTestUser(t, db, 203, 1)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/emails/domains" {
			_ = json.NewEncoder(writer).Encode(heroSMSDomainResponse(0.10, 5))
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": []any{}})
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()
	_, _, err = CreateHeroSMSEmailActivations(context.Background(), poor.Id, "poor-idem", HeroSMSEmailPurchaseRequest{DomainID: domainID, Quantity: 1})
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INSUFFICIENT_BALANCE", apiErr.Code)

	refundOrder := HeroSMSEmailOrder{ID: "refund-order", UserID: owner.Id, Operation: "purchase", IdempotencyKeyHash: "refund-hash", RequestPayloadHash: "refund-payload", DomainID: domainID, Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, ChargeQuota: 100}
	require.NoError(t, db.Create(&refundOrder).Error)
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
	apiErr := &HeroSMSError{}
	err := UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true)})
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "NOT_CONFIGURED", apiErr.Code)
	err = UpdateHeroSMSSettings(HeroSMSSettingsUpdate{APIKey: "short"})
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)
	err = UpdateHeroSMSSettings(HeroSMSSettingsUpdate{PriceMultiplier: "1000.0000001"})
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-super-secret-key", PriceMultiplier: "11"}))
	view := GetHeroSMSSettingsView()
	require.True(t, view.Enabled)
	require.True(t, view.APIKeyConfigured)
	require.Equal(t, "11", view.PriceMultiplier)

	var stored Option
	require.NoError(t, db.Where("key = ?", setting.HeroSMSOptionAPIKey).First(&stored).Error)
	require.NotEqual(t, "test-super-secret-key", stored.Value)
	require.True(t, strings.HasPrefix(stored.Value, "v1:"))

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true)}))
	require.Equal(t, "test-super-secret-key", setting.HeroSMSAPIKey)

	err = ClearHeroSMSAPIKey()
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(false)}))
	require.NoError(t, ClearHeroSMSAPIKey())
	require.Empty(t, setting.HeroSMSAPIKey)
	var remaining int64
	require.NoError(t, db.Model(&Option{}).Where("key = ?", setting.HeroSMSOptionAPIKey).Count(&remaining).Error)
	require.Zero(t, remaining)
}

func TestHeroSMSRefreshCannotResurrectCancelledActivation(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 301, 1_000_000)
	domainID := heroSMSTestQuoteID(t, "0.10")
	order := HeroSMSEmailOrder{ID: "cas-order", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "cas-hash", RequestPayloadHash: "cas-payload", DomainID: domainID, Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, ReservedUnitCostMicros: 100_000, ChargeQuota: 100, Currency: "USD", CurrencyCode: 840}
	require.NoError(t, db.Create(&order).Error)
	providerID := "501"
	activation := HeroSMSEmailActivation{ID: "cas-activation", OrderID: order.ID, UserID: user.Id, Slot: 1, Status: HeroSMSEmailActivationStatusCancelled, DomainID: domainID, Site: "demo.com", Domain: "mail.test", ProviderID: &providerID, ChargeQuota: 100, CancelReason: HeroSMSEmailCancelReasonUser}
	require.NoError(t, db.Create(&activation).Error)

	err := persistHeroSMSEmailRecord(&activation, &herosms.EmailRecord{ID: providerID, Email: "late@mail.test", CostUSD: microsToDecimal(100_000), CurrencyCode: 840})
	require.NoError(t, err)
	var refreshed HeroSMSEmailActivation
	require.NoError(t, db.First(&refreshed, "id = ?", activation.ID).Error)
	require.Equal(t, HeroSMSEmailActivationStatusCancelled, refreshed.Status)
}

func TestHeroSMSOrderRefundLedgerIsIdempotent(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 302, 900)
	order := HeroSMSEmailOrder{ID: "order-refund-idem", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "order-refund-hash", RequestPayloadHash: "order-refund-payload", DomainID: "domain", Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusFailed, ChargeQuota: 100}
	require.NoError(t, db.Create(&order).Error)
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			return heroSMSRefundOrderTx(tx, &order, 100, "failure")
		}))
	}
	var refreshedUser User
	require.NoError(t, db.First(&refreshedUser, user.Id).Error)
	require.Equal(t, 1000, refreshedUser.Quota)
	var refreshedOrder HeroSMSEmailOrder
	require.NoError(t, db.First(&refreshedOrder, "id = ?", order.ID).Error)
	require.Equal(t, 100, refreshedOrder.RefundedQuota)
	var ledgerCount int64
	require.NoError(t, db.Model(&HeroSMSEmailQuotaLedger{}).Where("order_id = ? AND entry_type = ?", order.ID, HeroSMSEmailLedgerRefund).Count(&ledgerCount).Error)
	require.Equal(t, int64(1), ledgerCount)
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
