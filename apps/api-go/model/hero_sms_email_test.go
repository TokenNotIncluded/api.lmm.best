package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

func encodeHeroSMSModelTestJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	require.NoError(t, json.NewEncoder(writer).Encode(value))
}

func setupHeroSMSTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	require.NoError(t, db.AutoMigrate(&User{}, &Option{}, &HeroSMSEmailOrder{}, &HeroSMSEmailActivation{}, &HeroSMSEmailQuotaLedger{}, &HeroSMSSMSOrder{}, &HeroSMSSMSQuotaLedger{}, &HeroSMSProviderPurchaseLease{}, &SystemTask{}, &SystemTaskLock{}))
	oldMap := common.OptionMap
	common.OptionMap = map[string]string{}
	InitOptionMap()
	t.Cleanup(func() { common.OptionMap = oldMap })
	oldEnv, hadEnv := os.LookupEnv("HERO_SMS_ENCRYPTION_KEY")
	require.NoError(t, os.Setenv("HERO_SMS_ENCRYPTION_KEY", "test-hero-sms-encryption-key-32-bytes"))
	t.Cleanup(func() {
		if hadEnv {
			require.NoError(t, os.Setenv("HERO_SMS_ENCRYPTION_KEY", oldEnv))
		} else {
			require.NoError(t, os.Unsetenv("HERO_SMS_ENCRYPTION_KEY"))
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
	quoteID, err := encodeHeroSMSQuoteID("demo.com", "mail.test", parsed, decimal.NewFromInt(1))
	require.NoError(t, err)
	return quoteID
}

func testHeroSMSEmailProductsPricing(t *testing.T) {
	setupHeroSMSTestDB(t)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "demo.com", request.URL.Query().Get("site"))
		encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.0000011, 5))
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	products, err := ListHeroSMSEmailProducts(t.Context(), 1, 10, "demo.com")
	require.NoError(t, err)
	require.Len(t, products.Items, 1)
	require.Equal(t, "0.0000011", products.Items[0].CustomerPriceUSD)
	require.Equal(t, 1, products.Items[0].ChargeQuota)
	require.True(t, products.Items[0].Available)
	publicPayload, err := json.Marshal(products)
	require.NoError(t, err)
	for _, internalField := range []string{"cost_usd", "price_multiplier", "currency", "currency_code"} {
		require.NotContains(t, string(publicPayload), `"`+internalField+`"`)
	}
	for _, publicView := range []any{HeroSMSEmailOrderView{}, HeroSMSEmailActivationView{}} {
		publicPayload, err = json.Marshal(publicView)
		require.NoError(t, err)
		for _, internalField := range []string{"cost_usd", "reserved_cost_usd", "price_multiplier", "currency", "currency_code"} {
			require.NotContains(t, string(publicPayload), `"`+internalField+`"`)
		}
	}
	quote, err := decodeHeroSMSQuoteID(products.Items[0].ID)
	require.NoError(t, err)
	require.Equal(t, "demo.com", quote.Site)
	require.Equal(t, "mail.test", quote.Domain)
	require.Equal(t, "0.0000011", quote.CostUSD)

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{PriceMultiplier: "12.5"}))
	products, err = ListHeroSMSEmailProducts(t.Context(), 1, 10, "demo.com")
	require.NoError(t, err)
	require.Equal(t, "0.00001375", products.Items[0].CustomerPriceUSD)

	invalidProducts, err := ListHeroSMSEmailProducts(t.Context(), 1, 10, "https://demo.com/path")
	require.Nil(t, invalidProducts)
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)
}

