package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func storeTrafficInput(kind string) MerchantStoreTrafficInput {
	return MerchantStoreTrafficInput{Kind: kind, PageKey: uuid.NewString(), PageStartedAt: common.GetTimestamp()}
}

func TestMerchantStoreAnalyticsPhaseSixUnknownAndReadOnly(t *testing.T) {
	f := storeSocialFixture(t, true)
	before := storeWriterSnapshot(t)
	stats, err := GetMerchantStoreAnalytics(f.seller.Id, false, 7, 0, 10)
	require.NoError(t, err)
	require.False(t, stats.TrafficSupported)
	require.Nil(t, stats.Totals.Impressions)
	require.Nil(t, stats.Totals.Clicks)
	require.ErrorIs(t, RecordMerchantStoreTraffic(f.buyer.Id, f.product.ID, storeTrafficInput("impression")), ErrMerchantStoreWriterFrozen)
	require.Equal(t, before, storeWriterSnapshot(t), "a read and refused traffic never install or activate schema")
}

func TestMerchantStoreAnalyticsTrafficVisibleIdempotentAndPrivate(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance")
	before := storeWriterSnapshot(t)
	in := storeTrafficInput("impression")
	for i := 0; i < 3; i++ {
		require.NoError(t, RecordMerchantStoreTraffic(f.buyer.Id, f.product.ID, in))
	}
	require.NoError(t, RecordMerchantStoreTraffic(f.buyer.Id, f.product.ID, MerchantStoreTrafficInput{Kind: "click", PageKey: in.PageKey, PageStartedAt: in.PageStartedAt}))
	require.NoError(t, RecordMerchantStoreTraffic(f.seller.Id, f.product.ID, storeTrafficInput("impression")))
	stats, err := GetMerchantStoreAnalytics(f.seller.Id, false, 7, 0, 10)
	require.NoError(t, err)
	require.True(t, stats.TrafficSupported)
	require.EqualValues(t, 1, *stats.Totals.Impressions)
	require.EqualValues(t, 1, *stats.Totals.Clicks)
	require.NotNil(t, stats.TrafficSince)
	var receipts []MerchantStoreProductTrafficReceipt
	require.NoError(t, DB.Find(&receipts).Error)
	require.Len(t, receipts, 2)
	for _, row := range receipts {
		require.Len(t, row.ID, 64)
		require.NotContains(t, row.ID, in.PageKey)
	}
	require.Equal(t, before, storeWriterSnapshot(t), "traffic never mutates wallets or permanent order facts")
	visibility := "private"
	private, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Private", PriceQuota: 500000, Template: "card-key", PaymentMethods: []string{"balance"}, Visibility: &visibility})
	require.NoError(t, err)
	require.NoError(t, RecordMerchantStoreTraffic(f.seller.Id, private.ID, storeTrafficInput("impression")))
	require.ErrorIs(t, RecordMerchantStoreTraffic(f.buyer.Id, private.ID, storeTrafficInput("impression")), gorm.ErrRecordNotFound)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).UpdateColumn("visibility", "registered").Error)
	require.ErrorIs(t, RecordMerchantStoreTraffic(0, f.product.ID, storeTrafficInput("impression")), gorm.ErrRecordNotFound, "registered-only products stay private from anonymous analytics too")

}

func TestMerchantStoreAnalyticsRealOrdersRefundsAndOwnerScope(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance")
	input := storeFixedCheckout(t, f, "real-paid-order", "balance")
	input.Quantity = 3
	order, _, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	self := storeFixedCheckout(t, f, "self-testing-order", "balance")
	self.BuyerID = f.seller.Id
	require.NoError(t, AcceptMerchantStoreDisclaimer(f.seller.Id, MerchantStoreDisclaimerVersion))
	_, _, err = CreateMerchantStoreOrder(self)
	require.NoError(t, err)
	refund, err := ProactivelyRefundMerchantStoreOrder(f.seller.Id, order.ID, MerchantStoreRefundInput{RequestKey: "partial-quantity", Reason: "Partial return", Mode: "quantity", Quantity: 1})
	require.NoError(t, err)
	require.Equal(t, "completed", refund.Status)
	_, err = ProactivelyRefundMerchantStoreOrder(f.seller.Id, order.ID, MerchantStoreRefundInput{RequestKey: "partial-amount", Reason: "Amount-only return", Mode: "amount", AmountQuota: 12345})
	require.NoError(t, err)
	stats, err := GetMerchantStoreAnalytics(f.seller.Id, false, 7, 0, 1)
	require.NoError(t, err)
	require.EqualValues(t, 1, stats.Totals.Orders)
	require.EqualValues(t, 1, stats.Totals.PaidOrders)
	require.EqualValues(t, 1, stats.Totals.RefundedOrders)
	require.EqualValues(t, 1, stats.Totals.QuantityRefundedOrders)
	require.EqualValues(t, 1, stats.Totals.AmountRefundedOrders)
	require.EqualValues(t, 3, stats.Totals.PaidQuantity)
	require.EqualValues(t, 1, stats.Totals.RefundedQuantity)
	require.EqualValues(t, 2, stats.Totals.NetPaidQuantity)
	buyer, err := GetMerchantStoreAnalytics(f.buyer.Id, false, 7, 0, 20)
	require.NoError(t, err)
	require.Empty(t, buyer.Items)
	require.Zero(t, buyer.Totals.Orders)
	_, err = GetMerchantStoreAnalytics(f.buyer.Id, true, 7, 0, 20)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	admin, err := GetMerchantStoreAnalytics(f.root.Id, true, 7, 0, 1)
	require.NoError(t, err)
	require.Equal(t, stats.Totals, admin.Totals, "a page boundary cannot truncate all-store totals")
	encoded, err := json.Marshal(admin)
	require.NoError(t, err)
	for _, field := range []string{"buyer_id", "guest_id", "pickup", "email", "checkout_url"} {
		require.NotContains(t, string(encoded), field)
	}
}

