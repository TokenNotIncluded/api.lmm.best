package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreDiscountPaymentMinimumUsesNativeQuoteBeforeRounding(t *testing.T) {
	merchantStoreTestCreditBasis(t)
	for _, test := range []struct {
		name  string
		quota int
		rate  string
		valid bool
	}{
		{"USD under one cent", 4999, "1", false},
		{"USD one cent", 5000, "1", true},
		{"CNY just under one fen", 745, "6.710363", false},
		{"CNY just over one fen", 746, "6.710363", true},
		{"LDC under native cent", 249, "20", false},
		{"LDC native cent", 250, "20", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			order := &model.MerchantStoreOrder{PriceQuota: test.quota, DiscountBPS: 9999}
			err := merchantStoreDiscountPaymentMinimum(order, test.rate, nil)
			if test.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, ErrMerchantStorePaymentMinimum)
			}
		})
	}
	// Paired LDC pricing must retain both operands; the effective rate must not
	// accidentally fall back to the legacy platform-unit numerator.
	pricing := &paymentpricing.SettlementPricing{PlatformUnitsPerUSD: decimal.NewFromInt(7), SettlementUnitsPerUSD: decimal.NewFromInt(30)}
	under := &model.MerchantStoreOrder{PriceQuota: 1166, DiscountBPS: 9999}
	require.ErrorIs(t, merchantStoreDiscountPaymentMinimum(under, "30", pricing), ErrMerchantStorePaymentMinimum)
	under.PriceQuota = 1167
	require.NoError(t, merchantStoreDiscountPaymentMinimum(under, "30", pricing))
	pricing = &paymentpricing.SettlementPricing{UsesSettlementUnitsPerPlatformUnit: true, SettlementUnitsPerPlatformUnit: decimal.NewFromInt(20)}
	under.PriceQuota = 249
	require.ErrorIs(t, merchantStoreDiscountPaymentMinimum(under, "20", pricing), ErrMerchantStorePaymentMinimum)
	under.PriceQuota = 250
	require.NoError(t, merchantStoreDiscountPaymentMinimum(under, "20", pricing))
	plain := &model.MerchantStoreOrder{PriceQuota: 4999, DiscountBPS: 0}
	require.ErrorIs(t, merchantStoreDiscountPaymentMinimum(plain, "1", nil), ErrMerchantStorePaymentMinimum, "zero-percent codes and small undiscounted SKUs cannot skip native minimums")
	plain.PriceQuota = 5000
	require.NoError(t, merchantStoreDiscountPaymentMinimum(plain, "1", nil))
}

func storeDiscountTestEpayMinimum(t *testing.T) merchantStoreServiceFixture {
	t.Helper()
	f := merchantStoreServiceDB(t, MerchantStoreExternalEpay)
	merchantStoreTestCreditBasis(t)
	oldFX := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 7
	t.Cleanup(func() { operation_setting.USDExchangeRate = oldFX })
	config, err := json.Marshal(merchantStoreGatewayConfig{GatewayURL: "https://pay.example.com", PartnerID: "12345", Key: "fixture-key", PaymentType: "alipay", Currency: "CNY"})
	require.NoError(t, err)
	_, err = model.SaveMerchantStoreGateway(f.seller.Id, MerchantStoreExternalEpay, true, string(config))
	require.NoError(t, err)
	_, err = model.SaveMerchantStoreGateway(f.seller.Id, MerchantStoreBalance, true, "")
	require.NoError(t, err)
	require.NoError(t, model.DB.Model(&model.MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("payment_methods", `["external:epay","balance"]`).Error)
	return f
}

func TestMerchantStoreDiscountMinimumCancelsUnissuedOrderAndRestoresReservations(t *testing.T) {
	f := storeDiscountTestEpayMinimum(t)
	config, err := model.GetMerchantStoreConfig()
	require.NoError(t, err)
	config.MinimumUnitPriceQuota = 0
	require.NoError(t, model.SetMerchantStoreConfig(f.root.Id, config))
	require.NoError(t, model.DB.Model(&model.MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("price_quota", 1).Error)
	require.NoError(t, model.DB.Model(&model.MerchantStoreVariant{}).Where("product_id = ?", f.product.ID).Update("price_quota", 1).Error)
	input := model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "small", PaymentMethod: MerchantStoreExternalEpay, PickupEmail: f.buyer.Email}
	order, _, err := model.CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	previous := http.DefaultTransport
	transport := &storeDiscountNoNetworkTransport{}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	_, err = CreateMerchantStorePaymentSession(context.Background(), f.buyer.Id, order.ID, "CNY")
	require.ErrorIs(t, err, ErrMerchantStorePaymentMinimum)
	var minimum *MerchantStorePaymentMinimumError
	require.ErrorAs(t, err, &minimum)
	require.True(t, minimum.OrderCancelled)
	require.Equal(t, "cancelled", minimum.OrderStatus)
	require.Equal(t, order.ID, minimum.OrderID)
	current, err := model.GetMerchantStorePaymentOrder(order.ID)
	require.NoError(t, err)
	require.False(t, current.FeeHeld)
	require.Empty(t, current.GatewaySnapshot)
	require.Zero(t, current.AmountMinor)
	var available int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreStock{}).Where("product_id = ? AND state = ?", f.product.ID, "available").Count(&available).Error)
	require.EqualValues(t, 2, available)
	var seller model.User
	require.NoError(t, model.DB.First(&seller, f.seller.Id).Error)
	require.Equal(t, 10000000, seller.Quota)
	require.Zero(t, transport.calls)
}

