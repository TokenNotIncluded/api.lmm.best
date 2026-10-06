package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func merchantStoreTestCreditBasis(t *testing.T) {
	t.Helper()
	old, oldErr := common.CreditsPerUSD()
	legacy, _ := common.LegacyPricingQuotaPerUnit()
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	t.Cleanup(func() {
		if oldErr == nil {
			_ = common.SetCreditCurrencyBasis(old, legacy)
		} else {
			common.ClearCreditsPerUSD()
		}
	})
}

func TestMerchantStoreQuoteUsesImmutableCreditsAndRealCurrency(t *testing.T) {
	merchantStoreTestCreditBasis(t)
	for _, test := range []struct {
		quota      int64
		unit, rate string
		minor      int64
	}{
		{500000, "USD", "1", 100}, {500000, "CNY", "6.710363", 672},
		{1000000, "CNY", "6.710363", 1343}, {500000, "LDC", "20", 2000},
		{1, "USD", "1", 1}, {1, "CNY", "6.710363", 1},
	} {
		minor, err := merchantStoreQuotePayment(test.quota, test.unit, test.rate)
		require.NoError(t, err)
		require.Equal(t, test.minor, minor)
	}
	for _, test := range []struct{ unit, rate string }{{"USD", "6.7"}, {"CNY", ""}, {"LDC", "0"}, {"EUR", "1"}, {"CNY", "1e2"}, {"CNY", "-6.7"}} {
		_, err := merchantStoreQuotePayment(500000, test.unit, test.rate)
		require.Error(t, err)
	}
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(74626), decimal.NewFromInt(500000)))
	_, err := merchantStoreQuotePayment(500000, "USD", "1")
	require.Error(t, err, "a legacy exchange-rate-scaled credit anchor must fail closed")
}

func TestMerchantStoreMoneyRejectsAmbiguousUnits(t *testing.T) {
	for _, value := range []string{"0", "-1", "+1", "1e2", "1.001", "1,000.00", " 1.00", "", "99999999999999999.00"} {
		_, err := merchantStoreMoneyToMinor(value)
		require.Error(t, err, value)
	}
	minor, err := merchantStoreMoneyToMinor("6.72")
	require.NoError(t, err)
	require.EqualValues(t, 672, minor)
}

func TestMerchantStoreLinuxDORequiresOfficialGatewayAndExplicitRootRate(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	oldAddress, oldID, oldKey, oldMethods := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.PayAddress = oldAddress
		operation_setting.EpayId = oldID
		operation_setting.EpayKey = oldKey
		operation_setting.PayMethods = oldMethods
	})
	operation_setting.PayAddress = "https://credit.linux.do/epay"
	operation_setting.EpayId = "123"
	operation_setting.EpayKey = "platform-private-key"
	operation_setting.PayMethods = nil
	_, err := merchantStorePlatformGatewayConfig(MerchantStorePlatformLinuxDO)
	require.Error(t, err, "blank rate cannot borrow a CNY exchange rate")
	config, err := model.GetMerchantStoreConfig()
	require.NoError(t, err)
	config.LinuxDOUnitsPerUSD = "20"
	require.NoError(t, model.SetMerchantStoreConfig(f.root.Id, config))
	method, err := merchantStorePlatformGatewayConfig(MerchantStorePlatformLinuxDO)
	require.NoError(t, err)
	require.Equal(t, "LDC", method.Currency)
	require.Equal(t, "20", method.UnitsPerUSD)
	operation_setting.PayMethods = []map[string]string{{"type": "epay", "settlement_unit": "LDC", "settlement_units_per_usd": "30"}}
	method, err = merchantStorePlatformGatewayConfig(MerchantStorePlatformLinuxDO)
	require.NoError(t, err)
	require.Equal(t, "20", method.UnitsPerUSD, "the explicit shop root rate wins")
	operation_setting.PayAddress = "https://some-fiat-gateway.example.com"
	_, err = merchantStorePlatformGatewayConfig(MerchantStorePlatformLinuxDO)
	require.Error(t, err, "a generic ePay provider is never assumed to charge LDC")
}