func testHeroSMSPurchaseRejectsChangedOrTamperedQuote(t *testing.T) {
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
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(cost, 5))
		case http.MethodGet + " /emails":
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
		case http.MethodPost + " /emails":
			posts.Add(1)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	products, err := ListHeroSMSEmailProducts(t.Context(), 1, 10, "demo.com")
	require.NoError(t, err)
	require.Len(t, products.Items, 1)
	changed.Store(true)
	failedOrder, failedStatus, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "changed-quote", HeroSMSEmailPurchaseRequest{DomainID: products.Items[0].ID, Quantity: 1})
	require.Nil(t, failedOrder)
	require.Zero(t, failedStatus)
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "PRICE_CHANGED", apiErr.Code)

	replacement := byte('A')
	if products.Items[0].ID[10] == replacement {
		replacement = 'B'
	}
	tampered := products.Items[0].ID[:10] + string(replacement) + products.Items[0].ID[11:]
	failedOrder, failedStatus, err = CreateHeroSMSEmailActivations(t.Context(), user.Id, "tampered-quote", HeroSMSEmailPurchaseRequest{DomainID: tampered, Quantity: 1})
	require.Nil(t, failedOrder)
	require.Zero(t, failedStatus)
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)
	require.Zero(t, posts.Load())
	var refreshed User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	require.Equal(t, user.Quota, refreshed.Quota)
}

func testHeroSMSExactQuoteRejectsFractionalProviderIncrease(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 108, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.0000011, 5))
		case http.MethodGet + " /emails":
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
		case http.MethodPost + " /emails":
			writer.WriteHeader(http.StatusCreated)
			encodeHeroSMSModelTestJSON(t, writer, heroSMSActivationResponse(88, "fraction@mail.test", 0.0000012, 840))
		case http.MethodDelete + " /emails/88":
			deletes.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	order, status, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "fractional-increase", HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.0000011"), Quantity: 1})
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, HeroSMSEmailOrderStatusFailed, order.Status)
	require.Equal(t, int32(1), deletes.Load())
	var refreshed User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	require.Equal(t, user.Quota, refreshed.Quota)
}

func testHeroSMSEmailPurchaseIdempotencyAndConcurrency(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 101, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
		case http.MethodPost + " /emails":
			seq := posts.Add(1)
			writer.WriteHeader(http.StatusCreated)
			encodeHeroSMSModelTestJSON(t, writer, heroSMSActivationResponse(int(seq), fmt.Sprintf("user-%d@mail.test", seq), 0.10, 840))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	request := HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 1}
	order, status, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "idem-1", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, int32(1), posts.Load())

	replayed, replayStatus, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "idem-1", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, replayStatus)
	require.Equal(t, order.ID, replayed.ID)
	require.Equal(t, int32(1), posts.Load())

	mismatchOrder, mismatchStatus, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "idem-1", HeroSMSEmailPurchaseRequest{DomainID: request.DomainID, Quantity: 2})
	require.Nil(t, mismatchOrder)
	require.Zero(t, mismatchStatus)
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "IDEMPOTENCY_MISMATCH", apiErr.Code)

	var wg sync.WaitGroup
	results := make(chan *HeroSMSEmailOrderView, 2)
	errs := make(chan error, 2)
	runConcurrentPurchase := func(request HeroSMSEmailPurchaseRequest) {
		result, purchaseStatus, purchaseErr := CreateHeroSMSEmailActivations(t.Context(), user.Id, "idem-2", request)
		if purchaseErr == nil && purchaseStatus != http.StatusCreated && purchaseStatus != http.StatusAccepted {
			purchaseErr = fmt.Errorf("unexpected purchase status %d", purchaseStatus)
		}
		if purchaseErr != nil {
			errs <- purchaseErr
			return
		}
		results <- result
	}
	wg.Add(2)
	go func(request HeroSMSEmailPurchaseRequest) {
		defer wg.Done()
		runConcurrentPurchase(request)
	}(request)
	go func(request HeroSMSEmailPurchaseRequest) {
		defer wg.Done()
		runConcurrentPurchase(request)
	}(request)
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

