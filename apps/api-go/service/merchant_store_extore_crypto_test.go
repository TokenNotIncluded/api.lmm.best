package service

import (
	"errors"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"strings"
	"testing"
)

func TestMerchantStoreExtoreKeySelection(t *testing.T) {
	key := "extore-test-only-persistent-key-20261009-123456789"
	for _, tc := range []struct {
		name, merchant, crypto, session string
		ok                              bool
	}{
		{"merchant-only", key, "", "", true},
		{"crypto-only", "", key, "", true},
		{"session-compatibility", "", "", key, true},
		{"missing", "", "", "", false},
		{"weak-merchant-does-not-fall-back", "short", key, key, false},
		{"weak-crypto-does-not-fall-back", "", "short", key, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", tc.merchant)
			t.Setenv("CRYPTO_SECRET", tc.crypto)
			t.Setenv("SESSION_SECRET", tc.session)
			ciphertext, err := EncryptMerchantStoreExtoreFlow("private-verifier")
			if !tc.ok {
				if !errors.Is(err, common.ErrPersistentKeyUnavailable) || ciphertext != "" {
					t.Fatal("must fail closed", err)
				}
				return
			}
			if err != nil || strings.Contains(ciphertext, "private-verifier") {
				t.Fatal("unsafe encryption", err)
			}
			plaintext, err := DecryptMerchantStoreExtoreFlow(ciphertext)
			if err != nil || plaintext != "private-verifier" {
				t.Fatal("round trip failed", err)
			}
		})
	}
}

func TestMerchantStoreExtoreOldFlowsSurviveAddingMerchantKey(t *testing.T) {
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "")
	t.Setenv("CRYPTO_SECRET", "legacy-extore-test-only-20261009-123456789")
	old, err := EncryptMerchantStoreExtoreFlow("old-verifier")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "new-extore-test-only-20261009-987654321")
	got, err := DecryptMerchantStoreExtoreFlow(old)
	if err != nil || got != "old-verifier" {
		t.Fatal("legacy flow lost", err)
	}
	current, err := EncryptMerchantStoreExtoreFlow("new-verifier")
	if err != nil || !strings.HasPrefix(current, merchantExtoreEnvelope) {
		t.Fatal("missing key envelope", err)
	}
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "")
	if _, err := DecryptMerchantStoreExtoreFlow(current); !errors.Is(err, common.ErrPersistentKeyUnavailable) {
		t.Fatal("must not guess a fallback key", err)
	}
}