func merchantStoreTestEpayOrder() (*model.MerchantStoreOrder, merchantStorePaymentContext) {
	order := &model.MerchantStoreOrder{ID: strings.Repeat("a", 64), TradeNo: "MS" + strings.Repeat("b", 30), BuyerID: 1, SellerID: 2, ProductID: "product", PaymentMethod: MerchantStoreExternalEpay, AmountMinor: 672, Currency: "CNY"}
	return order, merchantStorePaymentContext{Provider: order.PaymentMethod, Config: merchantStoreGatewayConfig{PartnerID: "123", Key: "merchant-secret-key", PaymentType: "alipay", Currency: "CNY"}, AmountMinor: 672, Currency: "CNY"}
}

func merchantStoreTestEpaySigned(order *model.MerchantStoreOrder, context merchantStorePaymentContext, edit func(map[string]string)) url.Values {
	fields := map[string]string{"pid": context.Config.PartnerID, "trade_no": "PROVIDER_123", "out_trade_no": order.TradeNo, "type": context.Config.PaymentType, "money": merchantStoreMinorMoney(order.AmountMinor), "trade_status": "TRADE_SUCCESS", "currency": order.Currency}
	if edit != nil {
		edit(fields)
	}
	fields = epay.GenerateParams(fields, context.Config.Key)
	values := url.Values{}
	for key, value := range fields {
		values.Set(key, value)
	}
	return values
}

func TestMerchantStoreEpaySignatureAndFrozenQuote(t *testing.T) {
	order, payment := merchantStoreTestEpayOrder()
	tradeID, err := validateMerchantStoreEpayCallback(order, payment, merchantStoreTestEpaySigned(order, payment, nil))
	require.NoError(t, err)
	require.Equal(t, "PROVIDER_123", tradeID)
	for field, value := range map[string]string{"money": "0.01", "currency": "USD", "pid": "999", "out_trade_no": "wrong", "type": "wxpay"} {
		t.Run(field, func(t *testing.T) {
			_, err := validateMerchantStoreEpayCallback(order, payment, merchantStoreTestEpaySigned(order, payment, func(fields map[string]string) { fields[field] = value }))
			require.ErrorIs(t, err, ErrMerchantStorePaymentVerification)
		})
	}
	values := merchantStoreTestEpaySigned(order, payment, nil)
	values.Set("money", "0.01")
	_, err = validateMerchantStoreEpayCallback(order, payment, values)
	require.ErrorIs(t, err, ErrMerchantStorePaymentVerification)
	values = merchantStoreTestEpaySigned(order, payment, nil)
	values.Add("money", "6.72")
	_, err = validateMerchantStoreEpayCallback(order, payment, values)
	require.Error(t, err)
	_, err = validateMerchantStoreEpayCallback(order, payment, url.Values{"pay": {"success"}})
	require.Error(t, err, "a browser return must not settle payment")
}

func TestMerchantStoreEpayQueryDoesNotGuessClosure(t *testing.T) {
	order, payment := merchantStoreTestEpayOrder()
	good := map[string]any{"code": 1, "status": 1, "pid": 123, "out_trade_no": order.TradeNo, "trade_no": "QUERY_TRADE", "type": "alipay", "money": "6.72", "currency": "CNY"}
	payload, _ := json.Marshal(good)
	tradeID, err := validateMerchantStoreEpayQuery(order, payment, payload)
	require.NoError(t, err)
	require.Equal(t, "QUERY_TRADE", tradeID)
	good["status"] = 0
	payload, _ = json.Marshal(good)
	_, err = validateMerchantStoreEpayQuery(order, payment, payload)
	require.ErrorIs(t, err, ErrMerchantStorePaymentIgnored)
	good["status"] = 1
	good["currency"] = "USD"
	payload, _ = json.Marshal(good)
	_, err = validateMerchantStoreEpayQuery(order, payment, payload)
	require.ErrorIs(t, err, ErrMerchantStorePaymentVerification)
}

type merchantStoreTestTransport func(*http.Request) (*http.Response, error)