func testHeroSMSEmailBatchPurchaseReconcilesProviderIDs(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 102, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var batchPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.10, 5))
		case http.MethodPost + " /emails/batch":
			batchPosts.Add(1)
			writer.WriteHeader(http.StatusCreated)
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"status": true, "data": []map[string]any{{"site": "demo.com", "domain": "mail.test", "email": "a@mail.test", "status": 1, "cost": 0.10}, {"site": "demo.com", "domain": "mail.test", "email": "b@mail.test", "status": 1, "cost": 0.10}}, "meta": map[string]any{"count": 2}})
		case http.MethodGet + " /emails":
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []map[string]any{{"id": 11, "email": "a@mail.test", "site": "demo.com", "status": 3}, {"id": 12, "email": "b@mail.test", "site": "demo.com", "status": 3}}})
		case http.MethodGet + " /emails/11":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSActivationResponse(11, "a@mail.test", 0.10, 840))
		case http.MethodGet + " /emails/12":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSActivationResponse(12, "b@mail.test", 0.10, 840))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	order, status, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "batch-idem", HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 2})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, HeroSMSEmailOrderStatusCompleted, order.Status)
	require.Len(t, order.Activations, 2)
	require.Equal(t, "a@mail.test", order.Activations[0].Email)
	require.Equal(t, "b@mail.test", order.Activations[1].Email)
	require.Equal(t, int32(1), batchPosts.Load())
}

func testHeroSMSBatchCountMismatchCancelsAndRefunds(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 107, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var purchased atomic.Bool
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.10, 5))
		case http.MethodPost + " /emails/batch":
			purchased.Store(true)
			writer.WriteHeader(http.StatusCreated)
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"status": true, "data": []map[string]any{{"site": "demo.com", "domain": "mail.test", "email": "partial@mail.test", "status": 1, "cost": 0.10}}, "meta": map[string]any{"count": 1}})
		case http.MethodGet + " /emails":
			if purchased.Load() {
				encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []map[string]any{{"id": 77, "email": "partial@mail.test", "site": "demo.com", "status": 3}}})
				return
			}
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
		case http.MethodDelete + " /emails/77":
			deletes.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	failedOrder, status, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "batch-mismatch", HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 2})
	require.Error(t, err)
	require.Nil(t, failedOrder)
	require.Zero(t, status)
	require.Equal(t, int32(1), deletes.Load())
	var refreshed User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	require.Equal(t, user.Quota, refreshed.Quota)
	var stored HeroSMSEmailOrder
	require.NoError(t, db.Where("user_id = ? AND operation = ?", user.Id, "purchase").First(&stored).Error)
	require.Equal(t, HeroSMSEmailOrderStatusFailed, stored.Status)
	require.Equal(t, stored.ChargeQuota, stored.RefundedQuota)
}

func testHeroSMSEmailTimeoutReconcilesWithoutReposting(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 103, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var posts atomic.Int32
	var purchased atomic.Bool
	releasePurchase := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			if purchased.Load() {
				encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []map[string]any{{"id": 1, "email": "a@mail.test", "site": "demo.com", "status": 3}}})
				return
			}
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
		case http.MethodGet + " /emails/1":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSActivationResponse(1, "a@mail.test", 0.10, 840))
		case http.MethodPost + " /emails":
			posts.Add(1)
			purchased.Store(true)
			<-releasePurchase
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
	order, status, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "timeout-idem", request)
	close(releasePurchase)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, HeroSMSEmailOrderStatusPurchaseUnknown, order.Status)
	require.Equal(t, int32(1), posts.Load())

	processed, err := RunHeroSMSEmailReconciliationOnce(t.Context(), 10)
	require.NoError(t, err)
	require.Positive(t, processed)
	replayed, replayStatus, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "timeout-idem", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, replayStatus)
	require.Equal(t, HeroSMSEmailOrderStatusCompleted, replayed.Status)
	require.Equal(t, "a@mail.test", replayed.Activations[0].Email)
	require.Equal(t, order.ID, replayed.ID)
	require.Equal(t, int32(1), posts.Load())
}

func testHeroSMSUpstream500ReconcilesWithoutRefundOrRepost(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 106, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var posts atomic.Int32
	var purchased atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			if purchased.Load() {
				encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []map[string]any{{"id": 2, "email": "server-error@mail.test", "site": "demo.com", "status": 3}}})
				return
			}
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
		case http.MethodGet + " /emails/2":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSActivationResponse(2, "server-error@mail.test", 0.10, 840))
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
	order, status, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "upstream-500-idem", request)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, HeroSMSEmailOrderStatusPurchaseUnknown, order.Status)
	processed, err := RunHeroSMSEmailReconciliationOnce(t.Context(), 10)
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