func TestMerchantStoreAnalyticsRetentionConfigAndReplayExpiry(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance")
	before := storeWriterSnapshot(t)
	config := MerchantStoreAnalyticsConfig{RetentionDays: 2, DedupeDays: 1}
	require.ErrorIs(t, SaveMerchantStoreAnalyticsConfig(f.seller.Id, config), ErrMerchantStoreDenied)
	require.NoError(t, SaveMerchantStoreAnalyticsConfig(f.root.Id, config))
	stored, err := GetMerchantStoreAnalyticsConfig(f.root.Id)
	require.NoError(t, err)
	require.Equal(t, config, stored)
	for _, raw := range []string{`{"retention_days":0,"dedupe_days":1}`, `{"retention_days":2,"dedupe_days":3}`, `{"retention_days":3651,"dedupe_days":1}`, `{"retention_days":2,"dedupe_days":1,"unknown":1}`} {
		require.Error(t, ValidateOptionValue(MerchantStoreAnalyticsRetentionOption, raw))
	}
	now := common.GetTimestamp()
	old := MerchantStoreProductTrafficDay{ProductID: f.product.ID, Day: storeAnalyticsDay(now) - 3*86400, Impressions: 40}
	require.NoError(t, DB.Create(&old).Error)
	require.NoError(t, DB.Create(&MerchantStoreProductTrafficReceipt{ID: strings.Repeat("a", 64), ProductID: f.product.ID, Kind: "impression", PageStartedAt: now - 2*86400, CreatedAt: now - 2*86400}).Error)
	expired := storeTrafficInput("impression")
	expired.PageStartedAt = now - 2*86400
	require.ErrorIs(t, RecordMerchantStoreTraffic(f.buyer.Id, f.product.ID, expired), ErrMerchantStoreInput)
	require.NoError(t, RecordMerchantStoreTraffic(f.buyer.Id, f.product.ID, storeTrafficInput("impression")))
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreProductTrafficDay{}).Where("day = ?", old.Day).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, DB.Model(&MerchantStoreProductTrafficReceipt{}).Where("id = ?", strings.Repeat("a", 64)).Count(&count).Error)
	require.Zero(t, count)
	stats, err := GetMerchantStoreAnalytics(f.seller.Id, false, 0, 0, 20)
	require.NoError(t, err)
	require.EqualValues(t, 1, *stats.Totals.Impressions)
	require.Equal(t, before, storeWriterSnapshot(t), "retention never rewrites business or balance records")
}

func TestMerchantStoreAnalyticsPostgresConcurrentReceipts(t *testing.T) {
	db, _, _, _ := merchantStorePGDB(t)
	f := merchantStorePGFixture(t, db, "balance")
	storeActivateFixedTest(t, db)
	input := storeTrafficInput("impression")
	var workers sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() { defer workers.Done(); errs <- RecordMerchantStoreTraffic(f.buyer.Id, f.product.ID, input) }()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var traffic MerchantStoreProductTrafficDay
	require.NoError(t, db.First(&traffic, "product_id = ?", f.product.ID).Error)
	require.EqualValues(t, 1, traffic.Impressions)
	var count int64
	require.NoError(t, db.Model(&MerchantStoreProductTrafficReceipt{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	stats, err := GetMerchantStoreAnalytics(f.seller.Id, false, 7, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, *stats.Totals.Impressions)
	require.EqualValues(t, 0, *stats.Totals.Clicks)
}
