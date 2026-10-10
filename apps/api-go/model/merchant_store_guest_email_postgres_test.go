package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The existing PostgreSQL harness validates a literal loopback DSN, creates a
// random owned schema, checks the exact model registry and preserves unrelated
// legacy fixture rows/schema. This function is only called when the explicit
// test DSN is present and the actual reviewed phase-5 source is running.
func assertMerchantStoreGuestEmailPostgresSingleUse(t *testing.T) {
	require.GreaterOrEqual(t, MerchantStoreWriterCapability, 5)
	db, name, observer, _ := merchantStorePGDB(t)
	storeGuestEmailTestGate(t)
	session, err := CreateMerchantStoreGuestSession()
	require.NoError(t, err)
	guest, err := ResolveMerchantStoreGuest(db, session.Token)
	require.NoError(t, err)
	challenge, err := BeginMerchantStoreGuestEmailVerification(session.Token, "owned@example.test")
	require.NoError(t, err)
	ops := make([]func() error, 8)
	for i := range ops {
		ops[i] = func() error {
			return ConfirmMerchantStoreGuestEmailVerification(session.Token, challenge.Email, challenge.ChallengeID, challenge.Code)
		}
	}
	passed := 0
	for _, err := range merchantStorePGContendAt(t, db, observer, name, "merchant_store_guests", ops) {
		if err == nil {
			passed++
		} else {
			require.ErrorIs(t, err, ErrMerchantStoreEmailVerificationInvalid)
		}
	}
	require.Equal(t, 1, passed, "one genuinely concurrent confirmation consumes the code")
	verified, err := GetMerchantStoreGuestEmailStatus(session.Token, challenge.Email)
	require.NoError(t, err)
	require.True(t, verified)
	storeGuestEmailAllowResend(t, guest.ID)
	for i := range ops {
		ops[i] = func() error {
			_, err := BeginMerchantStoreGuestEmailVerification(session.Token, "next@example.test")
			return err
		}
	}
	passed = 0
	for _, err := range merchantStorePGContendAt(t, db, observer, name, "merchant_store_guests", ops) {
		if err == nil {
			passed++
		} else {
			require.ErrorIs(t, err, ErrMerchantStoreEmailVerificationCooldown)
		}
	}
	require.Equal(t, 1, passed, "concurrent sends share one guest-wide cooldown")
	var count int64
	require.NoError(t, db.Model(&MerchantStoreGuestEmailVerification{}).Where("guest_id = ? AND challenge_id <> ''", guest.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var original MerchantStoreGuestEmailVerification
	require.NoError(t, db.First(&original, "guest_id = ? AND email_hash = ?", guest.ID, storeHash(challenge.Email)).Error)
	require.Positive(t, original.VerifiedAt, "a subsequent challenge preserves the paid-order address fact")
}