func testHeroSMSCurrencyMismatchCancelsAndRefunds(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 104, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	var deletes atomic.Int32
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.Method + " " + request.URL.Path {
		case http.MethodGet + " /emails/domains":
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
		case http.MethodPost + " /emails":
			posts.Add(1)
			writer.WriteHeader(http.StatusCreated)
			encodeHeroSMSModelTestJSON(t, writer, heroSMSActivationResponse(1, "a@mail.test", 0.10, 978))
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

	purchaseRequest := HeroSMSEmailPurchaseRequest{DomainID: heroSMSTestQuoteID(t, "0.10"), Quantity: 1}
	order, status, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "currency-idem", purchaseRequest)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, status)
	require.Equal(t, HeroSMSEmailOrderStatusFailed, order.Status)
	require.Equal(t, HeroSMSEmailActivationStatusRefunded, order.Activations[0].Status)
	require.Equal(t, int32(1), deletes.Load())
	var refreshed User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	require.Equal(t, user.Quota, refreshed.Quota)
	replayed, replayStatus, err := CreateHeroSMSEmailActivations(t.Context(), user.Id, "currency-idem", purchaseRequest)
	require.Error(t, err)
	require.Nil(t, replayed)
	require.Zero(t, replayStatus)
	require.Equal(t, int32(1), posts.Load())
}

func testHeroSMSReorderUsesProviderReorderEndpoint(t *testing.T) {
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
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.10, 5))
		case http.MethodGet + " /emails":
			encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
		case http.MethodPost + " /emails/71/reorder":
			reorderHits.Add(1)
			encodeHeroSMSModelTestJSON(t, writer, heroSMSActivationResponse(72, "again@mail.test", 0.10, 840))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	require.NoError(t, db.Model(&HeroSMSEmailActivation{}).Where("id = ?", activation.ID).Update("status", HeroSMSEmailActivationStatusActive).Error)
	activeOrder, activeStatus, err := ReorderHeroSMSEmailActivation(t.Context(), user.Id, activation.ID, "active-reorder", domainID)
	require.Error(t, err)
	require.Nil(t, activeOrder)
	require.Zero(t, activeStatus)
	require.Zero(t, reorderHits.Load())
	require.NoError(t, db.Model(&HeroSMSEmailActivation{}).Where("id = ?", activation.ID).Update("status", HeroSMSEmailActivationStatusCompleted).Error)

	created, status, err := ReorderHeroSMSEmailActivation(t.Context(), user.Id, activation.ID, "reorder-idem", domainID)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, "reorder", created.Operation)
	require.Equal(t, int32(1), reorderHits.Load())
	replayed, replayStatus, err := ReorderHeroSMSEmailActivation(t.Context(), user.Id, activation.ID, "reorder-idem", domainID)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, replayStatus)
	require.Equal(t, created.ID, replayed.ID)
	require.Equal(t, int32(1), reorderHits.Load())
}

