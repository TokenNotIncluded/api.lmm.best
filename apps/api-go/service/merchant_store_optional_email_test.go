package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreOptionalPickupEmailUsesOrderSnapshot(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	require.NoError(t, model.DB.Model(&model.MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("email_pickup_link", false).Error)
	oldOrigin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = oldOrigin })

	const pickupEmail = "optional@example.test"
	const pickupCode = "private-optional-pickup-code"
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{
		BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1,
		RequestKey: "optional-email-snapshot", PaymentMethod: MerchantStoreBalance,
		PickupEmail: pickupEmail, PickupCode: pickupCode,
	})
	require.NoError(t, err)
	pickupToken, err := model.GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, "paid", order.Status)
	require.True(t, order.EmailPickupLink, "a supplied optional address enables delivery for this order")
	require.True(t, order.PickupCodeRequired, "a supplied optional code protects this order")
	require.Len(t, pickupToken, 43)
	var row model.MerchantStoreEmailDelivery
	require.NoError(t, model.DB.Where("order_id = ?", order.ID).First(&row).Error)
	require.Equal(t, "pending", row.State)
	require.Zero(t, row.Attempts)

	// No account-address verification is needed for a buyer-supplied delivery
	// address, and an account change cannot redirect the frozen order address.
	const changedAccountEmail = "different-account@example.test"
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.buyer.Id).Update("email", changedAccountEmail).Error)
	_, err = model.GetMerchantStoreVerifiedEmailAddress(f.buyer.Id)
	require.ErrorIs(t, err, model.ErrMerchantStoreEmailUnverified)
	calls := 0
	n, err := processMerchantStorePickupEmailBatch(context.Background(), 5, func(_ context.Context, email merchantStorePickupEmail) error {
		calls++
		require.Equal(t, pickupEmail, email.destination)
		require.NotEqual(t, changedAccountEmail, email.destination)
		require.Equal(t, order.TradeNo, email.tradeNo)
		require.Equal(t, "https://api.example.com/store/claim/"+pickupToken, email.pickupURL)
		require.Empty(t, email.verificationCode)
		require.False(t, email.orderSearch)
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, calls)
	require.NoError(t, model.DB.Where("order_id = ?", order.ID).First(&row).Error)
	require.Equal(t, "sent", row.State)
	require.Equal(t, 1, row.Attempts)
	require.Positive(t, row.SentAt)
	require.Empty(t, row.LeaseToken)
	require.Zero(t, row.LeaseUntil)

	var storedOrder model.MerchantStoreOrder
	require.NoError(t, model.DB.First(&storedOrder, "id = ?", order.ID).Error)
	for _, value := range []any{order, storedOrder, row} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		for _, secret := range []string{pickupEmail, f.buyer.Email, changedAccountEmail, pickupCode, pickupToken, "PRIVATE-CARD-ONE", "PRIVATE-CARD-TWO"} {
			require.NotContains(t, string(encoded), secret)
		}
		for _, field := range []string{"pickup_email_hash", "pickup_email_ciphertext", "pickup_code_hash", "pickup_token_hash", "pickup_token_ciphertext", "lease_token"} {
			require.NotContains(t, string(encoded), `"`+field+`"`)
		}
	}
	// An acknowledged row cannot be sent again by another batch.
	n, err = processMerchantStorePickupEmailBatch(context.Background(), 5, func(context.Context, merchantStorePickupEmail) error {
		calls++
		return nil
	})
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, calls)
}

func TestMerchantStoreLegacyPickupEmailTracksVerifiedCurrentAccount(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	oldOrigin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.com"
	t.Cleanup(func() { system_setting.ServerAddress = oldOrigin })
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{
		BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1,
		RequestKey: "legacy-email-address", PaymentMethod: MerchantStoreBalance,
		PickupEmail: f.buyer.Email,
	})
	require.NoError(t, err)
	pickupToken, err := model.GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	// A pre-upgrade paid order has no encrypted delivery-address snapshot.
	require.NoError(t, model.DB.Model(&model.MerchantStoreOrder{}).Where("id = ?", order.ID).Updates(map[string]any{
		"pickup_email_hash": "", "pickup_email_ciphertext": "",
	}).Error)
	require.NoError(t, model.DB.Model(&model.MerchantStoreEmailDelivery{}).Where("order_id = ?", order.ID).Update("state", "awaiting_verification").Error)
	const newEmail = "verified-new-account@example.test"
	calls := 0
	sender := func(_ context.Context, email merchantStorePickupEmail) error {
		calls++
		require.Equal(t, newEmail, email.destination)
		require.Equal(t, order.TradeNo, email.tradeNo)
		require.Equal(t, "https://api.example.com/store/claim/"+pickupToken, email.pickupURL)
		require.Empty(t, email.verificationCode)
		return nil
	}
	n, err := processMerchantStorePickupEmailBatch(context.Background(), 5, sender)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, calls)
	var row model.MerchantStoreEmailDelivery
	require.NoError(t, model.DB.Where("order_id = ?", order.ID).First(&row).Error)
	require.Equal(t, "awaiting_verification", row.State)
	require.Zero(t, row.Attempts)

	require.NoError(t, model.MarkMerchantStoreEmailVerified(f.buyer.Id, f.buyer.Email))
	require.NoError(t, model.DB.Where("order_id = ?", order.ID).First(&row).Error)
	require.Equal(t, "pending", row.State)
	// Verification applies to the exact address, so changing it before the
	// worker runs suspends delivery without consuming a retry attempt.
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.buyer.Id).Update("email", newEmail).Error)
	n, err = processMerchantStorePickupEmailBatch(context.Background(), 5, sender)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Zero(t, calls)
	require.NoError(t, model.DB.Where("order_id = ?", order.ID).First(&row).Error)
	require.Equal(t, "awaiting_verification", row.State)
	require.Equal(t, "email_unverified", row.LastErrorCode)
	require.Zero(t, row.Attempts)
	require.Empty(t, row.LeaseToken)
	require.Zero(t, row.LeaseUntil)

	require.NoError(t, model.MarkMerchantStoreEmailVerified(f.buyer.Id, newEmail))
	require.NoError(t, model.DB.Where("order_id = ?", order.ID).First(&row).Error)
	require.Equal(t, "pending", row.State)
	require.Empty(t, row.LastErrorCode)
	n, err = processMerchantStorePickupEmailBatch(context.Background(), 5, sender)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, 1, calls)
	require.NoError(t, model.DB.Where("order_id = ?", order.ID).First(&row).Error)
	require.Equal(t, "sent", row.State)
	require.Equal(t, 1, row.Attempts)
	require.Positive(t, row.SentAt)
	n, err = processMerchantStorePickupEmailBatch(context.Background(), 5, sender)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, 1, calls)
}
