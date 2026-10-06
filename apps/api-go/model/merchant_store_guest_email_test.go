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

func storeEmailGuestFixture(t *testing.T) (string, *MerchantStoreGuest) {
	t.Helper()
	token, err := storeToken()
	require.NoError(t, err)
	guest := &MerchantStoreGuest{ID: uuid.NewString(), TokenHash: storeHash(token), CreatedAt: common.GetTimestamp(), ExpiresAt: common.GetTimestamp() + 86400}
	require.NoError(t, DB.Create(guest).Error)
	return token, guest
}

func storeGuestEmailTestGate(t *testing.T) {
	t.Helper()
	require.GreaterOrEqual(t, MerchantStoreWriterCapability, 5, "positive tests require the actual reviewed phase-5 binary, not a bypass")
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "5").Error)
}

func storeGuestEmailAllowResend(t *testing.T, guestID string) {
	t.Helper()
	require.NoError(t, DB.Model(&MerchantStoreGuestEmailVerification{}).Where("guest_id = ?", guestID).Update("sent_at", common.GetTimestamp()-61).Error)
}

func TestMerchantStoreGuestEmailIdentityAndPurposeIsolation(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeGuestEmailTestGate(t)
	a, ga := storeEmailGuestFixture(t)
	b, gb := storeEmailGuestFixture(t)
	challenge, err := BeginMerchantStoreGuestEmailVerification(a, "  Owner@EXAMPLE.test  ")
	require.NoError(t, err)
	require.Equal(t, "Owner@example.test", challenge.Email)
	require.Len(t, challenge.Code, 6)
	require.Len(t, challenge.ChallengeID, 36)
	require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(b, challenge.Email, challenge.ChallengeID, challenge.Code), ErrMerchantStoreEmailVerificationInvalid)
	require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(a, "owner@example.test", challenge.ChallengeID, challenge.Code), ErrMerchantStoreEmailVerificationInvalid, "local part is case-sensitive")
	_, accountCode, err := BeginMerchantStoreEmailVerification(f.buyer.Id)
	require.NoError(t, err)
	if accountCode == challenge.Code {
		accountCode = "wrong!"
	}
	require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(a, challenge.Email, challenge.ChallengeID, accountCode), ErrMerchantStoreEmailVerificationInvalid)
	searchID, searchCode, _, err := BeginMerchantStoreOrderSearch(challenge.Email)
	require.NoError(t, err)
	require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(a, challenge.Email, searchID, searchCode), ErrMerchantStoreEmailVerificationInvalid)
	require.NoError(t, ConfirmMerchantStoreGuestEmailVerification(a, challenge.Email, challenge.ChallengeID, challenge.Code))
	verified, err := GetMerchantStoreGuestEmailStatus(a, challenge.Email)
	require.NoError(t, err)
	require.True(t, verified)
	verified, err = GetMerchantStoreGuestEmailStatus(b, challenge.Email)
	require.NoError(t, err)
	require.False(t, verified)
	require.NotEqual(t, ga.ID, gb.ID)
	require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(a, challenge.Email, challenge.ChallengeID, challenge.Code), ErrMerchantStoreEmailVerificationInvalid)
	var row MerchantStoreGuestEmailVerification
	require.NoError(t, DB.First(&row, "guest_id = ?", ga.ID).Error)
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(encoded))
	require.NotContains(t, row.CodeCiphertext, challenge.Code)
	for _, token := range []string{"", "bad", strings.Repeat("a", 42)} {
		_, err = BeginMerchantStoreGuestEmailVerification(token, challenge.Email)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
	}
	require.NoError(t, DB.Model(ga).Update("expires_at", common.GetTimestamp()-1).Error)
	_, err = GetMerchantStoreGuestEmailStatus(a, challenge.Email)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
}