func testHeroSMSIDORInsufficientBalanceAndRefundIdempotent(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	owner := createHeroSMSTestUser(t, db, 201, 500_000)
	other := createHeroSMSTestUser(t, db, 202, 500_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	domainID := heroSMSTestQuoteID(t, "0.10")
	order := HeroSMSEmailOrder{ID: "order-1", UserID: owner.Id, Operation: "purchase", IdempotencyKeyHash: "h", RequestPayloadHash: "p", DomainID: domainID, Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, PriceMultiplier: "10", ReservedUnitCostMicros: 1_000_000, CustomerUnitPriceMicros: 10_000_000, ChargeQuota: 5_000_000, Currency: "USD", CurrencyCode: 840}
	require.NoError(t, db.Create(&order).Error)
	activation := HeroSMSEmailActivation{OrderID: order.ID, UserID: owner.Id, Slot: 1, Status: HeroSMSEmailActivationStatusActive, DomainID: domainID, Site: "demo.com", Domain: "mail.test", ChargeQuota: order.ChargeQuota}
	require.NoError(t, db.Create(&activation).Error)

	currentActivation, err := GetCurrentHeroSMSEmailActivation(owner.Id)
	require.NoError(t, err)
	require.Equal(t, activation.ID, currentActivation.ID)
	foreignCurrent, err := GetCurrentHeroSMSEmailActivation(other.Id)
	require.NoError(t, err)
	require.Nil(t, foreignCurrent)

	foreignActivation, err := GetHeroSMSEmailActivation(other.Id, activation.ID)
	require.Nil(t, foreignActivation)
	apiErr := &HeroSMSError{}
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "NOT_FOUND", apiErr.Code)

	poor := createHeroSMSTestUser(t, db, 203, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/emails/domains" {
			encodeHeroSMSModelTestJSON(t, writer, heroSMSDomainResponse(0.10, 5))
			return
		}
		encodeHeroSMSModelTestJSON(t, writer, map[string]any{"data": []any{}})
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()
	poorOrder, poorStatus, err := CreateHeroSMSEmailActivations(t.Context(), poor.Id, "poor-idem", HeroSMSEmailPurchaseRequest{DomainID: domainID, Quantity: 1})
	require.Nil(t, poorOrder)
	require.Zero(t, poorStatus)
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

func testHeroSMSSettingsEncryptionRetentionAndClear(t *testing.T) {
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
	view, err := GetHeroSMSSettingsView()
	require.NoError(t, err)
	require.True(t, view.Enabled)
	require.True(t, view.APIKeyConfigured)
	require.Equal(t, "11", view.PriceMultiplier)

	var stored Option
	require.NoError(t, db.Where("key = ?", setting.HeroSMSOptionAPIKey).First(&stored).Error)
	require.NotEqual(t, "test-super-secret-key", stored.Value)
	require.True(t, strings.HasPrefix(stored.Value, "v1:"))

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true)}))
	persistedKey, err := heroSMSConfiguredAPIKey()
	require.NoError(t, err)
	require.Equal(t, "test-super-secret-key", persistedKey)

	err = ClearHeroSMSAPIKey()
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "INVALID_REQUEST", apiErr.Code)

	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(false)}))
	user := createHeroSMSTestUser(t, db, 307, 1_000_000)
	activeOrder := HeroSMSEmailOrder{ID: "settings-active-order", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "settings-active-hash", RequestPayloadHash: "settings-active-payload", DomainID: "settings-domain", Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, ChargeQuota: 100}
	require.NoError(t, db.Create(&activeOrder).Error)
	activeActivation := HeroSMSEmailActivation{ID: "settings-active-activation", OrderID: activeOrder.ID, UserID: user.Id, Slot: 1, Status: HeroSMSEmailActivationStatusActive, DomainID: "settings-domain", Site: "demo.com", Domain: "mail.test", ChargeQuota: 100}
	require.NoError(t, db.Create(&activeActivation).Error)
	view, err = GetHeroSMSSettingsView()
	require.NoError(t, err)
	require.True(t, view.PendingWork)
	err = UpdateHeroSMSSettings(HeroSMSSettingsUpdate{APIKey: "replacement-secret-key-12345"}) // gitleaks:allow
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "ACTIVE_ORDERS", apiErr.Code)
	err = ClearHeroSMSAPIKey()
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, "ACTIVE_ORDERS", apiErr.Code)
	require.NoError(t, db.Model(&HeroSMSEmailActivation{}).Where("id = ?", activeActivation.ID).Update("status", HeroSMSEmailActivationStatusCompleted).Error)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{APIKey: "replacement-secret-key-12345"})) // gitleaks:allow
	require.NoError(t, ClearHeroSMSAPIKey())
	persistedKey, err = heroSMSConfiguredAPIKey()
	require.NoError(t, err)
	require.Empty(t, persistedKey)
	var remaining int64
	require.NoError(t, db.Model(&Option{}).Where("key = ?", setting.HeroSMSOptionAPIKey).Count(&remaining).Error)
	require.Zero(t, remaining)
}

