package model

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHeroSMSFiatCatalogProjectionPreservesQuotesAndRawCharges(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 1701, common.GetTrustQuota())
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(3500000)))
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{Enabled: ptrBool(true), EmailEnabled: ptrBool(true), SMSEnabled: ptrBool(true), APIKey: "test-secret-key-12345"}))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/emails/domains" {
			encodeHeroSMSModelTestJSON(t, w, heroSMSDomainResponse(1, 5))
			return
		}
		if r.URL.Path == "/api/v1/activations/offers/sms" {
			_, _ = w.Write([]byte(`{"data":{"tg":{"6":{"counts":{"total":6,"defaultPrice":4},"prices":{"default":1},"map":{"1":4,"0.0000001":1,"0.0000002":1}}}}}`))
			return
		}
		switch r.URL.Query().Get("action") {
		case "getPrices":
			_, _ = w.Write([]byte(`{"6":{"tg":{"cost":1,"count":4}}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL, "secret") }, server.URL)
	defer restore()
	products, err := ListHeroSMSEmailProducts(t.Context(), 1, 10, "demo.com")
	require.NoError(t, err)
	require.Len(t, products.Items, 1)
	product := products.Items[0]
	require.Equal(t, 500000, product.ChargeQuota)
	require.Equal(t, 2, product.PricingSchemaVersion)
	require.Equal(t, "USD", product.PricingCurrency)
	require.True(t, product.PricingAvailable)
	expected := decimal.NewFromInt(1).DivRound(decimal.NewFromInt(7), 64).String()
	require.Equal(t, expected, product.CustomerPriceUSD)
	token, err := decodeHeroSMSQuoteID(product.ID)
	require.NoError(t, err)
	require.Equal(t, "1", token.CostUSD)
	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
	require.NoError(t, err)
	require.Equal(t, 500000, offer.Tiers[2].ChargeQuota)
	require.Equal(t, 1, offer.Tiers[0].ChargeQuota)
	require.Equal(t, offer.Tiers[0].ChargeQuota, offer.Tiers[1].ChargeQuota)
	require.Equal(t, offer.Tiers[0].CustomerPriceUSD, offer.Tiers[1].CustomerPriceUSD)
	require.NotEmpty(t, offer.Tiers[0].PriceTierKey)
	require.NotEqual(t, offer.Tiers[0].PriceTierKey, offer.Tiers[1].PriceTierKey)
	require.Equal(t, expected, offer.Tiers[2].CustomerPriceUSD)
	smsToken, err := decodeHeroSMSSMSQuote(offer.Tiers[2].ID)
	require.NoError(t, err)
	require.Equal(t, "1", smsToken.CostCNY)
	common.ClearCreditsPerUSD()
	products, err = ListHeroSMSEmailProducts(t.Context(), 1, 10, "demo.com")
	require.NoError(t, err)
	require.False(t, products.Items[0].PricingAvailable)
	require.Empty(t, products.Items[0].PricingCurrency)
	require.Empty(t, products.Items[0].CustomerPriceUSD)
	require.Equal(t, 500000, products.Items[0].ChargeQuota)
}

func TestHeroSMSStoredOrdersProjectFiatWithoutRepricingOrChangingRefunds(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 1702, 7)
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(3500000)))
	order := HeroSMSEmailOrder{UserID: user.Id, Operation: "purchase", IdempotencyKeyHash: "projection-email", RequestPayloadHash: "original-payload", DomainID: "original-quote", Site: "demo.com", Domain: "mail.test", Quantity: 2, Status: HeroSMSEmailOrderStatusPendingProvider, PriceMultiplier: "1", ReservedUnitCostMicros: 1000000, ReservedUnitCostDecimal: "1", CustomerUnitPriceMicros: 1000000, ChargeQuota: 1000000}
	require.NoError(t, db.Create(&order).Error)
	before := order
	view, err := heroSMSEmailOrderView(&order)
	require.NoError(t, err)
	expected := decimal.NewFromInt(1).DivRound(decimal.NewFromInt(7), 64).String()
	require.Equal(t, expected, view.CustomerPriceUSD)
	require.Equal(t, 2, view.Quantity)
	require.Equal(t, 1000000, view.ChargeQuota)
	require.Equal(t, before, order)
	smsOrder := HeroSMSSMSOrder{UserID: user.Id, IdempotencyKeyHash: "projection-sms", RequestPayloadHash: "original-sms-payload", CountryID: 6, Service: "tg", Status: HeroSMSSMSOrderStatusPendingProvider, PriceMultiplier: "1", ProviderPriceCNY: "1", CustomerPriceUSD: "1", ReservedQuota: 500000, ChargeQuota: 500000}
	require.NoError(t, db.Create(&smsOrder).Error)
	smsBefore := smsOrder
	for _, render := range []func(*HeroSMSSMSOrder) (*HeroSMSSMSOrderView, error){heroSMSSMSOrderView, heroSMSSMSOrderSummaryView} {
		smsView, err := render(&smsOrder)
		require.NoError(t, err)
		require.Equal(t, expected, smsView.CustomerPriceUSD)
		require.Equal(t, 500000, smsView.ChargeQuota)
		require.True(t, smsView.PricingAvailable)
	}
	require.Equal(t, smsBefore, smsOrder)
	common.ClearCreditsPerUSD()
	view, err = heroSMSEmailOrderView(&order)
	require.NoError(t, err)
	require.False(t, view.PricingAvailable)
	require.Empty(t, view.CustomerPriceUSD)
	require.Equal(t, 1000000, view.ChargeQuota)
	for range 2 {
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error { return heroSMSRefundOrderTx(tx, &order, order.ChargeQuota, "original-refund") }))
	}
	var fresh HeroSMSEmailOrder
	require.NoError(t, db.First(&fresh, "id = ?", order.ID).Error)
	require.Equal(t, 1000000, fresh.RefundedQuota)
	require.Equal(t, before.CustomerUnitPriceMicros, fresh.CustomerUnitPriceMicros)
	require.Equal(t, before.ReservedUnitCostDecimal, fresh.ReservedUnitCostDecimal)
	require.Equal(t, before.ChargeQuota, fresh.ChargeQuota)
	var freshSMS HeroSMSSMSOrder
	require.NoError(t, db.First(&freshSMS, "id = ?", smsOrder.ID).Error)
	require.Equal(t, smsBefore.CustomerPriceUSD, freshSMS.CustomerPriceUSD)
	require.Equal(t, smsBefore.ChargeQuota, freshSMS.ChargeQuota)
	var freshUser User
	require.NoError(t, db.First(&freshUser, user.Id).Error)
	require.Equal(t, 1000007, freshUser.Quota)
	require.Contains(t, heroSMSSMSMinimumBalanceError(0, 5000000).Error(), "5000000 Credits")
}
