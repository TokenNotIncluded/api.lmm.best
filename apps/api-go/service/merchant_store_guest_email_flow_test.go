package service

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/stretchr/testify/require"
)

// This supplements the lease-isolation fixture with a genuine guest purchase.
// Provider signatures and SMTP rendering are real code; no provider HTTP or
// SMTP delivery occurs. The reviewed phase-5 runtime is a strict prerequisite.
func assertMerchantStoreRealGuestEmailFlow(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreExternalPancake)
	merchantStoreTestCreditBasis(t)
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 5)
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	terms, err := model.SaveMerchantStoreSellerTerms(f.seller.Id, model.MerchantStoreTermsInput{Content: "Merchant delivery and support instructions"})
	require.NoError(t, err)
	guestCheckout := false
	f.product, err = model.SaveMerchantStoreProduct(f.seller.Id, f.product.ID, model.MerchantStoreProductInput{
		Title: "Merchant-configured product", Description: "Purchased product instructions", PriceQuota: 500000,
		PaymentMethods: []string{MerchantStoreExternalPancake}, EmailPickupLink: true, PickupCodeRequired: true,
		PurchaseLoginRequired: &guestCheckout, Links: []model.MerchantStoreLink{{Title: "Delivery help", URL: "https://delivery.example.test/help"}},
	})
	require.NoError(t, err)
	variantID := model.MerchantStoreDefaultVariantID(f.product.ID)
	variant, err := model.SaveMerchantStoreVariant(f.seller.Id, f.product.ID, variantID, model.MerchantStoreVariantInput{
		Name: "商家自定义规格 / 三层组合", PriceQuota: 500000, Template: "card-key", Enabled: true,
	})
	require.NoError(t, err)
	require.NoError(t, model.SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, model.ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, ""))
	session, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	challenge, err := model.BeginMerchantStoreGuestEmailVerification(session.Token, "Original@EXAMPLE.test")
	require.NoError(t, err)
	require.NoError(t, model.ConfirmMerchantStoreGuestEmailVerification(session.Token, challenge.Email, challenge.ChallengeID, challenge.Code))
	require.NoError(t, model.AcceptMerchantStoreGuestDisclaimer(session.Token, model.MerchantStoreDisclaimerVersion))
	input := model.MerchantStoreCheckoutInput{GuestToken: session.Token, ProductID: f.product.ID, VariantID: variant.ID,
		Quantity: 2, PaymentMethod: MerchantStoreExternalPancake, RequestKey: "real-guest-email", PickupEmail: challenge.Email,
		PickupCode: "protected-code", SellerTermsVersion: terms.Version, AcceptSellerTerms: true}
	order, created, err := model.CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.True(t, created)
	require.Zero(t, order.BuyerID)
	require.NotEmpty(t, order.GuestID)
	require.Equal(t, variant.Name, order.VariantName)
	require.Equal(t, 1000000, order.PriceQuota)
	other, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	_, err = model.GetMerchantStoreGuestOrder(other.Token, order.ID)
	require.ErrorIs(t, err, model.ErrMerchantStoreDenied)
	key, private := merchantStorePancakeTestKey(t)
	config := merchantStoreGatewayConfig{MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv", PrivateKey: private,
		StoreID: "STO_AbCdEfGhIjKlMnOpQrStUv", ProductID: "PROD_AbCdEfGhIjKlMnOpQrStUv", Environment: "prod", Currency: "USD"}
	require.NoError(t, model.BindMerchantStorePaymentQuote(order.ID, 200, "USD", "1"))
	frozen := merchantStorePaymentContext{Provider: MerchantStoreExternalPancake, Config: config, AmountMinor: 200,
		Currency: "USD", FrozenRate: "1", Origin: "https://api.example.test", PublicOrigin: "https://api.example.test", ExpiresIn: 1800}
	encrypted, err := encryptMerchantStorePaymentValue(merchantStorePaymentContextPurpose(order.ID), frozen)
	require.NoError(t, err)
	scope, err := merchantStorePaymentScopeHash(frozen)
	require.NoError(t, err)
	require.NoError(t, model.BindMerchantStorePaymentContext(order.ID, encrypted, scope))
	data := map[string]any{"orderId": "ORD_AbCdEfGhIjKlMnOpQrStUv", "orderStatus": "completed", "paymentStatus": "succeeded",
		"paymentId": "PAY_AbCdEfGhIjKlMnOpQrStUv", "chargedAmount": "2.20", "orderMerchantExternalId": order.TradeNo,
		"merchantProvidedBuyerIdentity": model.MerchantStoreOrderBuyerIdentity(order), "currency": "USD", "amount": "2.20", "taxAmount": "0.20",
		"orderMetadata": map[string]string{"lmm_store_order_id": order.ID, "lmm_store_product_id": order.ProductID,
			"lmm_pancake_product_id": config.ProductID, "lmm_store_seller_id": strconv.Itoa(order.SellerID)}}
	event := map[string]any{"id": data["orderId"], "eventId": "guest-email-completed", "eventType": "order.completed",
		"mode": "prod", "storeId": config.StoreID, "timestamp": time.Now().UTC().Format(time.RFC3339Nano), "data": data}
	data["merchantProvidedBuyerIdentity"] = model.MerchantStoreOrderBuyerIdentity(&model.MerchantStoreOrder{GuestID: other.GuestID})
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	require.Error(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	data["merchantProvidedBuyerIdentity"] = model.MerchantStoreOrderBuyerIdentity(order)
	payload, err = json.Marshal(event)
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		require.NoError(t, HandleMerchantStorePancakeWebhook(context.Background(), "external", order.SellerID, "prod", payload, merchantStorePancakeSigned(t, key, payload)))
	}
	var basis model.MerchantStoreRefundPaymentBasis
	require.NoError(t, model.DB.First(&basis, "order_id = ?", order.ID).Error)
	require.Equal(t, data["paymentId"], basis.PaymentReference)
	require.EqualValues(t, 220, basis.AmountMinor, "actual charged native amount includes tax; credit price stays frozen")
	var seller, root model.User
	require.NoError(t, model.DB.First(&seller, f.seller.Id).Error)
	require.NoError(t, model.DB.First(&root, f.root.Id).Error)
	require.Equal(t, f.seller.Quota-10000, seller.Quota, "external merchant fee is debited once")
	require.Equal(t, 10000, root.Quota)
	replayed, created, err := model.CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, order.ID, replayed.ID)
	oldOrigin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = oldOrigin })
	merchantStoreSMTPTestSettings(t)
	calls := 0
	n, err := processMerchantStorePickupEmailBatch(context.Background(), 5, func(_ context.Context, email merchantStorePickupEmail) error {
		calls++
		require.Equal(t, challenge.Email, email.destination)
		require.Equal(t, variant.Name, email.details.VariantName)
		require.Equal(t, 2, email.details.Quantity)
		require.Equal(t, "Purchased product instructions", email.details.ProductDescription)
		message, _, _, err := merchantStorePickupEmailMessage(email)
		require.NoError(t, err)
		for _, body := range merchantStoreEmailTestBodies(t, message) {
			require.Contains(t, body, "/store/claim/")
			require.NotContains(t, body, "PRIVATE-CARD")
			require.NotContains(t, body, input.PickupCode)
			require.NotContains(t, body, session.Token)
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, calls)
	n, err = processMerchantStorePickupEmailBatch(context.Background(), 5, func(context.Context, merchantStorePickupEmail) error { calls++; return nil })
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, calls)
	pickup, err := model.GetMerchantStoreGuestPickupToken(session.Token, order.ID)
	require.NoError(t, err)
	_, err = model.ClaimMerchantStoreOrder(pickup, "wrong-code", 0)
	require.Error(t, err)
	claim, err := model.ClaimMerchantStoreOrder(pickup, input.PickupCode, 0)
	require.NoError(t, err)
	require.Equal(t, variant.Name, claim.VariantName)
	require.Equal(t, []string{"PRIVATE-CARD-ONE", "PRIVATE-CARD-TWO"}, claim.Items)
	var queued int64
	require.NoError(t, model.DB.Model(&model.MerchantStoreEmailDelivery{}).Where("order_id = ?", order.ID).Count(&queued).Error)
	require.EqualValues(t, 1, queued)
}