func testHeroSMSAbandonedProviderIntentRefundsWithoutUpstreamCall(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 109, 1_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		t.Fatal("abandoned provider intent must not call HeroSMS")
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	order := HeroSMSEmailOrder{ID: "abandoned-intent", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "abandoned-hash", RequestPayloadHash: "abandoned-payload", DomainID: "quote", Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusPendingProvider, LastErrorCode: "PROVIDER_INTENT_PENDING", LastErrorMessage: "provider purchase intent is reserved but not started", ChargeQuota: 100, Currency: "USD", CurrencyCode: 840}
	activations := []HeroSMSEmailActivation{{ID: "abandoned-activation", UserID: user.Id, Slot: 1, Status: HeroSMSEmailActivationStatusPendingProvider, DomainID: order.DomainID, Site: order.Site, Domain: order.Domain, ChargeQuota: 100}}
	newQuota, err := reserveHeroSMSEmailQuota(&order, activations)
	require.NoError(t, err)
	require.Equal(t, 900, newQuota)

	processed, err := RunHeroSMSEmailReconciliationOnce(t.Context(), 20)
	require.NoError(t, err)
	require.Positive(t, processed)
	var refreshed User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	require.Equal(t, user.Quota, refreshed.Quota)
	var stored HeroSMSEmailOrder
	require.NoError(t, db.Where("id = ?", order.ID).First(&stored).Error)
	require.Equal(t, HeroSMSEmailOrderStatusFailed, stored.Status)
	require.Equal(t, stored.ChargeQuota, stored.RefundedQuota)
}

func testHeroSMSReconciliationPollsActiveActivationUntilCodeArrives(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 300, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	domainID := heroSMSTestQuoteID(t, "0.10")
	order := HeroSMSEmailOrder{ID: "poll-order", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "poll-hash", RequestPayloadHash: "poll-payload", DomainID: domainID, Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, ReservedUnitCostMicros: 100_000, ChargeQuota: 100, Currency: "USD", CurrencyCode: 840}
	require.NoError(t, db.Create(&order).Error)
	providerID := "55"
	activation := HeroSMSEmailActivation{ID: "poll-activation", OrderID: order.ID, UserID: user.Id, Slot: 1, Status: HeroSMSEmailActivationStatusActive, DomainID: domainID, Site: "demo.com", Domain: "mail.test", ProviderID: &providerID, ChargeQuota: 100}
	require.NoError(t, db.Create(&activation).Error)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodGet+" /emails/55", request.Method+" "+request.URL.Path)
		encodeHeroSMSModelTestJSON(t, writer, map[string]any{"status": true, "data": map[string]any{"id": 55, "site": "demo.com", "email": "code@mail.test", "status": 5, "cost": 0.10, "currency": 840, "value": "582914", "message": "Your code is 582914"}})
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(false)}))

	processed, err := RunHeroSMSEmailReconciliationOnce(t.Context(), 20)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	view, err := GetHeroSMSEmailActivation(user.Id, activation.ID)
	require.NoError(t, err)
	require.Equal(t, HeroSMSEmailActivationStatusCompleted, view.Status)
	require.Equal(t, "582914", view.Code)
	require.Equal(t, "Your code is 582914", view.Message)
}