func TestMerchantStoreGuestEmailResendChangeAndBudgets(t *testing.T) {
	newStoreFixture(t, "balance")
	storeGuestEmailTestGate(t)
	token, guest := storeEmailGuestFixture(t)
	first, err := BeginMerchantStoreGuestEmailVerification(token, "first@example.test")
	require.NoError(t, err)
	_, err = BeginMerchantStoreGuestEmailVerification(token, "next@example.test")
	require.ErrorIs(t, err, ErrMerchantStoreEmailVerificationCooldown)
	require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(token, first.Email, first.ChallengeID, "invalid"), ErrMerchantStoreEmailVerificationInvalid)
	storeGuestEmailAllowResend(t, guest.ID)
	second, err := BeginMerchantStoreGuestEmailVerification(token, "next@example.test")
	require.NoError(t, err)
	require.Equal(t, first.ExpiresAt, second.ExpiresAt, "address change cannot restart the deadline")
	require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(token, first.Email, first.ChallengeID, first.Code), ErrMerchantStoreEmailVerificationInvalid)
	wrong := "000000"
	if wrong == second.Code {
		wrong = "111111"
	}
	for i := 0; i < 4; i++ {
		require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(token, second.Email, second.ChallengeID, wrong), ErrMerchantStoreEmailVerificationInvalid)
	}
	require.ErrorIs(t, ConfirmMerchantStoreGuestEmailVerification(token, second.Email, second.ChallengeID, second.Code), ErrMerchantStoreEmailVerificationInvalid)
	storeGuestEmailAllowResend(t, guest.ID)
	_, err = BeginMerchantStoreGuestEmailVerification(token, "third@example.test")
	require.ErrorIs(t, err, ErrMerchantStoreEmailVerificationCooldown)
	var rows []MerchantStoreGuestEmailVerification
	require.NoError(t, DB.Where("guest_id = ?", guest.ID).Find(&rows).Error)
	var attempts int
	for _, row := range rows {
		attempts += row.Attempts
	}
	require.Equal(t, 5, attempts, "wrong-code budget really commits across addresses")
	require.NoError(t, DB.Model(&MerchantStoreGuestEmailVerification{}).Where("guest_id = ?", guest.ID).Update("expires_at", common.GetTimestamp()-1).Error)
	fresh, err := BeginMerchantStoreGuestEmailVerification(token, "third@example.test")
	require.NoError(t, err)
	require.NoError(t, ConfirmMerchantStoreGuestEmailVerification(token, fresh.Email, fresh.ChallengeID, fresh.Code))
	// A verified address remains a fact when another address is verified later.
	storeGuestEmailAllowResend(t, guest.ID)
	next, err := BeginMerchantStoreGuestEmailVerification(token, "fourth@example.test")
	require.NoError(t, err)
	require.NoError(t, ConfirmMerchantStoreGuestEmailVerification(token, next.Email, next.ChallengeID, next.Code))
	for _, email := range []string{fresh.Email, next.Email} {
		verified, err := GetMerchantStoreGuestEmailStatus(token, email)
		require.NoError(t, err)
		require.True(t, verified)
	}
	storeGuestEmailAllowResend(t, guest.ID)
	times := make([]int64, 10)
	for i := range times {
		times[i] = common.GetTimestamp() - 120
	}
	require.NoError(t, DB.Model(&MerchantStoreGuestEmailVerification{}).Where("guest_id = ? AND email_hash = ?", guest.ID, storeHash(next.Email)).Update("sent_times", mustStoreGuestEmailJSON(t, times)).Error)
	_, err = BeginMerchantStoreGuestEmailVerification(token, "fifth@example.test")
	require.ErrorIs(t, err, ErrMerchantStoreEmailVerificationCooldown, "rolling budget spans addresses")
}

func mustStoreGuestEmailJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

func TestMerchantStoreGuestEmailCheckoutBindingAndConcurrentSingleUse(t *testing.T) {
	newStoreFixture(t, "balance")
	storeGuestEmailTestGate(t)
	token, guest := storeEmailGuestFixture(t)
	challenge, err := BeginMerchantStoreGuestEmailVerification(token, "a@example.test")
	require.NoError(t, err)
	require.NoError(t, marketTransaction(DB, func(tx *gorm.DB) error { return storeRequireGuestCheckoutEmail(tx, guest.ID, false, "") }))
	require.ErrorIs(t, marketTransaction(DB, func(tx *gorm.DB) error { return storeRequireGuestCheckoutEmail(tx, guest.ID, true, "") }), ErrMerchantStoreEmailUnverified)
	require.ErrorIs(t, marketTransaction(DB, func(tx *gorm.DB) error { return storeRequireGuestCheckoutEmail(tx, guest.ID, false, challenge.Email) }), ErrMerchantStoreEmailUnverified)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- ConfirmMerchantStoreGuestEmailVerification(token, challenge.Email, challenge.ChallengeID, challenge.Code)
		}()
	}
	wg.Wait()
	close(results)
	passed := 0
	for err := range results {
		if err == nil {
			passed++
		} else {
			require.ErrorIs(t, err, ErrMerchantStoreEmailVerificationInvalid)
		}
	}
	require.Equal(t, 1, passed, "SQLite single-connection fixture verifies single consumption, not PG contention")
	require.NoError(t, marketTransaction(DB, func(tx *gorm.DB) error { return storeRequireGuestCheckoutEmail(tx, guest.ID, true, challenge.Email) }))
	require.ErrorIs(t, marketTransaction(DB, func(tx *gorm.DB) error {
		return storeRequireGuestCheckoutEmail(tx, guest.ID, false, "other@example.test")
	}), ErrMerchantStoreEmailUnverified)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
	_, err = BeginMerchantStoreGuestEmailVerification(token, "new@example.test")
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
}

