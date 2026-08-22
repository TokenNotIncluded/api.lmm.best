package common

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPersistentStringEncryptionRequiresStrongExplicitSecret(t *testing.T) {
	const primary = "TEST_PERSISTENT_SECRET_PRIMARY"
	const fallback = "TEST_PERSISTENT_SECRET_FALLBACK"
	oldPrimary, hadPrimary := os.LookupEnv(primary)
	oldFallback, hadFallback := os.LookupEnv(fallback)
	t.Cleanup(func() {
		if hadPrimary {
			_ = os.Setenv(primary, oldPrimary)
		} else {
			_ = os.Unsetenv(primary)
		}
		if hadFallback {
			_ = os.Setenv(fallback, oldFallback)
		} else {
			_ = os.Unsetenv(fallback)
		}
	})

	require.NoError(t, os.Unsetenv(primary))
	require.NoError(t, os.Unsetenv(fallback))
	_, err := EncryptPersistentString("hero_sms.api_key", primary, fallback, "secret-value")
	require.ErrorIs(t, err, ErrPersistentSecretNotConfigured)

	require.NoError(t, os.Setenv(primary, "too-short"))
	_, err = EncryptPersistentString("hero_sms.api_key", primary, fallback, "secret-value")
	require.ErrorIs(t, err, ErrPersistentSecretNotConfigured)

	require.NoError(t, os.Setenv(primary, "REPLACE_WITH_AT_LEAST_32_RANDOM_BYTES"))
	_, err = EncryptPersistentString("hero_sms.api_key", primary, fallback, "secret-value")
	require.ErrorIs(t, err, ErrPersistentSecretNotConfigured)

	require.NoError(t, os.Setenv(primary, "0123456789abcdef0123456789abcdef"))
	ciphertext, err := EncryptPersistentString("hero_sms.api_key", primary, fallback, "secret-value")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(ciphertext, persistentSecretEnvelopeV1))
	require.NotContains(t, ciphertext, "secret-value")

	plaintext, err := DecryptPersistentString("hero_sms.api_key", primary, fallback, ciphertext)
	require.NoError(t, err)
	require.Equal(t, "secret-value", plaintext)

	_, err = DecryptPersistentString("hero_sms.payload", primary, fallback, ciphertext)
	require.Error(t, err)
}

func TestPersistentStringEncryptionUsesExplicitFallback(t *testing.T) {
	const primary = "TEST_PERSISTENT_SECRET_PRIMARY_FALLBACK"
	const fallback = "TEST_PERSISTENT_SECRET_FALLBACK_ONLY"
	t.Setenv(primary, "")
	t.Setenv(fallback, "fedcba9876543210fedcba9876543210")

	ciphertext, err := EncryptPersistentString("hero_sms.payload", primary, fallback, "email@example.test")
	require.NoError(t, err)
	plaintext, err := DecryptPersistentString("hero_sms.payload", primary, fallback, ciphertext)
	require.NoError(t, err)
	require.Equal(t, "email@example.test", plaintext)
}