func testHeroSMSNumericTerminalStatusStopsPolling(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 306, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	order := HeroSMSEmailOrder{ID: "numeric-status-order", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "numeric-status-hash", RequestPayloadHash: "numeric-status-payload", DomainID: "quote", Site: "demo.com", Domain: "mail.test", Quantity: 1, Status: HeroSMSEmailOrderStatusCompleted, ReservedUnitCostDecimal: "0.1", ChargeQuota: 100, Currency: "USD", CurrencyCode: 840}
	require.NoError(t, db.Create(&order).Error)
	providerID := "56"
	activation := HeroSMSEmailActivation{ID: "numeric-status-activation", OrderID: order.ID, UserID: user.Id, Slot: 1, Status: HeroSMSEmailActivationStatusActive, DomainID: order.DomainID, Site: order.Site, Domain: order.Domain, ProviderID: &providerID, ChargeQuota: 100}
	require.NoError(t, db.Create(&activation).Error)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		encodeHeroSMSModelTestJSON(t, writer, map[string]any{"status": true, "data": map[string]any{"id": 56, "site": "demo.com", "email": "terminal@mail.test", "status": 5, "cost": 0.1, "currency": 840}})
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	_, err := RunHeroSMSEmailReconciliationOnce(t.Context(), 20)
	require.NoError(t, err)
	view, err := GetHeroSMSEmailActivation(user.Id, activation.ID)
	require.NoError(t, err)
	require.Equal(t, HeroSMSEmailActivationStatusCompleted, view.Status)
	require.Empty(t, view.Code)
	cancelled, err := CancelHeroSMSEmailActivation(t.Context(), user.Id, activation.ID)
	require.Error(t, err)
	require.Nil(t, cancelled)
}