func (transport merchantStoreTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestMerchantStoreOutboundTransportPinsProviderAndRefreshesReads(t *testing.T) {
	keys := []string{}
	base := merchantStoreTestTransport(func(request *http.Request) (*http.Response, error) {
		keys = append(keys, request.Header.Get("X-Idempotency-Key"))
		require.Empty(t, request.Header.Get("Cookie"))
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	transport := merchantStoreProviderTransport{base: base, orderID: strings.Repeat("a", 64)}
	for _, path := range []string{"/v1/actions/checkout/create-session", "/v1/actions/checkout/create-session", "/v1/graphql"} {
		request, _ := http.NewRequest(http.MethodPost, "https://api.waffo.ai"+path, strings.NewReader(`{}`))
		request.Header.Set("X-Idempotency-Key", "sdk-minute-key")
		request.Header.Set("Cookie", "secret")
		response, err := transport.RoundTrip(request)
		require.NoError(t, err)
		_ = response.Body.Close()
	}
	require.Len(t, keys[0], 64)
	require.Equal(t, keys[0], keys[1])
	require.Empty(t, keys[2])
	request, _ := http.NewRequest(http.MethodPost, "https://evil.example/v1/graphql", nil)
	_, err := transport.RoundTrip(request)
	require.ErrorIs(t, err, ErrMerchantStorePaymentNetwork)
	for _, value := range []string{"http://pay.example", "https://localhost", "https://127.0.0.1", "https://[::1]", "https://192.168.0.1", "https://169.254.169.254", "https://user:pass@pay.example", "https://pay.example:8443", "https://pay.example?a=1"} {
		_, err := merchantStorePublicHTTPSURL(value, false)
		require.Error(t, err, value)
	}
	client := newMerchantStorePaymentHTTPClient("order")
	underlying := client.Transport.(merchantStoreProviderTransport).base.(*http.Transport)
	require.Nil(t, underlying.Proxy)
	require.Nil(t, underlying.TLSClientConfig)
	require.Error(t, client.CheckRedirect(nil, nil))
}

func TestMerchantStorePancakeClosureRequiresProviderExpiryAndCompleteLedger(t *testing.T) {
	order := &model.MerchantStoreOrder{ProviderCheckoutExpiresAt: 1000}
	require.False(t, merchantStorePancakeMayClose(order, nil, 1899))
	require.True(t, merchantStorePancakeMayClose(order, nil, 1900))
	for _, status := range []pancake.PaymentStatus{pancake.PaymentStatusSucceeded, pancake.PaymentStatusPending, "unknown"} {
		require.False(t, merchantStorePancakeMayClose(order, []waffoPancakePayment{{Status: status}}, 2000))
	}
	require.True(t, merchantStorePancakeMayClose(order, []waffoPancakePayment{{Status: pancake.PaymentStatusFailed}, {Status: pancake.PaymentStatusCanceled}}, 2000))
	order.ProviderCheckoutExpiresAt = 0
	require.False(t, merchantStorePancakeMayClose(order, nil, 999999))
}

func TestMerchantStorePancakeLookupRejectsIncompleteOrWrongOrderLedger(t *testing.T) {
	_, private := merchantStorePancakeTestKey(t)
	tradeNo := "MS" + strings.Repeat("a", 30)
	for _, test := range []struct {
		name, payload string
		valid         bool
	}{
		{"empty", `{"data":{"paymentsCount":0,"payments":[]}}`, true},
		{"partial", `{"data":{"paymentsCount":2,"payments":[]}}`, false},
		{"missing count", `{"data":{"payments":[]}}`, false},
		{"missing list", `{"data":{"paymentsCount":0}}`, false},
		{"wrong ref", `{"data":{"paymentsCount":1,"payments":[{"status":"failed","orderMerchantExternalId":"other"}]}}`, false},
		{"GraphQL error", `{"errors":[{"message":"restricted"}],"data":{"paymentsCount":0,"payments":[]}}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			transport := merchantStoreProviderTransport{orderID: "test-order", base: merchantStoreTestTransport(func(request *http.Request) (*http.Response, error) {
				require.Equal(t, "/v1/graphql", request.URL.Path)
				require.Empty(t, request.Header.Get("X-Idempotency-Key"))
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(test.payload))}, nil
			})}
			client, err := pancake.New(pancake.Config{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private, Environment: pancake.Environment("prod"), HTTPClient: &http.Client{Transport: transport}})
			require.NoError(t, err)
			_, err = waffoPancakePaymentsByTradeNo(context.Background(), client, tradeNo)
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

type merchantStoreServiceFixture struct {
	buyer, seller, root model.User
	product             *model.MerchantStoreProduct
}

func merchantStoreServiceDB(t *testing.T, method string) merchantStoreServiceFixture {
	t.Helper()
	oldDB, oldRedis := model.DB, common.RedisEnabled
	oldMain, oldLog := common.MainDatabaseType(), common.LogDatabaseType()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	model.DB = db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, oldLog)
	t.Cleanup(func() {
		model.DB = oldDB
		common.RedisEnabled = oldRedis
		common.SetDatabaseTypes(oldMain, oldLog)
		_ = pool.Close()
	})
	require.NoError(t, db.AutoMigrate(&model.User{}))
	require.NoError(t, db.AutoMigrate(model.MerchantStoreModels()...))
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	f := merchantStoreServiceFixture{buyer: model.User{Username: "store-buyer", AffCode: "store-buyer", Role: 1, Status: 1, Quota: 10000000, Email: "buyer@example.com"}, seller: model.User{Username: "store-seller", AffCode: "store-seller", Role: 1, Status: 1, Quota: 10000000}, root: model.User{Username: "store-root", AffCode: "store-root", Role: 100, Status: 1}}
	for _, user := range []*model.User{&f.buyer, &f.seller, &f.root} {
		require.NoError(t, db.Create(user).Error)
	}
	require.NoError(t, model.SetMerchantStoreConfig(f.root.Id, model.MerchantStoreConfig{FeeBPS: 100, RecipientID: f.root.Id, PromotionQuota: 500000}))
	f.product, err = model.SaveMerchantStoreProduct(f.seller.Id, "", model.MerchantStoreProductInput{Title: "Store card", PriceQuota: 500000, PaymentMethods: []string{method}, EmailPickupLink: true})
	require.NoError(t, err)
	require.NoError(t, model.SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, model.ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, ""))
	_, err = model.AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"PRIVATE-CARD-ONE", "PRIVATE-CARD-TWO"})
	require.NoError(t, err)
	require.NoError(t, model.AcceptMerchantStoreDisclaimer(f.buyer.Id, model.MerchantStoreDisclaimerVersion))
	if !strings.HasPrefix(method, "external:") {
		_, err = model.SaveMerchantStoreGateway(f.seller.Id, method, true, "")
		require.NoError(t, err)
	}
	return f
}

func merchantStoreTestOrder(t *testing.T, f merchantStoreServiceFixture, method string, config merchantStoreGatewayConfig, minor int64, currency string) *model.MerchantStoreOrder {
	t.Helper()
	if strings.HasPrefix(method, "external:") {
		encoded, _ := json.Marshal(config)
		_, err := model.SaveMerchantStoreGateway(f.seller.Id, method, true, string(encoded))
		require.NoError(t, err)
	}
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "purchase", PaymentMethod: method})
	require.NoError(t, err)
	require.NoError(t, model.BindMerchantStorePaymentQuote(order.ID, minor, currency, "1"))
	frozen := merchantStorePaymentContext{Provider: method, Config: config, AmountMinor: minor, Currency: currency, FrozenRate: "1", Origin: "https://api.example.com", PublicOrigin: "https://api.example.com", ExpiresIn: 1800}
	encrypted, err := encryptMerchantStorePaymentValue(merchantStorePaymentContextPurpose(order.ID), frozen)
	require.NoError(t, err)
	require.NoError(t, model.BindMerchantStorePaymentContext(order.ID, encrypted))
	order, err = model.GetMerchantStorePaymentOrder(order.ID)
	require.NoError(t, err)
	return order
}

func TestMerchantStoreEpayCallbackReplayAndSecretPrivacy(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreExternalEpay)
	view, err := SaveMerchantStorePaymentGateway(f.seller.Id, MerchantStoreGatewayConfigInput{Provider: MerchantStoreExternalEpay, Enabled: true, GatewayURL: "https://pay.example.com", PartnerID: "123", Key: "merchant-secret-key", PaymentType: "alipay", Currency: "CNY"})
	require.NoError(t, err)
	encoded, _ := json.Marshal(view)
	require.NotContains(t, string(encoded), "merchant-secret-key")
	_, err = SaveMerchantStorePaymentGateway(f.seller.Id, MerchantStoreGatewayConfigInput{Provider: MerchantStoreExternalEpay, Enabled: true})
	require.NoError(t, err, "blank write-only credentials must preserve the key")
	config, err := loadMerchantStoreGatewayConfig(f.seller.Id, MerchantStoreExternalEpay)
	require.NoError(t, err)
	require.Equal(t, "merchant-secret-key", config.Key)
	order := merchantStoreTestOrder(t, f, MerchantStoreExternalEpay, config, 672, "CNY")
	payment, err := loadMerchantStorePaymentContext(order)
	require.NoError(t, err)
	values := merchantStoreTestEpaySigned(order, payment, nil)
	require.NoError(t, HandleMerchantStoreEpayCallback(context.Background(), order.ID, values))
	require.NoError(t, HandleMerchantStoreEpayCallback(context.Background(), order.ID, values))
	var seller, root, buyer model.User
	require.NoError(t, model.DB.First(&seller, f.seller.Id).Error)
	require.NoError(t, model.DB.First(&root, f.root.Id).Error)
	require.NoError(t, model.DB.First(&buyer, f.buyer.Id).Error)
	require.Equal(t, 9995000, seller.Quota, "external gateway money must not also credit the seller wallet")
	require.Equal(t, 5000, root.Quota)
	require.Equal(t, 10000000, buyer.Quota)
	order, err = model.GetMerchantStorePaymentOrder(order.ID)
	require.NoError(t, err)
	encoded, _ = json.Marshal(order)
	require.NotContains(t, string(encoded), "merchant-secret-key")
	require.NotContains(t, string(encoded), order.GatewaySnapshot)
}

func merchantStorePancakeTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	private := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	t.Setenv("WAFFO_WEBHOOK_PROD_PUBLIC_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})))
	return key, private
}

func merchantStorePancakeSigned(t *testing.T, key *rsa.PrivateKey, payload []byte) string {
	t.Helper()
	stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
	digest := sha256.Sum256(append([]byte(stamp+"."), payload...))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return "t=" + stamp + ",v1=" + base64.StdEncoding.EncodeToString(signature)
}

func TestMerchantStorePancakeSignedCallbackValidatesMoneyScopeAndReplay(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreExternalPancake)
	key, private := merchantStorePancakeTestKey(t)
	config := merchantStoreGatewayConfig{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private, StoreID: "STO_AbCdEfGhIjKlMnOpQrStUv", ProductID: "PROD_AbCdEfGhIjKlMnOpQrStUv", Environment: "prod", Currency: "USD"}
	order := merchantStoreTestOrder(t, f, MerchantStoreExternalPancake, config, 100, "USD")
	data := map[string]any{"orderId": "ORD_example", "orderStatus": "completed", "paymentStatus": "succeeded", "orderMerchantExternalId": order.TradeNo, "merchantProvidedBuyerIdentity": WaffoPancakeBuyerIdentityFromUserID(order.BuyerID), "currency": "USD", "amount": "1.10", "taxAmount": "0.10", "orderMetadata": map[string]string{"lmm_store_order_id": order.ID, "lmm_store_product_id": order.ProductID, "lmm_pancake_product_id": config.ProductID, "lmm_store_seller_id": strconv.Itoa(order.SellerID)}}
	event := map[string]any{"id": "ORD_example", "eventId": "ORD_example-completed", "eventType": "order.completed", "mode": "prod", "storeId": config.StoreID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "data": data}
	data["currency"] = "CNY"
	payload, _ := json.Marshal(event)
	require.Error(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	data["currency"] = "USD"
	payload, _ = json.Marshal(event)
	require.Error(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, "invalid"))
	require.Error(t, HandleMerchantStorePancakeWebhook(context.Background(), "platform", 0, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	require.NoError(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	require.NoError(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	var root model.User
	require.NoError(t, model.DB.First(&root, f.root.Id).Error)
	require.Equal(t, 5000, root.Quota)
}

func TestMerchantStorePancakeSessionUsesRealSDKAndStoresProviderExpiry(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreExternalPancake)
	_, private := merchantStorePancakeTestKey(t)
	config := merchantStoreGatewayConfig{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private, StoreID: "STO_AbCdEfGhIjKlMnOpQrStUv", ProductID: "PROD_AbCdEfGhIjKlMnOpQrStUv", Environment: "prod", Currency: "USD"}
	order := merchantStoreTestOrder(t, f, MerchantStoreExternalPancake, config, 100, "USD")
	contextSnapshot, err := loadMerchantStorePaymentContext(order)
	require.NoError(t, err)
	expiry := time.Now().Add(25 * time.Minute).UTC().Truncate(time.Second)
	var calls atomic.Int32
	transport := merchantStoreProviderTransport{orderID: order.ID, base: merchantStoreTestTransport(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		require.Len(t, request.Header.Get("X-Idempotency-Key"), 64)
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		require.NotContains(t, string(body), f.buyer.Email)
		var payload string
		switch request.URL.Path {
		case "/v1/actions/auth/issue-session-token":
			require.Contains(t, string(body), WaffoPancakeBuyerIdentityFromUserID(order.BuyerID))
			payload = fmt.Sprintf(`{"data":{"token":"test-buyer-token","expiresAt":%q}}`, expiry.Format(time.RFC3339))
		case "/v1/actions/checkout/create-session":
			require.Contains(t, string(body), `"amount":"1.00"`)
			require.Contains(t, string(body), order.TradeNo)
			payload = fmt.Sprintf(`{"data":{"sessionId":"SESSION_example","checkoutUrl":"https://checkout.waffo.ai/example","expiresAt":%q}}`, expiry.Format(time.RFC3339))
		default:
			t.Errorf("unexpected provider path %s", request.URL.Path)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload))}, nil
	})}
	client, err := pancake.New(pancake.Config{MerchantID: config.MerchantID, PrivateKey: private, Environment: pancake.Environment("prod"), HTTPClient: &http.Client{Transport: transport}})
	require.NoError(t, err)
	session, err := createMerchantStorePancakeSessionWithClient(context.Background(), order, contextSnapshot, &MerchantStorePaymentSession{OrderID: order.ID, AmountMinor: 100, Amount: "1.00", Currency: "USD"}, client)
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load())
	require.Equal(t, "https://checkout.waffo.ai/example#token=test-buyer-token", session.PaymentURL)
	order, err = model.GetMerchantStorePaymentOrder(order.ID)
	require.NoError(t, err)
	require.Equal(t, expiry.Unix(), order.ProviderCheckoutExpiresAt)
	sellerView, err := model.GetMerchantStoreOrder(f.seller.Id, order.ID)
	require.NoError(t, err)
	require.Empty(t, sellerView.CheckoutURL)
}

func TestMerchantStorePancakeVerifiedClosureReleasesReservationOnce(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreExternalPancake)
	_, private := merchantStorePancakeTestKey(t)
	config := merchantStoreGatewayConfig{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private, StoreID: "STO_AbCdEfGhIjKlMnOpQrStUv", ProductID: "PROD_AbCdEfGhIjKlMnOpQrStUv", Environment: "prod", Currency: "USD"}
	order := merchantStoreTestOrder(t, f, MerchantStoreExternalPancake, config, 100, "USD")
	now := time.Now().Unix()
	require.NoError(t, model.BindMerchantStoreCheckoutSession(order.ID, "https://checkout.waffo.ai/expired", "SESSION_expired", now-901))
	order, err := model.GetMerchantStorePaymentOrder(order.ID)
	require.NoError(t, err)
	closed, err := merchantStoreCloseVerifiedPancakeLedger(order, []waffoPancakePayment{{Status: "unknown"}}, now)
	require.NoError(t, err)
	require.False(t, closed)
	var seller model.User
	require.NoError(t, model.DB.First(&seller, f.seller.Id).Error)
	require.Equal(t, 9995000, seller.Quota)
	closed, err = merchantStoreCloseVerifiedPancakeLedger(order, nil, now)
	require.NoError(t, err)
	require.True(t, closed)
	closed, err = merchantStoreCloseVerifiedPancakeLedger(order, nil, now)
	require.NoError(t, err)
	require.True(t, closed)
	require.NoError(t, model.DB.First(&seller, f.seller.Id).Error)
	require.Equal(t, 10000000, seller.Quota)
	var available int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreStock{}).Where("state = ?", "available").Count(&available).Error)
	require.EqualValues(t, 2, available)
}

func TestMerchantStorePaymentContextCannotMoveBetweenOrders(t *testing.T) {
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "C5wmMzDh1QsVZb0saEW9ulAPzVN87Boqv3DK6eIrKXc2YLfg")
	encoded, err := encryptMerchantStorePaymentValue(merchantStorePaymentContextPurpose("order-one"), merchantStorePaymentContext{Provider: MerchantStoreExternalEpay})
	require.NoError(t, err)
	var value merchantStorePaymentContext
	require.Error(t, decryptMerchantStorePaymentValue(merchantStorePaymentContextPurpose("order-two"), encoded, &value))
}

func TestMerchantStoreReconcileRequiresOrderOwnerOrActiveAdmin(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "paid", PaymentMethod: MerchantStoreBalance})
	require.NoError(t, err)
	outsider := model.User{Username: "outsider", AffCode: "outsider", Role: 1, Status: 1}
	require.NoError(t, model.DB.Create(&outsider).Error)
	_, err = ReconcileMerchantStoreOrderPayment(context.Background(), outsider.Id, order.ID)
	require.ErrorIs(t, err, ErrMerchantStorePaymentAccess)
	_, err = ReconcileMerchantStoreOrderPayment(context.Background(), 0, order.ID)
	require.ErrorIs(t, err, ErrMerchantStorePaymentAccess)
	paid, err := ReconcileMerchantStoreOrderPayment(context.Background(), f.buyer.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, "paid", paid.Status)
	paid, err = ReconcileMerchantStoreOrderPayment(context.Background(), f.root.Id, order.ID)
	require.NoError(t, err)
	require.Empty(t, paid.CheckoutURL)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.root.Id).Update("status", common.UserStatusDisabled).Error)
	_, err = ReconcileMerchantStoreOrderPayment(context.Background(), f.root.Id, order.ID)
	require.ErrorIs(t, err, ErrMerchantStorePaymentAccess)
}

func TestMerchantStorePickupEmailOutboxUsesBuyerAndRetriesPrivately(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	require.NoError(t, model.MarkMerchantStoreEmailVerified(f.buyer.Id, f.buyer.Email))
	oldOrigin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = oldOrigin })
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "paid", PaymentMethod: MerchantStoreBalance})
	require.NoError(t, err)
	calls := 0
	n, err := processMerchantStorePickupEmailBatch(context.Background(), 5, func(_ context.Context, email merchantStorePickupEmail) error {
		calls++
		require.Equal(t, f.buyer.Email, email.destination)
		require.Equal(t, order.TradeNo, email.tradeNo)
		require.True(t, strings.HasPrefix(email.pickupURL, "https://api.example.com/store/claim/"))
		require.Len(t, strings.TrimPrefix(email.pickupURL, "https://api.example.com/store/claim/"), 43)
		return fmt.Errorf("SMTP failed buyer@example.com secret-token")
	})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, calls)
	var row model.MerchantStoreEmailDelivery
	require.NoError(t, model.DB.First(&row).Error)
	require.Equal(t, "retry", row.State)
	require.Equal(t, "smtp_delivery_failed", row.LastErrorCode)
	require.NoError(t, model.DB.Model(&row).Update("next_attempt", 0).Error)
	n, err = processMerchantStorePickupEmailBatch(context.Background(), 5, func(context.Context, merchantStorePickupEmail) error { return nil })
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.NoError(t, model.DB.First(&row).Error)
	require.Equal(t, "sent", row.State)
	encoded, _ := json.Marshal(row)
	require.NotContains(t, string(encoded), "buyer@example.com")
	require.NotContains(t, string(encoded), "secret-token")
}

func TestMerchantStorePickupEmailWaitsForVerifiedCurrentAddress(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	oldOrigin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = oldOrigin })
	_, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "paid", PaymentMethod: MerchantStoreBalance})
	require.NoError(t, err)
	calls := 0
	sender := func(context.Context, merchantStorePickupEmail) error { calls++; return nil }
	n, err := processMerchantStorePickupEmailBatch(context.Background(), 5, sender)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, calls)
	var row model.MerchantStoreEmailDelivery
	require.NoError(t, model.DB.First(&row).Error)
	require.Equal(t, "awaiting_verification", row.State)
	require.NoError(t, model.MarkMerchantStoreEmailVerified(f.buyer.Id, f.buyer.Email))
	// Changing the address after it was verified blocks dispatch again, and
	// does not consume delivery retry attempts while verification is pending.
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.buyer.Id).Update("email", "new@example.com").Error)
	n, err = processMerchantStorePickupEmailBatch(context.Background(), 5, sender)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Zero(t, calls)
	require.NoError(t, model.DB.First(&row).Error)
	require.Equal(t, "awaiting_verification", row.State)
	require.Zero(t, row.Attempts)
	require.NoError(t, model.MarkMerchantStoreEmailVerified(f.buyer.Id, "new@example.com"))
	n, err = processMerchantStorePickupEmailBatch(context.Background(), 5, sender)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, calls)
}
