package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreGuestEmailOutboxUsesOriginalIdentityAndLayout(t *testing.T) {
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 2, PaymentMethod: MerchantStoreBalance, RequestKey: "guest-email-fixture", PickupEmail: "Original@example.test", PickupCode: "protected-code"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, model.MerchantStoreWriterCapability, 5, "actual phase-5 capability is required")
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
	session, err := model.CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	guest, err := model.ResolveMerchantStoreGuest(model.DB, session.Token)
	require.NoError(t, err)
	challenge, err := model.BeginMerchantStoreGuestEmailVerification(session.Token, "Original@example.test")
	require.NoError(t, err)
	require.NoError(t, model.ConfirmMerchantStoreGuestEmailVerification(session.Token, challenge.Email, challenge.ChallengeID, challenge.Code))
	require.NoError(t, model.DB.Model(order).Updates(map[string]any{"buyer_id": 0, "guest_id": guest.ID, "pickup_login_required": false, "variant_name": "商家自定义规格 / 三层组合"}).Error)
	require.NoError(t, model.DB.Model(&model.MerchantStoreEmailDelivery{}).Where("order_id = ?", order.ID).Update("buyer_id", 0).Error)
	require.NoError(t, model.DB.Model(guest).Update("expires_at", common.GetTimestamp()-1).Error)
	oldOrigin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = oldOrigin })
	merchantStoreSMTPTestSettings(t)
	calls := 0
	n, err := processMerchantStorePickupEmailBatch(context.Background(), 5, func(_ context.Context, email merchantStorePickupEmail) error {
		calls++
		require.Equal(t, "Original@example.test", email.destination)
		require.Equal(t, order.TradeNo, email.tradeNo)
		require.Equal(t, "商家自定义规格 / 三层组合", email.details.VariantName)
		require.Equal(t, 2, email.details.Quantity)
		message, _, _, err := merchantStorePickupEmailMessage(email)
		require.NoError(t, err)
		for _, body := range merchantStoreEmailTestBodies(t, message) {
			require.Contains(t, body, order.TradeNo)
			require.Contains(t, body, "商家自定义规格 / 三层组合")
			require.Contains(t, body, "/store/claim/")
			require.NotContains(t, body, "protected-code")
			require.NotContains(t, body, "PRIVATE-CARD")
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
	var row model.MerchantStoreEmailDelivery
	require.NoError(t, model.DB.First(&row, "id = ?", order.ID).Error)
	raw, err := json.Marshal(row)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "Original@example.test")
	require.NotContains(t, string(raw), session.Token)
	t.Run("real_guest_paid_order", assertMerchantStoreRealGuestEmailFlow)
}

func TestMerchantStoreGuestEmailClosedOrdersAndSMTPRetry(t *testing.T) {
	for _, status := range []string{"cancelled", "expired", "pending", "reconciliation_pending", "refunded"} {
		t.Run(status, func(t *testing.T) {
			f := merchantStoreServiceDB(t, MerchantStoreBalance)
			order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, PaymentMethod: MerchantStoreBalance, RequestKey: "closed-" + status, PickupEmail: f.buyer.Email})
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(order).Update("status", status).Error)
			calls := 0
			n, err := processMerchantStorePickupEmailBatch(context.Background(), 5, func(context.Context, merchantStorePickupEmail) error { calls++; return nil })
			require.NoError(t, err)
			require.Equal(t, 1, n)
			require.Zero(t, calls)
			var row model.MerchantStoreEmailDelivery
			require.NoError(t, model.DB.First(&row, "id = ?", order.ID).Error)
			require.Equal(t, "cancelled", row.State)
			require.Empty(t, row.LeaseToken)
		})
	}
	f := merchantStoreServiceDB(t, MerchantStoreBalance)
	order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, PaymentMethod: MerchantStoreBalance, RequestKey: "smtp-retry", PickupEmail: f.buyer.Email})
	require.NoError(t, err)
	oldOrigin := system_setting.ServerAddress
	system_setting.ServerAddress = "https://api.example.test"
	t.Cleanup(func() { system_setting.ServerAddress = oldOrigin })
	n, err := processMerchantStorePickupEmailBatch(context.Background(), 1, func(context.Context, merchantStorePickupEmail) error {
		return errors.New("private upstream address and token must not persist")
	})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	var row model.MerchantStoreEmailDelivery
	require.NoError(t, model.DB.First(&row, "id = ?", order.ID).Error)
	require.Equal(t, "retry", row.State)
	require.Equal(t, "smtp_delivery_failed", row.LastErrorCode)
	require.Empty(t, row.LeaseToken)
}

func TestMerchantStoreGuestEmailVerificationMultipartHasNoPickupInformation(t *testing.T) {
	merchantStoreSMTPTestSettings(t)
	oldName := common.SystemName
	common.SystemName = `<img src=x onerror="steal()">`
	t.Cleanup(func() { common.SystemName = oldName })
	message, _, destination, err := merchantStorePickupEmailMessage(merchantStorePickupEmail{destination: "address@example.test", verificationCode: "001234"})
	require.NoError(t, err)
	require.Equal(t, "address@example.test", destination)
	bodies := merchantStoreEmailTestBodies(t, message)
	require.Len(t, bodies, 2)
	for _, body := range bodies {
		require.Contains(t, body, "001234")
		require.NotContains(t, body, "/store/claim/")
		require.NotContains(t, body, "PRIVATE-CARD")
	}
	require.NotContains(t, bodies["text/html"], `<img src=x`)
	require.Contains(t, bodies["text/html"], "&lt;img")
	for _, invalid := range []merchantStorePickupEmail{
		{destination: "address@example.test\r\nBcc: steal@example.test", verificationCode: "001234"},
		{destination: "address@example.test", verificationCode: "001234", pickupURL: "https://api.example.test/store/claim/" + strings.Repeat("a", 43)},
		{destination: "address@example.test", verificationCode: "001234", details: &model.MerchantStoreOrderPickupDetails{ProductTitle: "private"}},
	} {
		_, _, _, err := merchantStorePickupEmailMessage(invalid)
		require.Error(t, err)
	}
}

func TestMerchantStoreGuestEmailPayloadRetainsAccountDisablePolicy(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "deleted"}[deleted], func(t *testing.T) {
			f := merchantStoreServiceDB(t, MerchantStoreBalance)
			order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, PaymentMethod: MerchantStoreBalance, RequestKey: "disabled-account", PickupEmail: "frozen@example.test"})
			require.NoError(t, err)
			if deleted {
				require.NoError(t, model.DB.Delete(&f.buyer).Error)
			} else {
				require.NoError(t, model.DB.Model(&f.buyer).Update("status", common.UserStatusDisabled).Error)
			}
			calls := 0
			_, err = processMerchantStorePickupEmailBatch(context.Background(), 1, func(context.Context, merchantStorePickupEmail) error { calls++; return nil })
			require.NoError(t, err)
			require.Zero(t, calls)
			var row model.MerchantStoreEmailDelivery
			require.NoError(t, model.DB.First(&row, "id = ?", order.ID).Error)
			require.Equal(t, "retry", row.State)
		})
	}
}