func testHeroSMSRefreshCannotResurrectCancelledActivation(t *testing.T) {
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

func testHeroSMSOrderRefundLedgerIsIdempotent(t *testing.T) {
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

func testHeroSMSReconciliationRotatesAcrossActiveRows(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 308, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	order := HeroSMSEmailOrder{ID: "rotation-order", UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "rotation-hash", RequestPayloadHash: "rotation-payload", DomainID: "quote", Site: "demo.com", Domain: "mail.test", Quantity: 15, Status: HeroSMSEmailOrderStatusCompleted, ReservedUnitCostDecimal: "0.1", ChargeQuota: 1500, Currency: "USD", CurrencyCode: 840}
	require.NoError(t, db.Create(&order).Error)
	for index := 1; index <= 15; index++ {
		providerID := strconv.Itoa(1000 + index)
		activation := HeroSMSEmailActivation{ID: fmt.Sprintf("rotation-%02d", index), OrderID: order.ID, UserID: user.Id, Slot: index, Status: HeroSMSEmailActivationStatusActive, DomainID: order.DomainID, Site: order.Site, Domain: order.Domain, ProviderID: &providerID, ChargeQuota: 100, CreatedAt: int64(index), UpdatedAt: int64(index)}
		require.NoError(t, db.Create(&activation).Error)
	}
	var hitsMu sync.Mutex
	hits := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerID := strings.TrimPrefix(request.URL.Path, "/emails/")
		hitsMu.Lock()
		hits[providerID]++
		hitsMu.Unlock()
		encodeHeroSMSModelTestJSON(t, writer, map[string]any{"status": true, "data": map[string]any{"id": providerID, "site": "demo.com", "email": providerID + "@mail.test", "status": "WAIT", "cost": 0.1, "currency": 840}})
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()

	for range 2 {
		processed, err := RunHeroSMSEmailReconciliationOnce(t.Context(), 10)
		require.NoError(t, err)
		require.Equal(t, 10, processed)
	}
	hitsMu.Lock()
	defer hitsMu.Unlock()
	require.Len(t, hits, 15)
}

func testHeroSMSProviderPurchaseLeaseSerializesRequests(t *testing.T) {
	setupHeroSMSTestDB(t)
	firstRelease, err := acquireHeroSMSProviderPurchaseLease(t.Context())
	require.NoError(t, err)
	acquired := make(chan func(), 1)
	errors := make(chan error, 1)
	go func() {
		release, acquireErr := acquireHeroSMSProviderPurchaseLease(t.Context())
		if acquireErr != nil {
			errors <- acquireErr
			return
		}
		acquired <- release
	}()

	wait := time.NewTimer(75 * time.Millisecond)
	select {
	case release := <-acquired:
		release()
		t.Fatal("second provider purchase acquired the lease before the first released it")
	case acquireErr := <-errors:
		t.Fatalf("second provider purchase failed while waiting: %v", acquireErr)
	case <-wait.C:
	}
	firstRelease()
	select {
	case release := <-acquired:
		release()
	case acquireErr := <-errors:
		t.Fatalf("second provider purchase failed after release: %v", acquireErr)
	case <-time.After(time.Second):
		t.Fatal("second provider purchase did not acquire the released lease")
	}
}

func testHeroSMSSQLiteMigration(t *testing.T) {
	setupHeroSMSTestDB(t)
	models := mainMigrationModels()
	require.NotEmpty(t, models)
	var foundOrder, foundActivation, foundLedger, foundLease bool
	for _, candidate := range models {
		switch candidate.(type) {
		case *HeroSMSEmailOrder:
			foundOrder = true
		case *HeroSMSEmailActivation:
			foundActivation = true
		case *HeroSMSEmailQuotaLedger:
			foundLedger = true
		case *HeroSMSProviderPurchaseLease:
			foundLease = true
		}
	}
	require.True(t, foundOrder)
	require.True(t, foundActivation)
	require.True(t, foundLedger)
	require.True(t, foundLease)
}

func ptrBool(value bool) *bool { return &value }

// pi-lens-ignore: ast-grep:go-test-functions
func TestHeroSMSEmailFeature(t *testing.T) {
	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "HeroSMSEmailProductsPricing", run: testHeroSMSEmailProductsPricing},
		{name: "HeroSMSPurchaseRejectsChangedOrTamperedQuote", run: testHeroSMSPurchaseRejectsChangedOrTamperedQuote},
		{name: "HeroSMSExactQuoteRejectsFractionalProviderIncrease", run: testHeroSMSExactQuoteRejectsFractionalProviderIncrease},
		{name: "HeroSMSEmailPurchaseIdempotencyAndConcurrency", run: testHeroSMSEmailPurchaseIdempotencyAndConcurrency},
		{name: "HeroSMSEmailBatchPurchaseReconcilesProviderIDs", run: testHeroSMSEmailBatchPurchaseReconcilesProviderIDs},
		{name: "HeroSMSBatchCountMismatchCancelsAndRefunds", run: testHeroSMSBatchCountMismatchCancelsAndRefunds},
		{name: "HeroSMSEmailTimeoutReconcilesWithoutReposting", run: testHeroSMSEmailTimeoutReconcilesWithoutReposting},
		{name: "HeroSMSUpstream500ReconcilesWithoutRefundOrRepost", run: testHeroSMSUpstream500ReconcilesWithoutRefundOrRepost},
		{name: "HeroSMSCurrencyMismatchCancelsAndRefunds", run: testHeroSMSCurrencyMismatchCancelsAndRefunds},
		{name: "HeroSMSReorderUsesProviderReorderEndpoint", run: testHeroSMSReorderUsesProviderReorderEndpoint},
		{name: "HeroSMSIDORInsufficientBalanceAndRefundIdempotent", run: testHeroSMSIDORInsufficientBalanceAndRefundIdempotent},
		{name: "HeroSMSSettingsEncryptionRetentionAndClear", run: testHeroSMSSettingsEncryptionRetentionAndClear},
		{name: "HeroSMSAbandonedProviderIntentRefundsWithoutUpstreamCall", run: testHeroSMSAbandonedProviderIntentRefundsWithoutUpstreamCall},
		{name: "HeroSMSReconciliationPollsActiveActivationUntilCodeArrives", run: testHeroSMSReconciliationPollsActiveActivationUntilCodeArrives},
		{name: "HeroSMSNumericTerminalStatusStopsPolling", run: testHeroSMSNumericTerminalStatusStopsPolling},
		{name: "HeroSMSRefreshCannotResurrectCancelledActivation", run: testHeroSMSRefreshCannotResurrectCancelledActivation},
		{name: "HeroSMSOrderRefundLedgerIsIdempotent", run: testHeroSMSOrderRefundLedgerIsIdempotent},
		{name: "HeroSMSReconciliationRotatesAcrossActiveRows", run: testHeroSMSReconciliationRotatesAcrossActiveRows},
		{name: "HeroSMSProviderPurchaseLeaseSerializesRequests", run: testHeroSMSProviderPurchaseLeaseSerializesRequests},
		{name: "HeroSMSSQLiteMigration", run: testHeroSMSSQLiteMigration},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, testCase.run)
	}
}