func TestMerchantStoreDiscountMinimumRetainsConcurrentIssuedAndNetworkObligations(t *testing.T) {
	f := storeDiscountTestEpayMinimum(t)
	input := model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "issued-race", PaymentMethod: MerchantStoreExternalEpay, PickupEmail: f.buyer.Email}
	order, _, err := model.CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	// A network refusal never takes the cancellation path.
	cancel := func() error { return model.CancelMerchantStoreOrder(f.buyer.Id, order.ID) }
	require.ErrorIs(t, merchantStorePaymentPreparationFailure(order, ErrMerchantStorePaymentNetwork, cancel), ErrMerchantStorePaymentNetwork)
	current, err := model.GetMerchantStorePaymentOrder(order.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", current.Status)
	require.True(t, current.FeeHeld)
	// Simulate another request issuing after the first request read the order.
	require.NoError(t, model.BindMerchantStorePaymentQuote(order.ID, 100, "CNY", "7"))
	require.NoError(t, model.BindMerchantStorePaymentContext(order.ID, "opaque-issued-fixture", strings.Repeat("a", 64)))
	err = merchantStorePaymentPreparationFailure(order, ErrMerchantStorePaymentMinimum, cancel)
	require.ErrorIs(t, err, ErrMerchantStorePaymentMinimum)
	var minimum *MerchantStorePaymentMinimumError
	require.True(t, errors.As(err, &minimum))
	require.False(t, minimum.OrderCancelled)
	require.Equal(t, "pending", minimum.OrderStatus)
	current, err = model.GetMerchantStorePaymentOrder(order.ID)
	require.NoError(t, err)
	require.Equal(t, "opaque-issued-fixture", current.GatewaySnapshot)
	require.True(t, current.FeeHeld)
	var reserved int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreStock{}).Where("order_id = ? AND state = ?", order.ID, "reserved").Count(&reserved).Error)
	require.EqualValues(t, 1, reserved)
}

func TestMerchantStoreDiscountMinimumOneUseCanRetryBalanceAfterConfirmedCancellation(t *testing.T) {
	f := storeDiscountTestEpayMinimum(t)
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 4, "promotion positive tests require capability-4 integration")
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	bps, uses := 9999, int64(1)
	promotion, err := model.SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", model.MerchantStoreDiscountCodeInput{DiscountBPS: &bps, MaxUses: &uses})
	require.NoError(t, err)
	input := model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "small-coupon", PaymentMethod: MerchantStoreExternalEpay, PromotionCode: promotion.Code, PickupEmail: f.buyer.Email}
	order, _, err := model.CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	balance := input
	balance.RequestKey, balance.PaymentMethod = "balance-before-cancel", MerchantStoreBalance
	_, _, err = model.CreateMerchantStoreOrder(balance)
	require.ErrorIs(t, err, model.ErrMerchantStoreDiscountLimit)
	_, err = CreateMerchantStorePaymentSession(context.Background(), f.buyer.Id, order.ID, "CNY")
	var minimum *MerchantStorePaymentMinimumError
	require.ErrorAs(t, err, &minimum)
	require.True(t, minimum.OrderCancelled)
	balance.RequestKey = "balance-after-cancel"
	paid, made, err := model.CreateMerchantStoreOrder(balance)
	require.NoError(t, err)
	require.True(t, made)
	require.Equal(t, "paid", paid.Status)
	require.Equal(t, 50, paid.PriceQuota)
	rows, _, err := model.ListMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows[0].UsesCount)
	require.Zero(t, rows[0].ReservedCount)
}

type storeDiscountNoNetworkTransport struct{ calls int }

func (transport *storeDiscountNoNetworkTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls++
	return nil, ErrMerchantStorePaymentNetwork
}

func TestMerchantStoreDiscountFreePaymentSessionNeverContactsGateway(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 4, "promotion positive tests require capability-4 integration")
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	bps := 10000
	promotion, err := model.SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", model.MerchantStoreDiscountCodeInput{DiscountBPS: &bps})
	require.NoError(t, err)
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "free", PaymentMethod: "free", PromotionCode: promotion.Code, PickupEmail: f.buyer.Email})
	require.NoError(t, err)
	previous := http.DefaultTransport
	transport := &storeDiscountNoNetworkTransport{}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	session, err := CreateMerchantStorePaymentSession(context.Background(), f.buyer.Id, order.ID, "USD")
	require.NoError(t, err)
	require.Equal(t, "free", session.Method)
	require.Equal(t, "paid", session.Status)
	require.Equal(t, "CREDIT", session.Currency)
	require.Zero(t, session.AmountMinor)
	require.Empty(t, session.PaymentURL)
	require.Zero(t, transport.calls)
	_, err = CreateMerchantStorePaymentSession(context.Background(), f.seller.Id, order.ID, "")
	require.ErrorIs(t, err, ErrMerchantStorePaymentAccess)
	// A corrupt or fabricated free label cannot turn a payable order into a gift.
	require.NoError(t, model.DB.Model(&model.MerchantStoreOrder{}).Where("id = ?", order.ID).Update("price_quota", 1).Error)
	_, err = CreateMerchantStorePaymentSession(context.Background(), f.buyer.Id, order.ID, "")
	require.ErrorIs(t, err, ErrMerchantStorePaymentAccess)
	require.Zero(t, transport.calls)
}
