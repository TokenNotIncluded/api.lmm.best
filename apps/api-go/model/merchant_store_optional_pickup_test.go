package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreOptionalPickupCodeFilledBecomesClaimProtection(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("pickup_code_required", false).Error)
	in := f.checkout("optional-code-filled", "balance")
	in.PickupCode = "optional-private-code"
	in.PickupEmail = "pickup@example.test"
	order, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, order.PickupCodeRequired)
	require.NotEmpty(t, order.PickupCodeHash)
	require.NotEqual(t, in.PickupCode, order.PickupCodeHash)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	metadata, err := InspectMerchantStoreClaim(token)
	require.NoError(t, err)
	require.True(t, metadata.PickupCodeRequired)
	for _, code := range []string{"", "another-private-code"} {
		_, err = ClaimMerchantStoreOrder(token, code, f.buyer.Id)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
	}
	claim, err := ClaimMerchantStoreOrder(token, in.PickupCode, f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
}

func TestMerchantStoreOptionalPickupCodeEmptyDoesNotRequireCode(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("pickup_code_required", false).Error)
	in := f.checkout("optional-code-empty", "balance")
	in.PickupCode = ""
	in.PickupEmail = "pickup@example.test"
	order, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, created)
	require.False(t, order.PickupCodeRequired)
	require.Empty(t, order.PickupCodeHash)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	metadata, err := InspectMerchantStoreClaim(token)
	require.NoError(t, err)
	require.False(t, metadata.PickupCodeRequired)
	claim, err := ClaimMerchantStoreOrder(token, "", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
}

func TestMerchantStorePickupCodeRequiredAndOptionalMinimumLength(t *testing.T) {
	for _, test := range []struct {
		name     string
		required bool
		code     string
	}{
		{name: "required missing", required: true, code: ""},
		{name: "required too short", required: true, code: "1234567"},
		{name: "optional filled too short", required: false, code: "1234567"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newStoreFixture(t, "balance")
			require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("pickup_code_required", test.required).Error)
			in := f.checkout("invalid-code", "balance")
			in.PickupCode = test.code
			in.PickupEmail = "pickup@example.test"
			_, created, err := CreateMerchantStoreOrder(in)
			require.ErrorIs(t, err, ErrMerchantStoreInput)
			require.False(t, created)
			var count int64
			require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("state = ?", "available").Count(&count).Error)
			require.EqualValues(t, 2, count)
			storeBalance(t, f.buyer.Id, 10000000)
		})
	}
}

func TestMerchantStoreOptionalPickupEmailFilledEnqueuesEncryptedSnapshot(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("email_pickup_link", false).Error)
	in := f.checkout("optional-email-filled", "balance")
	in.PickupEmail = "  Independent-Pickup@Example.TEST  "
	order, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, order.EmailPickupLink)
	require.Equal(t, "pending", order.EmailDeliveryStatus)
	require.NotEmpty(t, order.PickupEmailHash)
	require.NotEmpty(t, order.PickupEmailCiphertext)
	require.NotContains(t, order.PickupEmailCiphertext, "Independent-Pickup@example.test")
	var delivery MerchantStoreEmailDelivery
	require.NoError(t, DB.Where("order_id = ?", order.ID).First(&delivery).Error)
	require.Equal(t, "pending", delivery.State)
	for _, value := range []any{order, delivery} {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		for _, secret := range []string{"Independent-Pickup@example.test", order.PickupEmailHash, order.PickupEmailCiphertext, in.PickupCode} {
			require.NotContains(t, string(encoded), secret)
		}
	}
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).Update("email", "changed-account@example.test").Error)
	email, err := GetMerchantStoreOrderDeliveryEmail(f.buyer.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, "Independent-Pickup@example.test", email)
	_, err = GetMerchantStoreOrderDeliveryEmail(f.seller.Id, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
}

func TestMerchantStoreOptionalPickupEmailEmptyDoesNotEnqueue(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("email_pickup_link", false).Error)
	in := f.checkout("optional-email-empty", "balance")
	in.PickupEmail = ""
	order, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, created)
	require.False(t, order.EmailPickupLink)
	require.Empty(t, order.PickupEmailHash)
	require.Empty(t, order.PickupEmailCiphertext)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Count(&count).Error)
	require.Zero(t, count)
	_, err = GetMerchantStoreOrderDeliveryEmail(f.buyer.Id, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
}

func TestMerchantStorePickupEmailRequiredAndOptionalValidateBeforePurchase(t *testing.T) {
	for _, test := range []struct {
		name     string
		required bool
		email    string
	}{
		{name: "required missing", required: true, email: ""},
		{name: "required whitespace", required: true, email: "  "},
		{name: "required malformed", required: true, email: "not-an-email"},
		{name: "optional filled malformed", required: false, email: "not-an-email"},
		{name: "optional header injection", required: false, email: "buyer@example.test\r\nBcc: another@example.test"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newStoreFixture(t, "balance")
			require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("email_pickup_link", test.required).Error)
			in := f.checkout("invalid-email", "balance")
			in.PickupEmail = test.email
			_, created, err := CreateMerchantStoreOrder(in)
			require.ErrorIs(t, err, ErrMerchantStoreInput)
			require.False(t, created)
			var count int64
			require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Count(&count).Error)
			require.Zero(t, count)
			storeBalance(t, f.buyer.Id, 10000000)
		})
	}
}

func TestMerchantStorePickupEmailLegacySnapshotAbsenceUsesVerifiedAccount(t *testing.T) {
	f := newStoreFixture(t, "balance")
	in := f.checkout("legacy-email", "balance")
	in.PickupEmail = "original-snapshot@example.test"
	order, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	// New checkout always supplies the required field. Clearing only the
	// persisted snapshot reproduces an order created by the earlier version.
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", order.ID).Updates(map[string]any{"pickup_email_hash": "", "pickup_email_ciphertext": ""}).Error)
	email, err := GetMerchantStoreOrderDeliveryEmail(f.buyer.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, "store-buyer@example.test", email)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).Update("email", "legacy-new@example.test").Error)
	_, err = GetMerchantStoreOrderDeliveryEmail(f.buyer.Id, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreEmailUnverified)
	require.NoError(t, MarkMerchantStoreEmailVerified(f.buyer.Id, "legacy-new@example.test"))
	email, err = GetMerchantStoreOrderDeliveryEmail(f.buyer.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, "legacy-new@example.test", email)
}
