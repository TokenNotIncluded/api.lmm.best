package model

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func storeUnverifyFixture(t *testing.T, f storeFixture) {
	t.Helper()
	require.NoError(t, DB.Where("user_id = ?", f.buyer.Id).Delete(&MerchantStoreVerifiedEmail{}).Error)
}
func storeLegacyEmailOrder(t *testing.T, o *MerchantStoreOrder) {
	t.Helper()
	// Keep explicit coverage for pre-snapshot orders using the account mailbox.
	require.NoError(t, DB.Model(o).Updates(map[string]any{"pickup_email_hash": "", "pickup_email_ciphertext": ""}).Error)
	o.PickupEmailHash, o.PickupEmailCiphertext = "", ""
	require.NoError(t, DB.Where("order_id = ?", o.ID).Delete(&MerchantStoreEmailDelivery{}).Error)
	require.NoError(t, enqueueMerchantStoreEmail(DB, o))
}
func TestMerchantStoreEmailVerificationDurableChallengeAndQueue(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeUnverifyFixture(t, f)
	o, _, e := CreateMerchantStoreOrder(f.checkout("await-email", "balance"))
	require.NoError(t, e)
	storeLegacyEmailOrder(t, o)
	require.Equal(t, "awaiting_verification", o.EmailDeliveryStatus)
	_, e = ClaimMerchantStoreEmailDelivery(common.GetTimestamp())
	require.Error(t, e)
	email, code, e := BeginMerchantStoreEmailVerification(f.buyer.Id)
	require.NoError(t, e)
	require.Equal(t, "store-buyer@example.test", email)
	require.Len(t, code, 6)
	require.NotContains(t, code, "-")
	for _, r := range code {
		require.True(t, r >= '0' && r <= '9')
	}
	var c MerchantStoreEmailVerificationChallenge
	require.NoError(t, DB.First(&c, "user_id = ?", f.buyer.Id).Error)
	require.True(t, strings.HasPrefix(c.CodeCiphertext, "v1:"))
	require.Equal(t, storeHash(email), c.EmailHash)
	require.EqualValues(t, 600, c.ExpiresAt-c.SentAt)
	raw, e := json.Marshal(c)
	require.NoError(t, e)
	require.JSONEq(t, "{}", string(raw))
	require.NotContains(t, c.CodeCiphertext, email)
	_, _, e = BeginMerchantStoreEmailVerification(f.buyer.Id)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationCooldown)
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	require.ErrorIs(t, VerifyMerchantStoreEmailVerification(f.buyer.Id, wrong), ErrMerchantStoreEmailVerificationInvalid)
	require.NoError(t, DB.First(&c, "user_id = ?", f.buyer.Id).Error)
	require.Equal(t, 1, c.Attempts)
	require.NoError(t, VerifyMerchantStoreEmailVerification(f.buyer.Id, code))
	address, e := GetMerchantStoreVerifiedEmailAddress(f.buyer.Id)
	require.NoError(t, e)
	require.Equal(t, email, address)
	require.ErrorIs(t, VerifyMerchantStoreEmailVerification(f.buyer.Id, code), ErrMerchantStoreEmailVerificationInvalid)
	row, e := ClaimMerchantStoreEmailDelivery(common.GetTimestamp())
	require.NoError(t, e)
	require.Equal(t, o.ID, row.OrderID)
	view, e := GetMerchantStoreOrder(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Equal(t, "sending", view.EmailDeliveryStatus)
	seller, e := GetMerchantStoreOrder(f.seller.Id, o.ID)
	require.NoError(t, e)
	require.Empty(t, seller.EmailDeliveryStatus)
}
func TestMerchantStoreEmailWrongAttemptsCommitAndExpireFailClosed(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeUnverifyFixture(t, f)
	_, code, e := BeginMerchantStoreEmailVerification(f.buyer.Id)
	require.NoError(t, e)
	wrong := "000000"
	if wrong == code {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		require.ErrorIs(t, VerifyMerchantStoreEmailVerification(f.buyer.Id, wrong), ErrMerchantStoreEmailVerificationInvalid)
	}
	var c MerchantStoreEmailVerificationChallenge
	require.NoError(t, DB.First(&c, "user_id = ?", f.buyer.Id).Error)
	require.Equal(t, 5, c.Attempts)
	require.ErrorIs(t, VerifyMerchantStoreEmailVerification(f.buyer.Id, code), ErrMerchantStoreEmailVerificationInvalid)
	_, e = GetMerchantStoreVerifiedEmailAddress(f.buyer.Id)
	require.ErrorIs(t, e, ErrMerchantStoreEmailUnverified)
	_, _, e = BeginMerchantStoreEmailVerification(f.buyer.Id)
	require.ErrorIs(t, e, ErrMerchantStoreEmailVerificationCooldown)
	require.NoError(t, DB.Model(&MerchantStoreEmailVerificationChallenge{}).Where("user_id = ?", f.buyer.Id).Update("sent_at", common.GetTimestamp()-61).Error)
	_, newCode, e := BeginMerchantStoreEmailVerification(f.buyer.Id)
	require.NoError(t, e)
	require.NoError(t, DB.Model(&MerchantStoreEmailVerificationChallenge{}).Where("user_id = ?", f.buyer.Id).Update("expires_at", common.GetTimestamp()-1).Error)
	require.ErrorIs(t, VerifyMerchantStoreEmailVerification(f.buyer.Id, newCode), ErrMerchantStoreEmailVerificationInvalid)
	_, e = GetMerchantStoreVerifiedEmailAddress(f.buyer.Id)
	require.ErrorIs(t, e, ErrMerchantStoreEmailUnverified)
}
func TestMerchantStoreEmailAddressChangeInvalidatesFactChallengeAndLease(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o, _, e := CreateMerchantStoreOrder(f.checkout("email-change", "balance"))
	require.NoError(t, e)
	storeLegacyEmailOrder(t, o)
	leased, e := ClaimMerchantStoreEmailDelivery(common.GetTimestamp())
	require.NoError(t, e)
	_, oldCode, e := BeginMerchantStoreEmailVerification(f.buyer.Id)
	require.NoError(t, e)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).Update("email", "new-buyer@example.test").Error)
	_, e = GetMerchantStoreVerifiedEmailAddress(f.buyer.Id)
	require.ErrorIs(t, e, ErrMerchantStoreEmailUnverified)
	require.ErrorIs(t, VerifyMerchantStoreEmailVerification(f.buyer.Id, oldCode), ErrMerchantStoreEmailVerificationInvalid)
	require.NoError(t, DeferMerchantStoreEmailVerification(leased.ID, leased.LeaseToken))
	var queued MerchantStoreEmailDelivery
	require.NoError(t, DB.First(&queued, "id = ?", leased.ID).Error)
	require.Equal(t, "awaiting_verification", queued.State)
	require.Zero(t, queued.Attempts)
	require.Empty(t, queued.LeaseToken)
	require.ErrorIs(t, MarkMerchantStoreEmailVerified(f.buyer.Id, "store-buyer@example.test"), ErrMerchantStoreEmailUnverified)
	require.NoError(t, DB.Model(&MerchantStoreEmailVerificationChallenge{}).Where("user_id = ?", f.buyer.Id).Update("sent_at", common.GetTimestamp()-61).Error)
	email, code, e := BeginMerchantStoreEmailVerification(f.buyer.Id)
	require.NoError(t, e)
	require.Equal(t, "new-buyer@example.test", email)
	require.NoError(t, VerifyMerchantStoreEmailVerification(f.buyer.Id, code))
	again, e := ClaimMerchantStoreEmailDelivery(common.GetTimestamp())
	require.NoError(t, e)
	require.Equal(t, o.ID, again.OrderID)
	require.NotEqual(t, leased.LeaseToken, again.LeaseToken)
}
func TestMerchantStoreTransferLedgerMatchesWalletDeltas(t *testing.T) {
	for _, method := range []string{"balance", "platform:waffo_pancake", "external:epay", "cancel"} {
		t.Run(method, func(t *testing.T) {
			gateway := method
			if method == "cancel" {
				gateway = "platform:waffo_pancake"
			}
			f := newStoreFixture(t, gateway)
			if gateway == "external:epay" {
				_, e := SaveMerchantStoreGateway(f.seller.Id, gateway, true, `{"key":"fixture"}`)
				require.NoError(t, e)
			}
			before := map[int]int{f.buyer.Id: 10000000, f.seller.Id: 10000000, f.root.Id: 0}
			o, _, e := CreateMerchantStoreOrder(f.checkout(method, gateway))
			require.NoError(t, e)
			if method == "cancel" {
				require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
			} else if method != "balance" {
				require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
				require.NoError(t, CompleteMerchantStorePayment(o.ID, "receipt"))
			}
			var transfers []MerchantStoreTransfer
			require.NoError(t, DB.Where("order_id = ?", o.ID).Find(&transfers).Error)
			delta := map[int]int{}
			for _, tr := range transfers {
				delta[tr.FromUserID] -= tr.Quota
				delta[tr.ToUserID] += tr.Quota
			}
			for id, quota := range before {
				var u User
				require.NoError(t, DB.First(&u, id).Error)
				require.Equal(t, u.Quota-quota, delta[id], "user %d ledger matches wallet", id)
			}
		})
	}
}