func TestMerchantStoreGuestEmailPayloadLeaseAndFrozenAddress(t *testing.T) {
	f := newStoreFixture(t, "balance")
	order, _, err := CreateMerchantStoreOrder(f.checkout("guest-mail-payload", "balance"))
	require.NoError(t, err)
	storeGuestEmailTestGate(t)
	token, guest := storeEmailGuestFixture(t)
	challenge, err := BeginMerchantStoreGuestEmailVerification(token, "store-buyer@example.test")
	require.NoError(t, err)
	require.NoError(t, ConfirmMerchantStoreGuestEmailVerification(token, challenge.Email, challenge.ChallengeID, challenge.Code))
	// Isolate email authorization using the paid fixture's retained ciphertext;
	// no fake account 0 or financial checkout is created by this test.
	require.NoError(t, DB.Model(order).Updates(map[string]any{"buyer_id": 0, "guest_id": guest.ID, "pickup_login_required": false}).Error)
	require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Where("order_id = ?", order.ID).Update("buyer_id", 0).Error)
	_, err = GetMerchantStoreOrderDeliveryEmail(0, order.ID)
	require.ErrorIs(t, err, ErrMerchantStoreDenied, "registered-account getters never authorize anonymous buyer zero")
	lease, err := ClaimMerchantStoreEmailDelivery(common.GetTimestamp())
	require.NoError(t, err)
	_, err = GetMerchantStoreEmailDeliveryPayload(lease.ID, strings.Repeat("a", 43))
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	payload, err := GetMerchantStoreEmailDeliveryPayload(lease.ID, lease.LeaseToken)
	require.NoError(t, err)
	require.Equal(t, challenge.Email, payload.Destination)
	require.Len(t, payload.PickupToken, 43)
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(encoded))
	storeGuestEmailAllowResend(t, guest.ID)
	changed, err := BeginMerchantStoreGuestEmailVerification(token, "different@example.test")
	require.NoError(t, err)
	require.NoError(t, ConfirmMerchantStoreGuestEmailVerification(token, changed.Email, changed.ChallengeID, changed.Code))
	require.NoError(t, DB.Model(guest).Update("expires_at", common.GetTimestamp()-1).Error)
	payload, err = GetMerchantStoreEmailDeliveryPayload(lease.ID, lease.LeaseToken)
	require.NoError(t, err, "paid obligation survives expired guest bearer")
	require.Equal(t, challenge.Email, payload.Destination, "later verified address cannot redirect this order")
	require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Where("id = ?", lease.ID).Update("lease_until", common.GetTimestamp()-1).Error)
	_, err = GetMerchantStoreEmailDeliveryPayload(lease.ID, lease.LeaseToken)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Where("id = ?", lease.ID).Updates(map[string]any{"lease_until": common.GetTimestamp() + 600, "attempts": 10}).Error)
	require.NoError(t, DB.Model(order).Update("status", "refunded").Error)
	_, err = GetMerchantStoreEmailDeliveryPayload(lease.ID, lease.LeaseToken)
	require.ErrorIs(t, err, ErrMerchantStoreEmailDeliveryClosed)
	require.NoError(t, CloseMerchantStoreEmailDelivery(lease.ID, lease.LeaseToken))
	require.ErrorIs(t, AckMerchantStoreEmailDelivery(lease.ID, lease.LeaseToken), ErrMerchantStoreConflict)
	// Exhausted crash leases are terminal, rather than stuck sending forever.
	require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Where("id = ?", lease.ID).Updates(map[string]any{"state": "sending", "lease_until": common.GetTimestamp() - 1, "lease_token": lease.LeaseToken}).Error)
	_, err = ClaimMerchantStoreEmailDelivery(common.GetTimestamp())
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	var row MerchantStoreEmailDelivery
	require.NoError(t, DB.First(&row, "id = ?", lease.ID).Error)
	require.Equal(t, "failed", row.State)
	require.Empty(t, row.LeaseToken)
}
