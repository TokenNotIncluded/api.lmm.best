package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRequiresStableExplicitEncryptionMaterial(t *testing.T) {
	previous := common.CryptoSecret
	common.CryptoSecret = "a-valid-looking-but-process-local-random-startup-key"
	t.Cleanup(func() { common.CryptoSecret = previous })
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "")
	t.Setenv("CRYPTO_SECRET", "")
	ciphertext, err := storeEncrypt("stock", "fixture-product:item", "private-card")
	require.ErrorIs(t, err, common.ErrPersistentKeyUnavailable)
	require.Empty(t, ciphertext, "process-local startup keys cannot persist shop inventory")
	t.Setenv("CRYPTO_SECRET", "fedcba9876543210fedcba9876543210")
	ciphertext, err = storeEncrypt("stock", "fixture-product:item", "private-card")
	require.NoError(t, err)
	plaintext, err := storeDecrypt("stock", "fixture-product:item", ciphertext)
	require.NoError(t, err)
	require.Equal(t, "private-card", plaintext)
	_, err = storeDecrypt("stock", "another-product:item", ciphertext)
	require.Error(t, err, "ciphertext is bound to its product/item purpose")
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "too-short")
	_, err = storeEncrypt("stock", "fixture-product:item", "private-card")
	require.ErrorIs(t, err, common.ErrPersistentKeyUnavailable, "a weak explicit primary key fails instead of silently using another key")
	t.Setenv("MERCHANT_STORE_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef")
	_, err = storeDecrypt("stock", "fixture-product:item", ciphertext)
	require.Error(t, err, "changing the active key does not silently decrypt old inventory")
	primary, err := storeEncrypt("stock", "fixture-product:item", "private-card")
	require.NoError(t, err)
	t.Setenv("CRYPTO_SECRET", "another-fallback-that-cannot-override-the-primary-key")
	plaintext, err = storeDecrypt("stock", "fixture-product:item", primary)
	require.NoError(t, err)
	require.Equal(t, "private-card", plaintext, "a stable explicit primary key takes precedence across nodes")
}
