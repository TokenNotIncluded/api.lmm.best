package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreGuestMinimumCleanupKeepsExactAuthorityAndIssuedObligations(t *testing.T) {
	f := storeDiscountTestEpayMinimum(t)
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 5, "requires the centrally qualified phase-5 candidate")
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	require.True(t, model.MerchantStoreAccessSupported())
	config, err := model.GetMerchantStoreConfig()
	require.NoError(t, err)
	config.MinimumUnitPriceQuota = 0
	require.NoError(t, model.SetMerchantStoreConfig(f.root.Id, config))
	terms, err := model.SaveMerchantStoreSellerTerms(f.seller.Id, model.MerchantStoreTermsInput{Content: "This merchant delivers the chosen card specification and handles its support."})
	require.NoError(t, err)
	public, no := "public", false
	f.product, err = model.SaveMerchantStoreProduct(f.seller.Id, f.product.ID, model.MerchantStoreProductInput{
		Title: f.product.Title, PriceQuota: 1, PaymentMethods: []string{MerchantStoreExternalEpay},
		Visibility: &public, PurchaseLoginRequired: &no,
	})
	require.NoError(t, err)
	require.NoError(t, model.SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, model.ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, ""))
	a, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	b, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	require.NoError(t, model.AcceptMerchantStoreGuestDisclaimer(a.Token, model.MerchantStoreDisclaimerVersion))
	in := model.MerchantStoreCheckoutInput{GuestToken: a.Token, ProductID: f.product.ID, Quantity: 1, RequestKey: "guest-minimum", PaymentMethod: MerchantStoreExternalEpay, SellerTermsVersion: terms.Version, AcceptSellerTerms: true}
	order, _, err := model.CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	previous := http.DefaultTransport
	transport := &storeDiscountNoNetworkTransport{}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	_, err = CreateMerchantStoreGuestPaymentSession(context.Background(), b.Token, order.ID, "CNY")
	require.ErrorIs(t, err, ErrMerchantStorePaymentAccess)
	current, err := model.GetMerchantStoreGuestOrder(a.Token, order.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", current.Status)
	require.True(t, current.FeeHeld)
	var reserved int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreStock{}).Where("order_id = ? AND state = 'reserved'", order.ID).Count(&reserved).Error)
	require.EqualValues(t, 1, reserved)
	_, err = CreateMerchantStoreGuestPaymentSession(context.Background(), a.Token, order.ID, "CNY")
	var minimum *MerchantStorePaymentMinimumError
	require.ErrorAs(t, err, &minimum)
	require.True(t, minimum.OrderCancelled)
	require.Equal(t, order.ID, minimum.OrderID)
	current, err = model.GetMerchantStoreGuestOrder(a.Token, order.ID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", current.Status)
	require.False(t, current.FeeHeld)
	require.Empty(t, current.GatewaySnapshot)
	require.Zero(t, current.AmountMinor)
	var available int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreStock{}).Where("product_id = ? AND state = 'available'", f.product.ID).Count(&available).Error)
	require.EqualValues(t, 2, available)
	var seller model.User
	require.NoError(t, model.DB.First(&seller, f.seller.Id).Error)
	require.Equal(t, 10000000, seller.Quota)
	require.Zero(t, transport.calls)

	// A concurrent invoice issuance must retain both the stock and fee hold.
	in.RequestKey = "guest-issued-race"
	issued, _, err := model.CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.NoError(t, model.BindMerchantStorePaymentQuote(issued.ID, 100, "CNY", "7"))
	require.NoError(t, model.BindMerchantStorePaymentContext(issued.ID, "issued-guest-fixture", strings.Repeat("a", 64)))
	err = merchantStorePaymentPreparationFailure(issued, ErrMerchantStorePaymentMinimum, func() error {
		return model.CancelMerchantStoreGuestOrder(a.Token, issued.ID)
	})
	require.ErrorAs(t, err, &minimum)
	require.False(t, minimum.OrderCancelled)
	current, err = model.GetMerchantStoreGuestOrder(a.Token, issued.ID)
	require.NoError(t, err)
	require.Equal(t, "pending", current.Status)
	require.True(t, current.FeeHeld)
	require.Equal(t, "issued-guest-fixture", current.GatewaySnapshot)
	require.NoError(t, model.DB.Model(&model.MerchantStoreStock{}).Where("order_id = ? AND state = 'reserved'", issued.ID).Count(&reserved).Error)
	require.EqualValues(t, 1, reserved)
	require.Zero(t, transport.calls)
}
